package objectstorage

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

func envValue(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func envInt64(key string, fallback int64) (int64, error) {
	value := envValue(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1 {
		return 0, errors.New(key + " must be a positive integer")
	}
	return parsed, nil
}

// NewLifecycleFromEnv returns nil when media storage is explicitly disabled.
// Enabling it requires independent B2 and R2 credentials and four bucket names.
func NewLifecycleFromEnv() (*Lifecycle, error) {
	if strings.ToLower(envValue("MEDIA_STORAGE_ENABLED")) != "true" {
		return nil, nil
	}
	maxImage, err := envInt64("MEDIA_MAX_IMAGE_BYTES", 25<<20)
	if err != nil {
		return nil, err
	}
	maxVideo, err := envInt64("MEDIA_MAX_VIDEO_BYTES", 500<<20)
	if err != nil {
		return nil, err
	}
	uploadTTL := 15 * time.Minute
	if raw := envValue("MEDIA_UPLOAD_URL_TTL"); raw != "" {
		uploadTTL, err = time.ParseDuration(raw)
		if err != nil {
			return nil, errors.New("MEDIA_UPLOAD_URL_TTL must be a valid Go duration")
		}
	}
	downloadTTL := 10 * time.Minute
	if raw := envValue("MEDIA_DOWNLOAD_URL_TTL"); raw != "" {
		downloadTTL, err = time.ParseDuration(raw)
		if err != nil {
			return nil, errors.New("MEDIA_DOWNLOAD_URL_TTL must be a valid Go duration")
		}
	}
	return NewLifecycle(LifecycleConfig{
		B2Endpoint: envValue("B2_S3_ENDPOINT"),
		B2SigningHost: envValue("B2_S3_SIGNING_HOST"),
		B2AccessKey: envValue("B2_S3_ACCESS_KEY_ID"),
		B2SecretKey: envValue("B2_S3_SECRET_ACCESS_KEY"),
		B2Region: envValue("B2_S3_REGION"),
		B2PrivateBucket: envValue("B2_S3_PRIVATE_BUCKET"),
		B2PublicBucket: envValue("B2_S3_PUBLIC_BUCKET"),
		R2Endpoint: envValue("CLOUDFLARE_R2_ENDPOINT"),
		R2SigningHost: envValue("CLOUDFLARE_R2_SIGNING_HOST"),
		R2AccessKey: envValue("CLOUDFLARE_R2_ACCESS_KEY_ID"),
		R2SecretKey: envValue("CLOUDFLARE_R2_SECRET_ACCESS_KEY"),
		R2Region: envValue("CLOUDFLARE_R2_REGION"),
		R2PrivateBucket: envValue("CLOUDFLARE_R2_PRIVATE_BUCKET"),
		R2PublicBucket: envValue("CLOUDFLARE_R2_PUBLIC_BUCKET"),
		MaxImageBytes: maxImage,
		MaxVideoBytes: maxVideo,
		UploadTTL: uploadTTL,
		DownloadTTL: downloadTTL,
	})
}
