package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeStrictJSONRejectsUnknownAndTrailingFields(t *testing.T) {
	type requestBody struct {
		Name string `json:"name"`
	}
	for _, input := range []string{
		`{"name":"valid"}`,
		`{"name":"valid","unexpected":true}`,
		`{"name":"valid"} {"name":"second"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(input))
		recorder := httptest.NewRecorder()
		var body requestBody
		err := decodeStrictJSON(recorder, req, &body)
		if input == `{"name":"valid"}` && err != nil {
			t.Fatalf("valid JSON rejected: %v", err)
		}
		if input != `{"name":"valid"}` && err == nil {
			t.Fatalf("invalid JSON accepted: %s", input)
		}
	}
}

func TestValidMediaRequestBindsOwnerToObjectKey(t *testing.T) {
	key := "users/user-123/0123456789abcdef0123456789abcdef"
	if !validMediaRequest("user-123", "media-public", key) {
		t.Fatal("valid owner-scoped media key rejected")
	}
	if validMediaRequest("other-user", "media-public", key) {
		t.Fatal("media key accepted for a different owner")
	}
	if validMediaRequest("user-123", "media-private", "users/user-123/../secret") {
		t.Fatal("path traversal media key accepted")
	}
	if validMediaRequest("user-123", "unknown", key) {
		t.Fatal("unknown media namespace accepted")
	}
}

func TestMediaInternalTokenIsIndependentAndConstantTimeChecked(t *testing.T) {
	const token = "data-centre-private-media-token-0123456789"
	t.Setenv("FRESH_DATA_CENTRE_INTERNAL_TOKEN", token)
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/media/verify", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	if !server.mediaInternalOnly(recorder, request) {
		t.Fatalf("valid private media token rejected: %d %s", recorder.Code, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/internal/v1/media/verify", nil)
	request.Header.Set("Authorization", "Bearer wrong-token")
	recorder = httptest.NewRecorder()
	if server.mediaInternalOnly(recorder, request) || recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid private media token accepted: %d", recorder.Code)
	}
}

func TestMediaRoutesFailClosedWhenStorageIsDisabled(t *testing.T) {
	server := &Server{}
	recorder := httptest.NewRecorder()
	if !server.mediaUnavailable(recorder) || recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled media control plane did not fail closed: %d", recorder.Code)
	}
}
