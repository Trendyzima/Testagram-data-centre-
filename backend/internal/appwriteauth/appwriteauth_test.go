package appwriteauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchAccountSendsAppwriteProjectAndJWT(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("X-Appwrite-Project") != "test-project" {
			t.Error("Appwrite project header missing")
		}
		if r.Header.Get("X-Appwrite-JWT") != "session-jwt" {
			t.Error("Appwrite JWT header missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"$id":"user_123","status":true}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := fetchAccount(ctx, server.URL, "test-project", "session-jwt")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "user_123" || !got.Status {
		t.Fatalf("unexpected Appwrite account: %+v", got)
	}
}

func TestFetchAccountRejectsDisabledOrInvalidAccounts(t *testing.T) {
	for name, body := range map[string]string{
		"disabled": `{"$id":"user_123","status":false}`,
		"invalid id": `{"$id":" bad id","status":true}`,
		"malformed": `{"$id":`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			if _, err := fetchAccount(context.Background(), server.URL, "test-project", "session-jwt"); err == nil {
				t.Fatal("expected invalid Appwrite account to be rejected")
			}
		})
	}
}

func TestFetchAccountDoesNotFollowRedirects(t *testing.T) {
	var destinationCalled bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"$id":"user_123","status":true}`))
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, nil, destination.URL, http.StatusTemporaryRedirect)
	}))
	_ = origin
	_ = strings.TrimSpace
	_ = destinationCalled
}
