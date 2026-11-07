package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Agent struct {
	control, bootstrap, nodeToken, nodeID, name, endpoint, volumeRoot, storageRoot, stateFile string
	workloadTimeout                                                                           time.Duration
	computeEnabled                                                                            bool
	storageEnabled                                                                            bool
	mu                                                                                        sync.RWMutex
	storageStatsMu                                                                            sync.Mutex
	storageStatsAt                                                                            time.Time
	storageStatsTotal                                                                         int64
	storageStatsFree                                                                          int64
}

type Registration struct {
	Name         string         `json:"name"`
	Platform     string         `json:"platform"`
	Arch         string         `json:"arch"`
	CPUCores     int            `json:"cpuCores"`
	MemoryBytes  int64          `json:"memoryBytes"`
	StorageBytes int64          `json:"storageBytes"`
	FreeBytes    int64          `json:"freeBytes"`
	Endpoint     string         `json:"endpoint"`
	Capabilities map[string]any `json:"capabilities"`
}

type Workload struct {
	Image        string            `json:"image"`
	ImageDigest  string            `json:"imageDigest"`
	Command      []string          `json:"command"`
	Env          map[string]string `json:"env"`
	CPUMillis    int               `json:"cpuMillis"`
	MemoryBytes  int64             `json:"memoryBytes"`
	StorageBytes int64             `json:"storageBytes"`
	Volume       string            `json:"volume"`
}

type pollResponse struct {
	ID       string   `json:"id"`
	Workload Workload `json:"workload"`
}

func (a *Agent) token() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.nodeToken
}

func (a *Agent) authHeader() string {
	if t := a.token(); t != "" {
		return "Bearer " + t
	}
	return "Bearer " + a.bootstrap
}

func runtimeBin() string {
	for _, name := range []string{"docker", "podman"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func (a *Agent) disk() (int64, int64) {
	if total, err := strconv.ParseInt(os.Getenv("VPS_STORAGE_BYTES"), 10, 64); err == nil && total > 0 {
		free, _ := strconv.ParseInt(os.Getenv("VPS_FREE_BYTES"), 10, 64)
		return total, free
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(a.volumeRoot, &stat); err != nil {
		return 0, 0
	}
	return int64(stat.Blocks) * int64(stat.Bsize), int64(stat.Bavail) * int64(stat.Bsize)
}

func envEnabled(name string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(name)), "true")
}

func (a *Agent) mediaDisk() (int64, int64) {
	a.storageStatsMu.Lock()
	defer a.storageStatsMu.Unlock()
	if !a.storageEnabled || strings.TrimSpace(a.storageRoot) == "" {
		a.storageStatsAt = time.Now()
		a.storageStatsTotal, a.storageStatsFree = 0, 0
		return 0, 0
	}
	if !a.storageStatsAt.IsZero() && time.Since(a.storageStatsAt) < 30*time.Second {
		return a.storageStatsTotal, a.storageStatsFree
	}
	quota, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("VPS_STORAGE_QUOTA_BYTES")), 10, 64)
	if err != nil || quota <= 0 {
		a.storageStatsAt = time.Now()
		a.storageStatsTotal, a.storageStatsFree = 0, 0
		return 0, 0
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(a.storageRoot, &stat); err != nil {
		a.storageStatsAt = time.Now()
		a.storageStatsTotal, a.storageStatsFree = 0, 0
		return 0, 0
	}
	used, err := directoryBytes(a.storageRoot)
	if err != nil || used >= quota {
		total := min64(quota, int64(stat.Blocks)*int64(stat.Bsize))
		a.storageStatsAt = time.Now()
		a.storageStatsTotal, a.storageStatsFree = total, 0
		return total, 0
	}
	total := min64(quota, int64(stat.Blocks)*int64(stat.Bsize))
	free := min64(quota-used, int64(stat.Bavail)*int64(stat.Bsize))
	a.storageStatsAt = time.Now()
	a.storageStatsTotal, a.storageStatsFree = total, free
	return total, free
}

func directoryBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func validateStorageRoot(storageRoot, computeRoot string) error {
	storageRoot = filepath.Clean(storageRoot)
	computeRoot = filepath.Clean(computeRoot)
	if !filepath.IsAbs(storageRoot) || storageRoot == string(os.PathSeparator) || storageRoot == computeRoot {
		return errors.New("VPS_STORAGE_ROOT must be a dedicated absolute path, separate from VPS_VOLUME_ROOT")
	}
	rel, err := filepath.Rel(storageRoot, computeRoot)
	if err != nil {
		return err
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return errors.New("compute volume root must not be inside the media storage root")
	}
	rel, err = filepath.Rel(computeRoot, storageRoot)
	if err != nil {
		return err
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return errors.New("media storage root must not be inside the compute volume root")
	}
	return nil
}

