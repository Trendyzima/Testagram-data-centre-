package videomedia

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Remove deletes all local media trees for a canonical UUID. Requiring an
// absolute, non-root media directory and a canonical UUID prevents accidental
// cleanup relative to the process working directory or outside the media root.
func Remove(mediaRoot, videoID string) error {
	root := filepath.Clean(strings.TrimSpace(mediaRoot))
	if root == "" || !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return fmt.Errorf("invalid video media root")
	}
	if len(videoID) != 36 || videoID[8] != '-' || videoID[13] != '-' ||
		videoID[18] != '-' || videoID[23] != '-' {
		return fmt.Errorf("invalid video ID for media cleanup")
	}
	compact := strings.ReplaceAll(videoID, "-", "")
	if len(compact) != 32 {
		return fmt.Errorf("invalid video ID for media cleanup")
	}
	if _, err := hex.DecodeString(compact); err != nil {
		return fmt.Errorf("invalid video ID for media cleanup")
	}

	paths := []string{
		filepath.Join(root, "videos", "originals", videoID),
		filepath.Join(root, "videos", "hls", videoID),
		filepath.Join(root, "videos", "posters", videoID),
	}
	var cleanupErrors []error
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove %s: %w", path, err))
		}
	}
	return errors.Join(cleanupErrors...)
}
