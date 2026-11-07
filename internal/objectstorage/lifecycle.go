package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type LifecycleConfig struct {
	B2Endpoint       string
	B2SigningHost    string
	B2AccessKey      string
	B2SecretKey      string
	B2Region         string
	B2PrivateBucket  string
	B2PublicBucket   string
	R2Endpoint       string
	R2SigningHost    string
	R2AccessKey      string
	R2SecretKey      string
	R2Region         string
	R2PrivateBucket  string
	R2PublicBucket   string
	MaxImageBytes    int64
	MaxVideoBytes    int64
	UploadTTL        time.Duration
	DownloadTTL      time.Duration
}

type UploadGrant struct {
	URL         string    `json:"url"`
	ExpiresAt   time.Time `json:"expires_at"`
	MaxBytes    int64     `json:"max_bytes"`
	Primary     string    `json:"primary"`
}

type VerifiedObject struct {
	Key             string    `json:"key"`
	Size            int64     `json:"size_bytes"`
	ContentType     string    `json:"content_type"`
	SHA256          string    `json:"sha256"`
	PrimaryVerified bool      `json:"primary_verified"`
	ReplicaVerified bool      `json:"replica_verified"`
	VerifiedAt      time.Time `json:"verified_at"`
}

type DownloadGrant struct {
	URL      string `json:"url"`
	Provider string `json:"provider"`
}

type Lifecycle struct {
	manager *Manager
	primary *S3Provider
	replica *S3Provider
	b2Private string
	b2Public string
	r2Private string
	r2Public string
	uploadTTL time.Duration
	downloadTTL time.Duration
	maxImageBytes int64
	maxVideoBytes int64
}

type providerRouter struct {
	providers map[string]*S3Provider
}

func (r *providerRouter) provider(bucket string) (*S3Provider, error) {
	provider := r.providers[bucket]
	if provider == nil {
		return nil, ErrInvalidNamespace
	}
	return provider, nil
}

func (r *providerRouter) PresignPut(ctx context.Context, bucket, key, contentType string, maxBytes int64, ttl time.Duration) (string, error) {
	p, err := r.provider(bucket)
	if err != nil { return "", err }
	return p.PresignPut(ctx, bucket, key, contentType, maxBytes, ttl)
}
func (r *providerRouter) PresignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	p, err := r.provider(bucket)
	if err != nil { return "", err }
	return p.PresignGet(ctx, bucket, key, ttl)
}
func (r *providerRouter) Head(ctx context.Context, bucket, key string) (ObjectInfo, error) {
	p, err := r.provider(bucket)
	if err != nil { return ObjectInfo{}, err }
	return p.Head(ctx, bucket, key)
}
func (r *providerRouter) Delete(ctx context.Context, bucket, key string) error {
	p, err := r.provider(bucket)
	if err != nil { return err }
	return p.Delete(ctx, bucket, key)
}
func (r *providerRouter) List(ctx context.Context, bucket, prefix, cursor string, limit int) ([]ObjectInfo, string, error) {
	p, err := r.provider(bucket)
	if err != nil { return nil, "", err }
	return p.List(ctx, bucket, prefix, cursor, limit)
}
func (r *providerRouter) SHA256(ctx context.Context, bucket, key string, maxBytes int64) (string, error) {
	p, err := r.provider(bucket)
	if err != nil { return "", err }
	return p.SHA256(ctx, bucket, key, maxBytes)
}

