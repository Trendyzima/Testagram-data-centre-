package main

import (
	"net/http/httptest"
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