func (a *Agent) capabilities() map[string]any {
	mediaTotal, mediaFree := a.mediaDisk()
	return map[string]any{
		"containers":            runtimeBin() != "",
		"localVolumes":          true,
		"computeNode":           a.computeEnabled,
		"storageNode":           a.storageEnabled,
		"storageOnly":           a.storageEnabled && !a.computeEnabled,
		"workloadExecution":     a.computeEnabled,
		"mediaStorageBytes":     mediaTotal,
		"mediaStorageFreeBytes": mediaFree,
	}
}

func (a *Agent) post(path string, body any, out any) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(a.control, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", a.authHeader())
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNoContent {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, errors.New(string(data))
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func (a *Agent) memory() int64 {
	if value, err := strconv.ParseInt(os.Getenv("VPS_MEMORY_BYTES"), 10, 64); err == nil && value > 0 {
		return value
	}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, _ := strconv.ParseInt(fields[1], 10, 64)
			return kb * 1024
		}
	}
	return 0
}

func (a *Agent) loadState() error {
	data, err := os.ReadFile(a.stateFile)
	if err != nil {
		return nil
	}
	var state struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.ID == "" || state.Token == "" {
		return errors.New("invalid node state")
	}
	a.mu.Lock()
	a.nodeID, a.nodeToken = state.ID, state.Token
	a.mu.Unlock()
	return nil
}

func (a *Agent) saveState(id, token string) error {
	if err := os.MkdirAll(filepath.Dir(a.stateFile), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}{id, token})
	if err != nil {
		return err
	}
	tmp := a.stateFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, a.stateFile)
}

func (a *Agent) register() error {
	total, free := a.disk()
	body := Registration{
		Name: a.name, Platform: runtime.GOOS, Arch: runtime.GOARCH,
		CPUCores: runtime.NumCPU(), MemoryBytes: a.memory(), StorageBytes: total, FreeBytes: free,
		Endpoint:     a.endpoint,
		Capabilities: a.capabilities(),
	}
	var out struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	_, err := a.post("/v1/nodes/register", body, &out)
	if err != nil {
		return err
	}
	if out.ID == "" || out.Token == "" {
		return errors.New("control plane returned incomplete node credentials")
	}
	if err := a.saveState(out.ID, out.Token); err != nil {
		return err
	}
	a.mu.Lock()
	a.nodeID, a.nodeToken = out.ID, out.Token
	a.mu.Unlock()
	return nil
}

func (a *Agent) heartbeat() error {
	total, free := a.disk()
	body := Registration{
		CPUCores: runtime.NumCPU(), MemoryBytes: a.memory(), StorageBytes: total, FreeBytes: free,
		Capabilities: a.capabilities(),
	}
	_, err := a.post("/v1/nodes/heartbeat", body, nil)
	return err
}

