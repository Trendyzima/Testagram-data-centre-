package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestLiveProviderRoundTrip is intentionally opt-in. It writes only unique
// ci-smoke objects and deletes them after verifying each provider independently.
func TestLiveProviderRoundTrip(t *testing.T) {
	if os.Getenv("TESTAGRAM_LIVE_MEDIA_SMOKE") != "true" {
		t.Skip("set TESTAGRAM_LIVE_MEDIA_SMOKE=true only in the manual provider-smoke workflow")
	}
	readStore := func(prefix, name string) s3Store {
		store := s3Store{
			name: name,
			endpoint: os.Getenv(prefix + "_ENDPOINT"),
			region: os.Getenv(prefix + "_REGION"),
			bucket: os.Getenv(prefix + "_BUCKET"),
			access: os.Getenv(prefix + "_ACCESS_KEY_ID"),
			secret: os.Getenv(prefix + "_SECRET_ACCESS_KEY"),
		}
		if err := validateStore(store); err != nil {
			t.Fatalf("%s smoke credentials/configuration invalid: %v", name, err)
		}
		return store
	}
	stores := []s3Store{
		readStore("MEDIA_SMOKE_B2", "backblaze_b2"),
		readStore("MEDIA_SMOKE_R2", "cloudflare_r2"),
	}
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	s := &server{cfg: config{primary: stores[0], replica: stores[1]}, http: client}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	payload := []byte("Testagram live media provider smoke " + time.Now().UTC().Format(time.RFC3339Nano))
	for _, store := range stores {
		store := store
		t.Run(store.name, func(t *testing.T) {
			id, err := randomID()
			if err != nil {
				t.Fatal(err)
			}
			key := "ci-smoke/" + id
			defer func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cleanupCancel()
				resp, cleanupErr := s.signedRequest(cleanupCtx, store, http.MethodDelete, key, nil, "")
				if cleanupErr != nil {
					t.Errorf("cleanup request failed: %v", cleanupErr)
					return
				}
				io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
					t.Errorf("cleanup returned HTTP %d", resp.StatusCode)
				}
			}()

			body := &lengthReader{Reader: bytes.NewReader(payload), n: int64(len(payload))}
			put, err := s.signedRequest(ctx, store, http.MethodPut, key, body, "application/octet-stream")
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, io.LimitReader(put.Body, 4096))
			put.Body.Close()
			if put.StatusCode < 200 || put.StatusCode >= 300 {
				t.Fatalf("PUT returned HTTP %d", put.StatusCode)
			}

			get, err := s.signedRequest(ctx, store, http.MethodGet, key, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if get.StatusCode != http.StatusOK {
				get.Body.Close()
				t.Fatalf("GET returned HTTP %d", get.StatusCode)
			}
			if !digestMatches(get.Body, int64(len(payload)), hashHex(payload)) {
				get.Body.Close()
				t.Fatal("provider GET read-back did not match the exact payload size and SHA-256")
			}
			get.Body.Close()

			head, err := s.signedRequest(ctx, store, http.MethodHead, key, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			head.Body.Close()
			if head.StatusCode != http.StatusOK || head.ContentLength != int64(len(payload)) {
				t.Fatalf("HEAD returned status=%d content-length=%d", head.StatusCode, head.ContentLength)
			}
		})
	}
}
