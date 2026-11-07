package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseWorkloadTimeout(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{name: "default", want: 30 * time.Minute},
		{name: "configured", value: "45m", want: 45 * time.Minute},
		{name: "minimum", value: "1s", want: time.Second},
		{name: "too short", value: "500ms", wantErr: true},
		{name: "too long", value: "25h", wantErr: true},
		{name: "invalid", value: "tomorrow", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWorkloadTimeout(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseWorkloadTimeout(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("parseWorkloadTimeout(%q) = %s, want %s", tt.value, got, tt.want)
			}
		})
	}
}

func TestValidContainerID(t *testing.T) {
	for _, id := range []string{
		"0123456789ab",
		"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	} {
		if !validContainerID(id) {
			t.Errorf("validContainerID(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", "0123456789a", "0123456789az", "../0123456789ab", "0123456789ab\n"} {
		if validContainerID(id) {
			t.Errorf("validContainerID(%q) = true, want false", id)
		}
	}
}

func TestValidImageDigest(t *testing.T) {
	if !validImageDigest("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef") {
		t.Fatal("valid sha256 digest was rejected")
	}
	for _, digest := range []string{"", "sha256:ABCDEF", "sha256:" + string(make([]byte, 64)), "sha256:0123456789"} {
		if validImageDigest(digest) {
			t.Errorf("validImageDigest(%q) = true, want false", digest)
		}
	}
}

func TestAllowedWorkloadImage(t *testing.T) {
	image := "registry.example/testagram/worker@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, configured := range []string{image, "other/image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa, " + image} {
		if !allowedWorkloadImage(image, configured) {
			t.Errorf("allowedWorkloadImage rejected configured image %q", configured)
		}
	}
	for _, configured := range []string{"", " ", "registry.example/testagram/worker", "registry.example/testagram/worker@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if allowedWorkloadImage(image, configured) {
			t.Errorf("allowedWorkloadImage accepted unapproved image with config %q", configured)
		}
	}
}

func TestResolveVolumePath(t *testing.T) {
	root := t.TempDir()
	got, err := resolveVolumePath(root, "account-123")
	if err != nil {
		t.Fatalf("resolveVolumePath: %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, canonicalRoot+string(os.PathSeparator)) {
		t.Fatalf("resolved volume %q is outside root %q", got, canonicalRoot)
	}
	for _, name := range []string{"", ".", "..", "../escape", "/tmp/escape", "nested/name", `nested\\name`} {
		if _, err := resolveVolumePath(root, name); err == nil {
			t.Errorf("resolveVolumePath accepted unsafe name %q", name)
		}
	}
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveVolumePath(root, "escape"); err == nil {
		t.Fatal("resolveVolumePath accepted symlink escaping the storage root")
	}
}

func TestNodeCapabilitiesSeparateComputeAndMediaStorage(t *testing.T) {
	compute := (&Agent{computeEnabled: true}).capabilities()
	if compute["computeNode"] != true || compute["workloadExecution"] != true || compute["storageNode"] != false || compute["storageOnly"] != false {
		t.Fatalf("unexpected compute-only capabilities: %#v", compute)
	}
	storage := (&Agent{storageEnabled: true}).capabilities()
	if storage["storageNode"] != true || storage["storageOnly"] != true || storage["computeNode"] != false || storage["workloadExecution"] != false {
		t.Fatalf("unexpected storage-only capabilities: %#v", storage)
	}
	both := (&Agent{computeEnabled: true, storageEnabled: true}).capabilities()
	if both["storageNode"] != true || both["storageOnly"] != false || both["computeNode"] != true || both["workloadExecution"] != true {
		t.Fatalf("unexpected dual-capability node: %#v", both)
	}
}

func TestStorageRootMustBeSeparateAndDedicated(t *testing.T) {
	if err := validateStorageRoot("/", "/var/lib/testagram/compute"); err == nil {
		t.Fatal("filesystem root must not be accepted as media storage")
	}
	if err := validateStorageRoot("/var/lib/testagram/compute", "/var/lib/testagram/compute"); err == nil {
		t.Fatal("same path must not be used for media and compute volumes")
	}
	if err := validateStorageRoot("/var/lib/testagram", "/var/lib/testagram/compute"); err == nil {
		t.Fatal("media root containing compute root must be rejected")
	}
	if err := validateStorageRoot("/var/lib/testagram/compute", "/var/lib/testagram"); err == nil {
		t.Fatal("compute root containing media root must be rejected")
	}
	if err := validateStorageRoot("/srv/testagram-media", "/var/lib/testagram/compute"); err != nil {
		t.Fatalf("separate roots rejected: %v", err)
	}
}

func TestMediaDiskUsesExplicitQuotaAndDirectoryUsage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "asset.bin"), make([]byte, 4096), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VPS_STORAGE_QUOTA_BYTES", "8192")
	device, err := filesystemDevice(root)
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{storageEnabled: true, storageRoot: root, storageDevice: device, storageDeviceSet: true}
	total, free := a.mediaDisk()
	if total <= 0 || total > 8192 {
		t.Fatalf("unexpected total quota: %d", total)
	}
	if free > 4096 {
		t.Fatalf("used media bytes were not deducted from quota: free=%d", free)
	}
	a.storageEnabled = false
	if total, free := a.mediaDisk(); total != 0 || free != 0 {
		t.Fatalf("disabled storage node advertised capacity: total=%d free=%d", total, free)
	}
}

func TestDirectoryBytesDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.bin"), make([]byte, 16384), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "external")); err != nil {
		t.Fatal(err)
	}
	got, err := directoryBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("symlink target was counted as node storage: %d", got)
	}
}

func TestComputeExecutionDisabledByDefault(t *testing.T) {
	a := &Agent{}
	if err := a.execute(Workload{}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled compute guard, got %v", err)
	}
}

func TestValidateStorageDeviceRejectsSystemDiskUnlessExplicitlyAllowed(t *testing.T) {
	root := t.TempDir()
	if _, err := validateStorageDevice(root, false); err == nil {
		t.Fatal("storage on the operating-system filesystem must be rejected by default")
	}
	device, err := validateStorageDevice(root, true)
	if err != nil || device == 0 {
		t.Fatalf("explicit system-disk override should be accepted for development: device=%d err=%v", device, err)
	}
}

func TestMediaDiskDoesNotReportCapacityAfterMountedVolumeChanges(t *testing.T) {
	root := t.TempDir()
	device, err := filesystemDevice(root)
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{storageEnabled: true, storageRoot: root, storageDevice: device + 1, storageDeviceSet: true}
	if total, free := a.mediaDisk(); total != 0 || free != 0 {
		t.Fatalf("detached or replaced volume must report zero capacity, got total=%d free=%d", total, free)
	}
}
