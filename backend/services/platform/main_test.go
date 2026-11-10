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
	server := &server{internal: "private-token"}
	for _, tc := range []struct {
		name  string
		token string
		want  bool
	}{
		{name: "missing", want: false},
		{name: "wrong", token: "wrong-token", want: false},
		{name: "correct", token: "private-token", want: true},
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
