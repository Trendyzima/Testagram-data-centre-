package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckMediaRootAcceptsWritableDirectory(t *testing.T) {
	root := t.TempDir()
	if err := checkMediaRoot(root); err != nil {
		t.Fatalf("checkMediaRoot(%q): %v", root, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("healthcheck probe was not cleaned up: %v", entries)
	}
}

func TestCheckMediaRootRejectsMissingDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	if err := checkMediaRoot(root); err == nil {
		t.Fatal("expected missing media root to fail")
	}
}

func TestCheckMediaRootRejectsFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("probe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkMediaRoot(root); err == nil {
		t.Fatal("expected a file to fail media-root validation")
	}
}

func TestHealthcheckRequiresDatabaseURL(t *testing.T) {
	if err := healthcheck(t.Context(), "", t.TempDir()); err == nil {
		t.Fatal("expected empty DATABASE_URL to fail")
	}
}
