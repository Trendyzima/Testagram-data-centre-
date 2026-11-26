package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// removeVideoMedia removes every local media tree belonging to one video.
// It attempts all three removals and reports every failure so an operator can
// identify incomplete cleanup without hiding successful removals.
func removeVideoMedia(mediaRoot, videoID string) error {
	if strings.TrimSpace(videoID) == "" || videoID == "." || videoID == ".." ||
		strings.ContainsAny(videoID, `/\\`) {
		return fmt.Errorf("invalid video ID for media cleanup")
	}

	paths := []string{
		filepath.Join(mediaRoot, "videos", "originals", videoID),
		filepath.Join(mediaRoot, "videos", "hls", videoID),
		filepath.Join(mediaRoot, "videos", "posters", videoID),
	}
	var cleanupErrors []error
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove %s: %w", path, err))
		}
	}
	return errors.Join(cleanupErrors...)
}
