package main

import (
	"github.com/Trendyzima/Testagram-data-centre-/internal/videomedia"
)

// Keep the existing video-service helper as a narrow adapter while the worker
// imports the same path-safe implementation directly.
func removeVideoMedia(mediaRoot, videoID string) error {
	return videomedia.Remove(mediaRoot, videoID)
}
