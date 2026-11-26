package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveVideoMediaRemovesOriginalHLSAndPosterTrees(t *testing.T) {
	root := t.TempDir()
	videoID := "2c6e2d3a-61c1-4e8f-8d41-92f1a0d13b62"
	siblingID := "6b8b4567-1234-4abc-8def-123456789abc"

	for _, kind := range []string{"originals", "hls", "posters"} {
		for _, id := range []string{videoID, siblingID} {
			dir := filepath.Join(root, "videos", kind, id)
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatalf("create %s fixture: %v", dir, err)
			}
			if err := os.WriteFile(filepath.Join(dir, "fixture.bin"), []byte("test-media"), 0o600); err != nil {
				t.Fatalf("write %s fixture: %v", dir, err)
			}
		}
	}

	if err := removeVideoMedia(root, videoID); err != nil {
		t.Fatalf("remove video media: %v", err)
	}
	for _, kind := range []string{"originals", "hls", "posters"} {
		deleted := filepath.Join(root, "videos", kind, videoID)
		if _, err := os.Stat(deleted); !os.IsNotExist(err) {
			t.Errorf("deleted video %s tree still exists or stat failed unexpectedly: %v", kind, err)
		}
		sibling := filepath.Join(root, "videos", kind, siblingID, "fixture.bin")
		if _, err := os.Stat(sibling); err != nil {
			t.Errorf("cleanup touched sibling video media %s: %v", kind, err)
		}
	}

	// Repeated cleanup is intentionally safe for retry/reconciliation jobs.
	if err := removeVideoMedia(root, videoID); err != nil {
		t.Fatalf("repeated cleanup should be idempotent: %v", err)
	}
}

func TestRemoveVideoMediaRejectsUnsafeVideoIDs(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../other-video", "nested/video", "nested\\\\video"} {
		if err := removeVideoMedia(t.TempDir(), id); err == nil {
			t.Errorf("unsafe video ID %q was accepted", id)
		}
	}
}
