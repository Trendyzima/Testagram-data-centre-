package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidControlPlaneSecret(t *testing.T) {
	if !validControlPlaneSecret(strings.Repeat("x", 32)) {
		t.Fatal("expected 32-character secret to be accepted")
	}
	for _, value := range []string{"", strings.Repeat("x", 31), strings.Repeat("x", 32) + "\n", " " + strings.Repeat("x", 32)} {
		if validControlPlaneSecret(value) {
			t.Fatalf("invalid control-plane secret accepted: %q", value)
		}
	}
}

func TestHealth(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	s.health(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
}

func TestValidImageDigest(t *testing.T) {
	if !validImageDigest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatal("expected valid digest")
	}
	if validImageDigest("sha256:bad") {
		t.Fatal("expected invalid digest")
	}
	if validImageDigest("sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") {
		t.Fatal("expected lowercase-only digest")
	}
}

func TestNormalizeWorkloadDefaultsNilJSONCollections(t *testing.T) {
	work := Workload{}
	normalizeWorkload(&work)
	if work.Command == nil {
		t.Fatal("command must be an empty JSON array, not nil")
	}
	if work.Env == nil {
		t.Fatal("env must be an empty JSON object, not nil")
	}
	if len(work.Command) != 0 || len(work.Env) != 0 {
		t.Fatalf("expected empty collections, got command=%v env=%v", work.Command, work.Env)
	}
}
