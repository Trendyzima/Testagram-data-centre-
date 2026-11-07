package objectstorage

import (
	"os"
	"testing"
)

func validLifecycleEnvironment(t *testing.T) {
	t.Helper()
	values := map[string]string{
		"MEDIA_STORAGE_ENABLED": "true",
		"B2_S3_ENDPOINT": "http://127.0.0.1:9000",
		"B2_S3_ACCESS_KEY_ID": "b2-access",
		"B2_S3_SECRET_ACCESS_KEY": "b2-secret",
		"B2_S3_REGION": "us-east-1",
		"B2_S3_PRIVATE_BUCKET": "testagram-private",
		"B2_S3_PUBLIC_BUCKET": "testagram-public",
		"CLOUDFLARE_R2_ENDPOINT": "http://127.0.0.1:9001",
		"CLOUDFLARE_R2_ACCESS_KEY_ID": "r2-access",
		"CLOUDFLARE_R2_SECRET_ACCESS_KEY": "r2-secret",
		"CLOUDFLARE_R2_REGION": "auto",
		"CLOUDFLARE_R2_PRIVATE_BUCKET": "testagram-r2-private",
		"CLOUDFLARE_R2_PUBLIC_BUCKET": "testagram-r2-public",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

func TestNewLifecycleFromEnvDisabledByDefault(t *testing.T) {
	t.Setenv("MEDIA_STORAGE_ENABLED", "false")
	lifecycle, err := NewLifecycleFromEnv()
	if err != nil || lifecycle != nil {
		t.Fatalf("disabled media lifecycle = %v, %v; want nil, nil", lifecycle, err)
	}
}

func TestNewLifecycleFromEnvRequiresIndependentProviderCredentials(t *testing.T) {
	t.Setenv("MEDIA_STORAGE_ENABLED", "true")
	if _, err := NewLifecycleFromEnv(); err == nil {
		t.Fatal("expected missing B2/R2 credentials to fail closed")
	}
}

func TestNewLifecycleFromEnvBuildsB2PrimaryAndR2Replica(t *testing.T) {
	validLifecycleEnvironment(t)
	lifecycle, err := NewLifecycleFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if lifecycle == nil || lifecycle.primary == nil || lifecycle.replica == nil {
		t.Fatal("expected both B2 primary and R2 replica providers")
	}
	if lifecycle.primary.EndpointHost() != "127.0.0.1:9000" || lifecycle.replica.EndpointHost() != "127.0.0.1:9001" {
		t.Fatalf("providers are not independently configured: primary=%q replica=%q",
			lifecycle.primary.EndpointHost(), lifecycle.replica.EndpointHost())
	}
}

func TestLifecycleMaxBytesAndNamespaceRouting(t *testing.T) {
	validLifecycleEnvironment(t)
	lifecycle, err := NewLifecycleFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := lifecycle.maxBytes("image/jpeg"); err != nil || got != 25<<20 {
		t.Fatalf("image max bytes = %d, %v", got, err)
	}
	if got, err := lifecycle.maxBytes("video/mp4"); err != nil || got != 500<<20 {
		t.Fatalf("video max bytes = %d, %v", got, err)
	}
	if _, err := lifecycle.maxBytes("application/pdf"); err == nil {
		t.Fatal("unsupported media content type was accepted")
	}
	b2, r2, err := lifecycle.bucketPair("media-private")
	if err != nil || b2 != "testagram-private" || r2 != "testagram-r2-private" {
		t.Fatalf("private bucket route = %q %q %v", b2, r2, err)
	}
	if _, _, err := lifecycle.bucketPair("unknown"); err == nil {
		t.Fatal("unknown namespace was accepted")
	}
}

func TestNewLifecycleRejectsSharedPrivateAndPublicBuckets(t *testing.T) {
	_ = os.Unsetenv("UNUSED_TESTAGRAM_ENV")
	_, err := NewLifecycle(LifecycleConfig{
		B2Endpoint: "http://127.0.0.1:9000", B2AccessKey: "a", B2SecretKey: "b",
		B2PrivateBucket: "same-bucket", B2PublicBucket: "same-bucket",
		R2Endpoint: "http://127.0.0.1:9001", R2AccessKey: "c", R2SecretKey: "d",
		R2PrivateBucket: "r2-private", R2PublicBucket: "r2-public",
	})
	if err == nil {
		t.Fatal("shared private/public B2 bucket was accepted")
	}
}