func (a *Agent) poll() (*pollResponse, error) {
	var out pollResponse
	code, err := a.post("/v1/nodes/poll", map[string]any{}, &out)
	if code == http.StatusNoContent {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if out.ID == "" || out.Workload.Image == "" {
		return nil, errors.New("control plane returned an incomplete workload")
	}
	return &out, nil
}

func (a *Agent) report(id string, success bool) error {
	_, err := a.post("/v1/workloads/"+id+"/result", map[string]bool{"success": success}, nil)
	return err
}

func (a *Agent) execute(w Workload) error {
	if !a.computeEnabled {
		return errors.New("compute workload execution is disabled on this node")
	}
	if w.StorageBytes > 0 {
		_, free := a.disk()
		if free < w.StorageBytes {
			return errors.New("insufficient free disk space for workload storage reservation")
		}
	}
	rt := runtimeBin()
	if rt == "" {
		return errors.New("docker or podman is required on this execution node")
	}
	if w.Image == "" || !validImageDigest(w.ImageDigest) {
		return errors.New("immutable sha256 image digest required")
	}
	image := w.Image + "@" + w.ImageDigest
	if !allowedWorkloadImage(image, os.Getenv("VPS_ALLOWED_WORKLOAD_IMAGES")) {
		return errors.New("workload image is not in VPS_ALLOWED_WORKLOAD_IMAGES")
	}
	if len(w.Command) == 0 || strings.TrimSpace(w.Command[0]) == "" {
		return errors.New("workload command is required")
	}
	if w.CPUMillis <= 0 || w.CPUMillis > 2000 {
		return errors.New("workload CPU request must be between 1 and 2000 millicores")
	}
	if w.MemoryBytes <= 0 || w.MemoryBytes > 4*1024*1024*1024 {
		return errors.New("workload memory request must be between 1 byte and 4 GiB")
	}
	cidFile, err := os.CreateTemp("", "testagram-workload-*.cid")
	if err != nil {
		return err
	}
	cidPath := cidFile.Name()
	if err := cidFile.Close(); err != nil {
		_ = os.Remove(cidPath)
		return err
	}
	if err := os.Remove(cidPath); err != nil {
		return err
	}
	defer os.Remove(cidPath)
	args := []string{"run", "--cidfile", cidPath, "--rm", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=256", "--network=none"}
	if w.CPUMillis > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(float64(w.CPUMillis)/1000, 'f', 3, 64))
	}
	if w.MemoryBytes > 0 {
		memory := strconv.FormatInt(w.MemoryBytes, 10)
		args = append(args, "--memory", memory, "--memory-swap", memory)
	}
	for key, value := range w.Env {
		if key == "" || strings.ContainsAny(key, "=\x00\r\n") {
			return errors.New("invalid environment key")
		}
		args = append(args, "--env", key+"="+value)
	}
	if w.Volume != "" {
		path, err := resolveVolumePath(a.volumeRoot, w.Volume)
		if err != nil {
			return err
		}
		args = append(args, "--volume", path+":/data:rw")
	}
	args = append(args, image)
	args = append(args, w.Command...)

	ctx, cancel := context.WithTimeout(context.Background(), a.workloadTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, rt, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		// Killing the runtime CLI does not guarantee that the container stops.
		// Best-effort force removal by its recorded ID prevents orphaned work.
		if data, readErr := os.ReadFile(cidPath); readErr == nil {
			id := strings.TrimSpace(string(data))
			if validContainerID(id) {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
				cleanup := exec.CommandContext(cleanupCtx, rt, "rm", "-f", id)
				_ = cleanup.Run()
				cleanupCancel()
			}
		}
		return fmt.Errorf("workload execution timed out after %s", a.workloadTimeout)
	}
	return err
}

func allowedWorkloadImage(image, configured string) bool {
	if strings.TrimSpace(configured) == "" {
		return false
	}
	for _, candidate := range strings.Split(configured, ",") {
		if strings.TrimSpace(candidate) == image {
			return true
		}
	}
	return false
}

// resolveVolumePath accepts a single volume name, never a host path. It
// resolves symlinks after creation and verifies the canonical path remains
// below the canonical root before exposing it to a container.
func resolveVolumePath(root, volume string) (string, error) {
	if strings.TrimSpace(volume) == "" || volume == "." || volume == ".." ||
		filepath.IsAbs(volume) || strings.ContainsAny(volume, "/\\\\") ||
		strings.ContainsRune(volume, 0) {
		return "", errors.New("volume must be a single safe name")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	canonicalRoot, err = filepath.Abs(canonicalRoot)
	if err != nil {
		return "", err
	}
	target := filepath.Join(canonicalRoot, volume)
	if err := os.MkdirAll(target, 0700); err != nil {
		return "", err
	}
	canonicalTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	canonicalTarget, err = filepath.Abs(canonicalTarget)
	if err != nil {
		return "", err
	}
	if canonicalTarget == canonicalRoot || !strings.HasPrefix(canonicalTarget, canonicalRoot+string(os.PathSeparator)) {
		return "", errors.New("volume resolves outside storage root")
	}
	return canonicalTarget, nil
}

func parseWorkloadTimeout(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 30 * time.Minute, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	if d < time.Second || d > 24*time.Hour {
		return 0, errors.New("must be between 1s and 24h")
	}
	return d, nil
}

func validContainerID(id string) bool {
	if len(id) < 12 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !((c >= 48 && c <= 57) || (c >= 97 && c <= 102)) {
			return false
		}
	}
	return true
}

