package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMediaAuthRequiresBothCredentialAndVersion(t *testing.T) {
	s := &server{cfg: config{token: strings.Repeat("x", 40)}}
	for _, tc := range []struct{name, token, version string; want bool}{
		{"both", "Bearer "+s.cfg.token, "1", true},
		{"missing bearer", "", "1", false},
		{"raw token without scheme", s.cfg.token, "1", false},
		{"wrong scheme", "Basic "+s.cfg.token, "1", false},
		{"whitespace token", "Bearer "+s.cfg.token+" extra", "1", false},
		{"missing version", "Bearer "+s.cfg.token, "", false},
		{"wrong version", "Bearer "+s.cfg.token, "2", false},
		{"wrong token", "Bearer "+strings.Repeat("y", 40), "1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/internal/v1/media/status", nil)
			r.Header.Set("Authorization", tc.token)
			r.Header.Set("X-Testagram-API-Version", tc.version)
			w := httptest.NewRecorder()
			if got := s.auth(w, r); got != tc.want { t.Fatalf("auth=%v want %v", got, tc.want) }
		})
	}
}

func TestMediaUploadValidation(t *testing.T) {
	base := uploadRequest{OwnerID:"owner-1", Namespace:"media-private", Key:"users/owner-1/0123456789abcdef0123456789abcdef", ContentType:"video/mp4", SizeBytes:1024}
	if !validUpload(base) { t.Fatal("valid upload rejected") }
	cases := []uploadRequest{base, base, base, base, base, base, base, base, base}
	cases[0].Namespace = "public"
	cases[1].Key = "../escape"
	cases[2].ContentType = "video/mp4\r\nX-Evil: yes"
	cases[3].SizeBytes = 0
	cases[4].Key = "/absolute/key"
	cases[5].Key = "users//object"
	cases[6].Key = "users/another-owner/0123456789abcdef0123456789abcdef"
	cases[7].ContentType = "application/octet-stream"
	cases[8].SizeBytes = (5 << 30) + 1
	for i, in := range cases { if validUpload(in) { t.Fatalf("invalid upload case %d accepted", i) } }
}

func TestS3PresignBindsMethodAndContentType(t *testing.T) {
	store := s3Store{name:"backblaze_b2", endpoint:"https://s3.example.invalid", region:"us-west-004", bucket:"test-bucket", access:"access-key", secret:strings.Repeat("s",40)}
	put, err := presign(store, "PUT", "owner/object.mp4", 10*time.Minute, "video/mp4")
	if err != nil { t.Fatal(err) }
	if !strings.Contains(put, "X-Amz-Signature=") || !strings.Contains(put, "X-Amz-SignedHeaders=content-type%3Bhost") { t.Fatalf("unexpected signed upload URL: %s", put) }
	get, err := presign(store, "GET", "owner/object.mp4", 5*time.Minute, "")
	if err != nil { t.Fatal(err) }
	if strings.Contains(get, "content-type") || put == get { t.Fatal("GET and PUT grants must be distinct") }
}

func TestS3StoreRequiresTLSAndCleanOrigin(t *testing.T) {
	base := s3Store{name:"backblaze_b2",endpoint:"https://s3.example.invalid",region:"us-west-004",bucket:"test-bucket",access:"access-key",secret:strings.Repeat("s",40)}
	if err := validateStore(base); err != nil { t.Fatalf("valid TLS endpoint rejected: %v",err) }
	base.endpoint = "http://s3.example.invalid"
	if err := validateStore(base); err == nil { t.Fatal("plaintext S3 endpoint accepted") }
	base.endpoint = "https://s3.example.invalid?token=unsafe"
	if err := validateStore(base); err == nil { t.Fatal("S3 endpoint query string accepted") }
}

func TestObjectLockKeyIsPostgresTextSafeAndUnambiguous(t *testing.T) {
	a := objectLockKey("owner", "media-private", "a|b")
	b := objectLockKey("owner|media-private", "a", "b")
	if strings.ContainsRune(a, '\x00') || strings.ContainsRune(b, '\x00') {
		t.Fatal("advisory lock key must be valid PostgreSQL text")
	}
	if a == b {
		t.Fatal("length-prefixed lock keys must not collide across tuple boundaries")
	}
}
