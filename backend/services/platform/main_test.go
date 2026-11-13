package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONRequiresExactlyOneBoundedObject(t *testing.T) {
	t.Run("valid object", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"key":"feed.enabled","enabled":true,"public":false,"value":true}`))
		rec := httptest.NewRecorder()
		var input flagRequest
		if !decodeJSON(rec, req, &input) || input.Key != "feed.enabled" || !input.Enabled {
			t.Fatalf("decodeJSON rejected valid object: %#v", input)
		}
	})
	t.Run("trailing object", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"key":"feed.enabled","enabled":true,"public":false,"value":true}{"extra":true}`))
		rec := httptest.NewRecorder()
		var input flagRequest
		if decodeJSON(rec, req, &input) {
			t.Fatal("decodeJSON accepted a second trailing JSON value")
		}
	})
	t.Run("unknown field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"key":"feed.enabled","enabled":true,"public":false,"value":true,"unexpected":1}`))
		rec := httptest.NewRecorder()
		var input flagRequest
		if decodeJSON(rec, req, &input) {
			t.Fatal("decodeJSON accepted an unknown field")
		}
	})
	t.Run("oversized body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"key":"`+strings.Repeat("x", 1<<20)+`"}`))
		rec := httptest.NewRecorder()
		var input flagRequest
		if decodeJSON(rec, req, &input) {
			t.Fatal("decodeJSON accepted a body larger than 1 MiB")
		}
	})
}

func TestPlatformAuthorizationFailsClosed(t *testing.T) {
	server := &server{internal: "0123456789abcdef0123456789abcdef"}
	for _, tc := range []struct {
		name  string
		token string
		want  bool
	}{
		{name: "missing", want: false},
		{name: "wrong", token: "wrong-token", want: false},
		{name: "correct", token: "0123456789abcdef0123456789abcdef", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/config/set", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			if got := server.authorize(rec, req); got != tc.want {
				t.Fatalf("authorize() = %v, want %v", got, tc.want)
			}
		})
	}
}


func TestPlatformMethodGuards(t *testing.T) {
	cases := []struct {
		name string
		handler http.HandlerFunc
		method string
		path string
		allow string
	}{
		{"config set", (&server{}).configSet, http.MethodGet, "/v1/config/set", http.MethodPost},
		{"events", (&server{}).events, http.MethodGet, "/v1/events", http.MethodPost},
		{"audit", (&server{}).audit, http.MethodGet, "/v1/audit", http.MethodPost},
		{"public config", (&server{}).publicConfig, http.MethodPost, "/v1/config/key", http.MethodGet},
		{"health", (&server{}).health, http.MethodPost, "/healthz", http.MethodGet},
		{"ready", (&server{}).readyz, http.MethodPost, "/readyz", http.MethodGet},
		{"metrics", (&server{}).metrics, http.MethodPost, "/metrics", http.MethodGet},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			tc.handler(rec, req)
			if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != tc.allow {
				t.Fatalf("got status=%d Allow=%q", rec.Code, rec.Header().Get("Allow"))
			}
		})
	}
}

func TestJSONObjectValidation(t *testing.T) {
	for _, tc := range []struct { raw string; want bool }{
		{ `{"ok":true}`, true },
		{ `[]`, false },
		{ `null`, false },
		{ `"text"`, false },
		{ "", false },
		{ `{`, false },
	} {
		if got := validJSONObject([]byte(tc.raw)); got != tc.want {
			t.Errorf("validJSONObject(%q)=%v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestInternalTokenStrength(t *testing.T) {
	for _, tc := range []struct { token string; want bool }{
		{"0123456789abcdef0123456789abcdef", true},
		{"short", false},
		{" 0123456789abcdef0123456789abcdef", false},
		{"0123456789abcdef0123456789abc def", false},
	} {
		if got := validInternalToken(tc.token); got != tc.want {
			t.Errorf("validInternalToken(%q)=%v, want %v", tc.token, got, tc.want)
		}
	}
}

func TestPlatformKeyValidation(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"feed.enabled", true},
		{"notification:push", true},
		{"", false},
		{"bad key", false},
		{strings.Repeat("x", 161), false},
	} {
		if got := validKey(tc.value); got != tc.want {
			t.Errorf("validKey(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
