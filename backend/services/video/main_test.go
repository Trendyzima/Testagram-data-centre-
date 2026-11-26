package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func signNativeVideoTestToken(t *testing.T, claims nativeVideoAuthClaims, secret []byte, algorithm string) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": algorithm, "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestParseNativeVideoAccessToken(t *testing.T) {
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	secret := []byte("testagram-native-auth-test-secret-32-bytes")
	claims := nativeVideoAuthClaims{
		Subject: "2c6e2d3a-61c1-4e8f-8d41-92f1a0d13b62",
		Email: "creator@example.test",
		Role: "authenticated",
		Issuer: "testagram-auth",
		Audience: "testagram-api",
		IssuedAt: now.Unix(),
		ExpiresAt: now.Add(15 * time.Minute).Unix(),
		TokenVersion: 1,
		SessionID: "5b7d8f90-0e31-4e4b-9c8a-2f3a4b5c6d7e",
	}
	t.Run("accepts valid Backend access token", func(t *testing.T) {
		token := signNativeVideoTestToken(t, claims, secret, "HS256")
		got, ok := parseNativeVideoAccessToken(token, secret, now)
		if !ok || got.Subject != claims.Subject || got.SessionID != claims.SessionID {
			t.Fatalf("valid token rejected: claims=%+v ok=%v", got, ok)
		}
	})
	t.Run("rejects altered signature", func(t *testing.T) {
		token := signNativeVideoTestToken(t, claims, secret, "HS256")
		token += "x"
		if _, ok := parseNativeVideoAccessToken(token, secret, now); ok {
			t.Fatal("altered token signature was accepted")
		}
	})
	t.Run("rejects another signing secret", func(t *testing.T) {
		token := signNativeVideoTestToken(t, claims, secret, "HS256")
		if _, ok := parseNativeVideoAccessToken(token, []byte("different-native-auth-secret-at-least-32"), now); ok {
			t.Fatal("token signed by another provider secret was accepted")
		}
	})
	t.Run("rejects algorithm substitution", func(t *testing.T) {
		token := signNativeVideoTestToken(t, claims, secret, "none")
		if _, ok := parseNativeVideoAccessToken(token, secret, now); ok {
			t.Fatal("unsupported algorithm was accepted")
		}
	})
	t.Run("rejects wrong issuer or audience", func(t *testing.T) {
		for name, mutate := range map[string]func(*nativeVideoAuthClaims){
			"issuer": func(c *nativeVideoAuthClaims) { c.Issuer = "untrusted-external-issuer" },
			"audience": func(c *nativeVideoAuthClaims) { c.Audience = "another-service" },
		} {
			t.Run(name, func(t *testing.T) {
				changed := claims
				mutate(&changed)
				token := signNativeVideoTestToken(t, changed, secret, "HS256")
				if _, ok := parseNativeVideoAccessToken(token, secret, now); ok {
					t.Fatal("foreign issuer/audience was accepted")
				}
			})
		}
	})
	t.Run("rejects expired or excessive lifetime", func(t *testing.T) {
		for name, mutate := range map[string]func(*nativeVideoAuthClaims){
			"expired": func(c *nativeVideoAuthClaims) { c.ExpiresAt = now.Add(-time.Second).Unix() },
			"excessive": func(c *nativeVideoAuthClaims) { c.ExpiresAt = now.Add(17 * time.Minute).Unix() },
		} {
			t.Run(name, func(t *testing.T) {
				changed := claims
				mutate(&changed)
				token := signNativeVideoTestToken(t, changed, secret, "HS256")
				if _, ok := parseNativeVideoAccessToken(token, secret, now); ok {
					t.Fatal("invalid token lifetime was accepted")
				}
			})
		}
	})
	t.Run("rejects invalid identity claims and short secrets", func(t *testing.T) {
		changed := claims
		changed.Subject = "not-a-uuid"
		token := signNativeVideoTestToken(t, changed, secret, "HS256")
		if _, ok := parseNativeVideoAccessToken(token, secret, now); ok {
			t.Fatal("invalid subject was accepted")
		}
		if _, ok := parseNativeVideoAccessToken(token, []byte("short"), now); ok {
			t.Fatal("short signing secret was accepted")
		}
	})
}

func TestPlaybackTokenRoundTrip(t *testing.T) {
	s := &server{signKey: []byte("test-secret")}
	token := s.sign("video-1", time.Now().Add(time.Minute))
	if !s.validToken("video-1", token) {
		t.Fatal("expected valid token")
	}
	if s.validToken("video-2", token) {
		t.Fatal("token crossed video boundary")
	}
	if s.validToken("video-1", token+".x") {
		t.Fatal("malformed token accepted")
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff header")
	}
}

