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
		name string
		value string
		want time.Duration
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
	for _, digest := range []string{"", "sha256:ABCDEF", "sha256:"+string(make([]byte, 64)), "sha256:0123456789"} {
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
	if err != nil { t.Fatalf("resolveVolumePath: %v", err) }
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil { t.Fatal(err) }
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
	if err := os.Symlink(outside, link); err != nil { t.Fatal(err) }
	if _, err := resolveVolumePath(root, "escape"); err == nil {
		t.Fatal("resolveVolumePath accepted symlink escaping the storage root")
	}
}
