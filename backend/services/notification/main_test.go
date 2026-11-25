package main

import (
	"net/http/httptest"
	"time"
	"testing"
)

func TestNotificationUserRejectsInvalidNativeToken(t *testing.T) {
	s := &service{authJWTSecret: []byte("testagram-native-auth-secret-at-least-32-bytes")}
	r := httptest.NewRequest("GET", "/v1/stream", nil)
	r.Header.Set("Authorization", "Bearer not-a-jwt")
	if userID, ok := s.user(r); ok || userID != "" {
		t.Fatalf("invalid native token authenticated as %q", userID)
	}
}

func TestNotificationUserRejectsMissingBearer(t *testing.T) {
	s := &service{authJWTSecret: []byte("testagram-native-auth-secret-at-least-32-bytes")}
	r := httptest.NewRequest("GET", "/v1/stream", nil)
	if userID, ok := s.user(r); ok || userID != "" {
		t.Fatalf("missing bearer authenticated as %q", userID)
	}
}

func TestLedgerNotificationCursorRoundTrip(t *testing.T) {
	want := ledgerNotificationCursor{
		CreatedAt: time.Date(2026, 11, 25, 10, 30, 0, 123456000, time.UTC),
		ID: "12345678-1234-4234-8234-123456789abc",
	}
	got, err := decodeLedgerCursor(encodeLedgerCursor(want))
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || got.ID != want.ID {
		t.Fatalf("cursor round trip mismatch: got %#v want %#v", got, want)
	}
}

func TestLedgerNotificationCursorRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{
		"not-base64",
		"",
		"eyJjcmVhdGVkX2F0IjoiMjAyNi0xMS0yNVQxMDozMDowMFoiLCJpZCI6Im5vdC1hLXV1aWQifQ",
	} {
		if value == "" {
			continue
		}
		if _, err := decodeLedgerCursor(value); err == nil {
			t.Fatalf("expected malformed cursor to be rejected: %q", value)
		}
	}
}

func TestLedgerNotificationCursorAllowsEmptyInitialCursor(t *testing.T) {
	got, err := decodeLedgerCursor("")
	if err != nil {
		t.Fatalf("empty initial cursor rejected: %v", err)
	}
	if !got.CreatedAt.IsZero() || got.ID != "" {
		t.Fatalf("unexpected initial cursor: %#v", got)
	}
}
