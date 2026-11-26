package videomedia

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveDeletesAllMediaTreesAndPreservesSiblings(t *testing.T) {
	root := t.TempDir()
	videoID := "2c6e2d3a-61c1-4e8f-8d41-92f1a0d13b62"
	siblingID := "6b8b4567-1234-4abc-8def-123456789abc"
	for _, kind := range []string{"originals", "hls", "posters"} {
		for _, id := range []string{videoID, siblingID} {
			dir := filepath.Join(root, "videos", kind, id)
			if err := os.MkdirAll(dir, 0o750); err != nil { t.Fatal(err) }
			if err := os.WriteFile(filepath.Join(dir, "media.bin"), []byte("test"), 0o600); err != nil { t.Fatal(err) }
		}
	}
	if err := Remove(root, videoID); err != nil { t.Fatal(err) }
	for _, kind := range []string{"originals", "hls", "posters"} {
		if _, err := os.Stat(filepath.Join(root, "videos", kind, videoID)); !os.IsNotExist(err) {
			t.Errorf("%s tree not removed: %v", kind, err)
		}
		if _, err := os.Stat(filepath.Join(root, "videos", kind, siblingID, "media.bin")); err != nil {
			t.Errorf("sibling %s media was touched: %v", kind, err)
		}
	}
	if err := Remove(root, videoID); err != nil { t.Fatalf("cleanup should be idempotent: %v", err) }
}

func TestRemoveRejectsUnsafeRootAndIDs(t *testing.T) {
	for _, root := range []string{"", ".", "/"} {
		if err := Remove(root, "2c6e2d3a-61c1-4e8f-8d41-92f1a0d13b62"); err == nil {
			t.Errorf("unsafe root %q accepted", root)
		}
	}
	for _, id := range []string{"", ".", "..", "../other-video", "nested/video", "2c6e2d3a-61c1-4e8f-8d41-92f1a0d13b6z"} {
		if err := Remove(t.TempDir(), id); err == nil { t.Errorf("unsafe ID %q accepted", id) }
	}
}