func validImageDigest(v string) bool {
	if len(v) != 71 || !strings.HasPrefix(v, "sha256:") {
		return false
	}
	for _, c := range v[7:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func main() {
	control := os.Getenv("VPS_CONTROL_PLANE_URL")
	bootstrap := os.Getenv("VPS_NODE_BOOTSTRAP_TOKEN")
	if control == "" || bootstrap == "" {
		log.Fatal("VPS_CONTROL_PLANE_URL and VPS_NODE_BOOTSTRAP_TOKEN are required")
	}

	a := &Agent{
		control: control, bootstrap: bootstrap,
		name:           os.Getenv("VPS_NODE_NAME"),
		endpoint:       os.Getenv("VPS_NODE_ENDPOINT"),
		volumeRoot:     os.Getenv("VPS_VOLUME_ROOT"),
		storageRoot:    os.Getenv("VPS_STORAGE_ROOT"),
		stateFile:      os.Getenv("VPS_NODE_STATE_FILE"),
		computeEnabled: envEnabled("VPS_ENABLE_COMPUTE_NODE") && strings.TrimSpace(os.Getenv("VPS_ALLOWED_WORKLOAD_IMAGES")) != "" && runtimeBin() != "",
		storageEnabled: envEnabled("VPS_ENABLE_STORAGE_NODE"),
	}
	if strings.TrimSpace(os.Getenv("VPS_ALLOWED_WORKLOAD_IMAGES")) == "" {
		log.Printf("workload execution is disabled until VPS_ALLOWED_WORKLOAD_IMAGES contains approved image@sha256 references")
	}
	if a.name == "" {
		a.name = "testagram-node"
	}
	if a.endpoint == "" {
		a.endpoint = "outbound-only"
	}
	if a.volumeRoot == "" {
		a.volumeRoot = "/var/lib/testagram/storage/nodes"
	}
	timeout, err := parseWorkloadTimeout(os.Getenv("VPS_WORKLOAD_TIMEOUT"))
	if err != nil {
		log.Fatalf("invalid VPS_WORKLOAD_TIMEOUT: %v", err)
	}
	a.workloadTimeout = timeout
	if a.storageEnabled {
		if a.storageRoot == "" {
			log.Fatal("VPS_STORAGE_ROOT is required when VPS_ENABLE_STORAGE_NODE=true")
		}
		quota, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("VPS_STORAGE_QUOTA_BYTES")), 10, 64)
		if err != nil || quota <= 0 {
			log.Fatal("VPS_STORAGE_QUOTA_BYTES must be a positive integer when storage is enabled")
		}
		if err := validateStorageRoot(a.storageRoot, a.volumeRoot); err != nil {
			log.Fatal(err)
		}
		if err := os.MkdirAll(a.volumeRoot, 0700); err != nil {
			log.Fatal(err)
		}
		if err := os.MkdirAll(a.storageRoot, 0700); err != nil {
			log.Fatal(err)
		}
		storageCanonical, err := filepath.EvalSymlinks(a.storageRoot)
		if err != nil {
			log.Fatal(err)
		}
		computeCanonical, err := filepath.EvalSymlinks(a.volumeRoot)
		if err != nil {
			log.Fatal(err)
		}
		if err := validateStorageRoot(storageCanonical, computeCanonical); err != nil {
			log.Fatal(err)
		}
		a.storageRoot = storageCanonical
	}
	if a.stateFile == "" {
		a.stateFile = "/var/lib/testagram/storage/nodes/node-state.json"
	}
	if err := a.loadState(); err != nil {
		log.Printf("load node state: %v", err)
		a.mu.Lock()
		a.nodeToken = ""
		a.mu.Unlock()
	}
	if err := os.MkdirAll(a.volumeRoot, 0700); err != nil {
		log.Fatal(err)
	}

	for {
		if a.token() == "" {
			if err := a.register(); err != nil {
				log.Printf("register: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}
		}

		if err := a.heartbeat(); err != nil {
			log.Printf("heartbeat: %v", err)
			a.mu.Lock()
			a.nodeToken = ""
			a.mu.Unlock()
			time.Sleep(2 * time.Second)
			continue
		}

		if a.computeEnabled {
			if work, err := a.poll(); err != nil {
				log.Printf("poll: %v", err)
			} else if work != nil {
				err = a.execute(work.Workload)
				if reportErr := a.report(work.ID, err == nil); reportErr != nil {
					log.Printf("report %s: %v", work.ID, reportErr)
				}
				if err != nil {
					log.Printf("workload %s failed: %v", work.ID, err)
				}
			}
		}

		time.Sleep(3 * time.Second)
	}
}