func NewLifecycle(cfg LifecycleConfig) (*Lifecycle, error) {
	if cfg.MaxImageBytes == 0 { cfg.MaxImageBytes = 25 << 20 }
	if cfg.MaxVideoBytes == 0 { cfg.MaxVideoBytes = 500 << 20 }
	if cfg.UploadTTL == 0 { cfg.UploadTTL = 15 * time.Minute }
	if cfg.DownloadTTL == 0 { cfg.DownloadTTL = 10 * time.Minute }
	if cfg.MaxImageBytes < 1 || cfg.MaxVideoBytes < 1 || cfg.MaxImageBytes > 5<<30 || cfg.MaxVideoBytes > 5<<30 {
		return nil, errors.New("media size limits must be between 1 byte and 5 GiB")
	}
	if cfg.UploadTTL < time.Minute || cfg.UploadTTL > time.Hour || cfg.DownloadTTL < time.Minute || cfg.DownloadTTL > time.Hour {
		return nil, errors.New("media URL TTL must be between 1 minute and 1 hour")
	}
	for label, bucket := range map[string]string{
		"B2 private": cfg.B2PrivateBucket, "B2 public": cfg.B2PublicBucket,
		"R2 private": cfg.R2PrivateBucket, "R2 public": cfg.R2PublicBucket,
	} {
		if !validBucket(bucket) { return nil, fmt.Errorf("%s bucket name is invalid", label) }
	}
	if cfg.B2PrivateBucket == cfg.B2PublicBucket || cfg.R2PrivateBucket == cfg.R2PublicBucket {
		return nil, errors.New("private and public buckets must be distinct for each provider")
	}
	primary, err := NewS3Provider(S3Config{
		Endpoint: cfg.B2Endpoint, SigningHost: cfg.B2SigningHost,
		AccessKey: cfg.B2AccessKey, SecretKey: cfg.B2SecretKey, Region: cfg.B2Region,
	})
	if err != nil { return nil, fmt.Errorf("configure Backblaze B2 primary: %w", err) }
	replica, err := NewS3Provider(S3Config{
		Endpoint: cfg.R2Endpoint, SigningHost: cfg.R2SigningHost,
		AccessKey: cfg.R2AccessKey, SecretKey: cfg.R2SecretKey, Region: cfg.R2Region,
	})
	if err != nil { return nil, fmt.Errorf("configure Cloudflare R2 replica: %w", err) }

	router := &providerRouter{providers: map[string]*S3Provider{
		cfg.B2PrivateBucket: primary,
		cfg.B2PublicBucket: primary,
	}}
	allowed := map[string]struct{}{
		"image/jpeg": {}, "image/png": {}, "image/webp": {}, "image/gif": {}, "image/avif": {},
		"video/mp4": {}, "video/webm": {},
	}
	namespaces := []Namespace{
		{Name: "media-private", PhysicalBucket: cfg.B2PrivateBucket, Public: false, MaxObjectBytes: cfg.MaxVideoBytes, AllowedTypes: allowed, UploadTTL: cfg.UploadTTL, DownloadTTL: cfg.DownloadTTL},
		{Name: "media-public", PhysicalBucket: cfg.B2PublicBucket, Public: true, MaxObjectBytes: cfg.MaxVideoBytes, AllowedTypes: allowed, UploadTTL: cfg.UploadTTL, DownloadTTL: cfg.DownloadTTL},
	}
	manager, err := New(router, namespaces)
	if err != nil { return nil, err }
	return &Lifecycle{
		manager: manager, primary: primary, replica: replica,
		b2Private: cfg.B2PrivateBucket, b2Public: cfg.B2PublicBucket,
		r2Private: cfg.R2PrivateBucket, r2Public: cfg.R2PublicBucket,
		uploadTTL: cfg.UploadTTL, downloadTTL: cfg.DownloadTTL,
		maxImageBytes: cfg.MaxImageBytes, maxVideoBytes: cfg.MaxVideoBytes,
	}, nil
}

func (l *Lifecycle) bucketPair(namespace string) (string, string, error) {
	switch namespace {
	case "media-private":
		return l.b2Private, l.r2Private, nil
	case "media-public":
		return l.b2Public, l.r2Public, nil
	default:
		return "", "", ErrInvalidNamespace
	}
}

func (l *Lifecycle) maxBytes(contentType string) (int64, error) {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return l.maxImageBytes, nil
	case strings.HasPrefix(contentType, "video/"):
		return l.maxVideoBytes, nil
	default:
		return 0, ErrForbidden
	}
}

func (l *Lifecycle) UploadURL(ctx context.Context, tenantID, namespace, key, contentType string, size int64) (UploadGrant, error) {
	maxBytes, err := l.maxBytes(contentType)
	if err != nil { return UploadGrant{}, err }
	if size > maxBytes { return UploadGrant{}, ErrTooLarge }
	_, _, err := l.bucketPair(namespace)
	if err != nil { return UploadGrant{}, err }
	url, err := l.manager.UploadURL(ctx, tenantID, namespace, key, contentType, size)
	if err != nil { return UploadGrant{}, err }
	return UploadGrant{URL: url, ExpiresAt: time.Now().UTC().Add(l.uploadTTL), MaxBytes: size, Primary: "backblaze_b2"}, nil
}

