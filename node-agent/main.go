package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
	control, bootstrap, nodeToken, nodeID, name, endpoint, volumeRoot string
	mu sync.RWMutex
}

type Registration struct {
	Name string `json:"name"`
	Platform string `json:"platform"`
	Arch string `json:"arch"`
	CPUCores int `json:"cpuCores"`
	MemoryBytes int64 `json:"memoryBytes"`
	StorageBytes int64 `json:"storageBytes"`
	FreeBytes int64 `json:"freeBytes"`
	Endpoint string `json:"endpoint"`
	Capabilities map[string]any `json:"capabilities"`
}

type Workload struct {
	Image string `json:"image"`
	ImageDigest string `json:"imageDigest"`
	Command []string `json:"command"`
	Env map[string]string `json:"env"`
	CPUMillis int `json:"cpuMillis"`
	MemoryBytes int64 `json:"memoryBytes"`
	Volume string `json:"volume"`
}

type pollResponse struct {
	ID string `json:"id"`
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

func (a *Agent) register() error {
	total, free := a.disk()
	body := Registration{
		Name: a.name, Platform: runtime.GOOS, Arch: runtime.GOARCH,
		CPUCores: runtime.NumCPU(), MemoryBytes: a.memory(), StorageBytes: total, FreeBytes: free,
		Endpoint: a.endpoint,
		Capabilities: map[string]any{
			"containers": runtimeBin() != "",
			"localVolumes": true,
			"encryptedVolumes": true,
		},
	}
	var out struct {
		ID string `json:"id"`
		Token string `json:"token"`
	}
	_, err := a.post("/v1/nodes/register", body, &out)
	if err != nil {
		return err
	}
	if out.ID == "" || out.Token == "" {
		return errors.New("control plane returned incomplete node credentials")
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
		Capabilities: map[string]any{"containers": runtimeBin() != ""},
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
	rt := runtimeBin()
	if rt == "" {
		return errors.New("docker or podman is required on this execution node")
	}
	if w.Image == "" || !validImageDigest(w.ImageDigest) { return errors.New("immutable sha256 image digest required") }
	image := w.Image + "@" + w.ImageDigest
	args := []string{"run", "--rm", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges"}
	if w.CPUMillis > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(float64(w.CPUMillis)/1000, 'f', 3, 64))
	}
	if w.MemoryBytes > 0 {
		args = append(args, "--memory", strconv.FormatInt(w.MemoryBytes, 10))
	}
	for key, value := range w.Env {
		if key == "" || strings.ContainsAny(key, "=\x00\r\n") {
			return errors.New("invalid environment key")
		}
		args = append(args, "--env", key+"="+value)
	}
	if w.Volume != "" {
		root, err := filepath.Abs(a.volumeRoot)
		if err != nil {
			return err
		}
		path, err := filepath.Abs(filepath.Join(root, filepath.Clean(w.Volume)))
		if err != nil {
			return err
		}
		if path != root && !strings.HasPrefix(path, root+string(os.PathSeparator)) {
			return errors.New("volume escapes root")
		}
		args = append(args, "--volume", path+":/data:rw")
	}
	args = append(args, image)
	args = append(args, w.Command...)

	cmd := exec.Command(rt, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func validImageDigest(v string) bool { if len(v) != 71 || !strings.HasPrefix(v, "sha256:") { return false }; for _, c := range v[7:] { if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) { return false } }; return true }

func main() {
	control := os.Getenv("VPS_CONTROL_PLANE_URL")
	bootstrap := os.Getenv("VPS_NODE_BOOTSTRAP_TOKEN")
	if control == "" || bootstrap == "" {
		log.Fatal("VPS_CONTROL_PLANE_URL and VPS_NODE_BOOTSTRAP_TOKEN are required")
	}

	a := &Agent{
		control: control, bootstrap: bootstrap,
		name: os.Getenv("VPS_NODE_NAME"),
		endpoint: os.Getenv("VPS_NODE_ENDPOINT"),
		volumeRoot: os.Getenv("VPS_VOLUME_ROOT"),
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

		time.Sleep(3 * time.Second)
	}
}