func TestQuotaAllows(t *testing.T) {
	tests := []struct {
		name                             string
		used, reserved, requested, limit int64
		want                             bool
	}{
		{"within", 5, 2, 3, 10, true},
		{"exact", 5, 2, 3, 10, true},
		{"over by one", 5, 3, 3, 10, false},
		{"request exceeds limit", 0, 0, 11, 10, false},
		{"negative usage", -1, 0, 1, 10, false},
		{"negative reservation", 0, -1, 1, 10, false},
		{"zero request", 0, 0, 0, 10, false},
		{"zero quota", 0, 0, 1, 0, false},
		{"overflow-safe", 9223372036854775807, 1, 1, 9223372036854775807, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quotaAllows(tt.used, tt.reserved, tt.requested, tt.limit); got != tt.want {
				t.Fatalf("quotaAllows(%d,%d,%d,%d)=%v want %v", tt.used, tt.reserved, tt.requested, tt.limit, got, tt.want)
			}
		})
	}
}

func TestUploadLimitEnvironment(t *testing.T) {
	t.Setenv("VIDEO_ACCOUNT_QUOTA_BYTES", "1024")
	got, err := positiveLimitEnv("VIDEO_ACCOUNT_QUOTA_BYTES", 2048)
	if err != nil || got != 1024 {
		t.Fatalf("positive limit = %d, %v", got, err)
	}
	t.Setenv("VIDEO_ACCOUNT_QUOTA_BYTES", "0")
	if _, err := positiveLimitEnv("VIDEO_ACCOUNT_QUOTA_BYTES", 2048); err == nil {
		t.Fatal("zero account quota should be rejected")
	}
	t.Setenv("VIDEO_MIN_FREE_BYTES", "-1")
	if _, err := nonnegativeLimitEnv("VIDEO_MIN_FREE_BYTES", 2048); err == nil {
		t.Fatal("negative disk reserve should be rejected")
	}
}


func TestCreatorVideoReadsRequireNativeAuthentication(t *testing.T) {
	s := &server{}
	for _, tc := range []struct {
		name string
		path string
		handler http.HandlerFunc
	}{
		{
			name: "owned channels",
			path: "/v1/channels",
			handler: s.channels,
		},
		{
			name: "owned video processing status",
			path: "/v1/videos/video-1/status",
			handler: func(w http.ResponseWriter, r *http.Request) { s.videoStatus(w, r, "video-1") },
		},
		{
			name: "video comments require native authentication",
			path: "/v1/videos/video-1/comments",
			handler: func(w http.ResponseWriter, r *http.Request) { s.comments(w, r, "video-1") },
		},
		{
			name: "video likes require native authentication",
			path: "/v1/videos/video-1/like",
			handler: func(w http.ResponseWriter, r *http.Request) { r.Method = http.MethodPost; s.like(w, r, "video-1") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, tc.path, nil)
			tc.handler(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated request status = %d, want %d", recorder.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestVideoRouteDispatchesOwnerStatusEndpoint(t *testing.T) {
	s := &server{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/videos/video-1/status", nil)
	s.videoRoute(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status route response = %d, want authentication denial before data access", recorder.Code)
	}
}

func TestVideoPlaybackSessionValidation(t *testing.T) {
	for _, session := range []string{"abcdefgh", "session-123", "A1_b2-C3"} {
		if !validVideoPlaybackSession(session) {
			t.Fatalf("valid playback session rejected: %q", session)
		}
	}
	for _, session := range []string{"", "short", "has space", "bad/slash", "bad\\\\slash", "x?token=y", string(make([]byte, 129))} {
		if validVideoPlaybackSession(session) {
			t.Fatalf("invalid playback session accepted: %q", session)
		}
	}
}

func TestVideoHLSFilenameAllowlist(t *testing.T) {
	for _, file := range []string{"master.m3u8", "segment-001.ts", "variant_2.m3u8"} {
		if !validVideoHLSFilename(file) {
			t.Fatalf("valid HLS filename rejected: %q", file)
		}
	}
	for _, file := range []string{"", "..", "master..m3u8", "bad name.ts", "a/b.ts", "x\\\\secret.ts", "segment?token=x.ts"} {
		if validVideoHLSFilename(file) {
			t.Fatalf("unsafe HLS filename accepted: %q", file)
		}
	}
}

func TestVideoHLSRejectsInvalidMethodAndTokenQueryBeforeStorageAccess(t *testing.T) {
	s := &server{}
	tests := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"method", http.MethodPost, "/v1/videos/video-1/hls/master.m3u8?token=abc", http.StatusMethodNotAllowed},
		{"missing token", http.MethodGet, "/v1/videos/video-1/hls/master.m3u8", http.StatusBadRequest},
		{"duplicate token", http.MethodGet, "/v1/videos/video-1/hls/master.m3u8?token=a&token=b", http.StatusBadRequest},
		{"extra query", http.MethodGet, "/v1/videos/video-1/hls/master.m3u8?token=a&debug=1", http.StatusBadRequest},
		{"unsafe filename", http.MethodGet, "/v1/videos/video-1/hls/../secret.ts?token=a", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, nil)
			s.videoRoute(recorder, request)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.want, recorder.Body.String())
			}
		})
	}
}

func TestVideoManifestRejectsNonGetBeforeDatabaseAccess(t *testing.T) {
	s := &server{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/videos/video-1/manifest", nil)
	s.videoRoute(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("manifest method status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}