func (l *Lifecycle) VerifyAndReplicate(ctx context.Context, tenantID, namespace, key, contentType, sha256Hex string, size int64) (VerifiedObject, error) {
	maxBytes, err := l.maxBytes(contentType)
	if err != nil { return VerifiedObject{}, err }
	if size > maxBytes { return VerifiedObject{}, ErrTooLarge }
	b2Bucket, r2Bucket, err := l.bucketPair(namespace)
	if err != nil { return VerifiedObject{}, err }
	info, err := l.manager.VerifyUpload(ctx, tenantID, namespace, key, contentType, size)
	if err != nil { return VerifiedObject{}, err }
	if err := l.manager.VerifyUploadChecksum(ctx, tenantID, namespace, key, size, sha256Hex); err != nil {
		return VerifiedObject{}, err
	}
	scopedKey := tenantPrefix(tenantID) + key
	if err := l.replica.CopyFrom(ctx, l.primary, b2Bucket, scopedKey, r2Bucket, scopedKey, size, contentType, sha256Hex); err != nil {
		return VerifiedObject{}, fmt.Errorf("verify B2 primary and replicate to R2: %w", err)
	}
	return VerifiedObject{
		Key: key, Size: info.Size, ContentType: info.ContentType, SHA256: strings.ToLower(sha256Hex),
		PrimaryVerified: true, ReplicaVerified: true, VerifiedAt: time.Now().UTC(),
	}, nil
}

func (l *Lifecycle) DownloadURL(ctx context.Context, requesterTenant, ownerTenant, namespace, key, sha256Hex string, size int64) (DownloadGrant, error) {
	// Manager performs tenant ownership and private/public namespace checks.
	if _, err := l.manager.DownloadURL(ctx, requesterTenant, ownerTenant, namespace, key); err != nil {
		return DownloadGrant{}, err
	}
	b2Bucket, r2Bucket, err := l.bucketPair(namespace)
	if err != nil { return DownloadGrant{}, err }
	if size < 1 || size > l.maxVideoBytes || len(sha256Hex) != 64 {
		return DownloadGrant{}, ErrObjectMismatch
	}
	scopedKey := tenantPrefix(ownerTenant) + key
	if info, headErr := l.replica.Head(ctx, r2Bucket, scopedKey); headErr == nil && info.Size == size {
		if digest, hashErr := l.replica.SHA256(ctx, r2Bucket, scopedKey, size); hashErr == nil && strings.EqualFold(digest, sha256Hex) {
			signed, signErr := l.replica.PresignGet(ctx, r2Bucket, scopedKey, l.downloadTTL)
			if signErr == nil { return DownloadGrant{URL: signed, Provider: "cloudflare_r2"}, nil }
		}
	}
	info, err := l.primary.Head(ctx, b2Bucket, scopedKey)
	if err != nil { return DownloadGrant{}, err }
	if info.Size != size { return DownloadGrant{}, ErrObjectMismatch }
	digest, err := l.primary.SHA256(ctx, b2Bucket, scopedKey, size)
	if err != nil { return DownloadGrant{}, err }
	if !strings.EqualFold(digest, sha256Hex) { return DownloadGrant{}, ErrChecksumMismatch }
	signed, err := l.primary.PresignGet(ctx, b2Bucket, scopedKey, l.downloadTTL)
	if err != nil { return DownloadGrant{}, err }
	return DownloadGrant{URL: signed, Provider: "backblaze_b2"}, nil
}

func (l *Lifecycle) Delete(ctx context.Context, requesterTenant, ownerTenant, namespace, key string) error {
	_, r2Bucket, err := l.bucketPair(namespace)
	if err != nil {
		return err
	}
	primaryErr := l.manager.Delete(ctx, requesterTenant, ownerTenant, namespace, key)
	replicaErr := l.replica.Delete(ctx, r2Bucket, tenantPrefix(ownerTenant)+key)
	return errors.Join(primaryErr, replicaErr)
}
