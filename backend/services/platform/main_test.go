package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestLogEmitsVersionAndRequestID(t *testing.T) {
	handler := requestLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "backend-trace-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Testagram-API-Version"); got != "1" { t.Fatalf("API version = %q", got) }
	if got := rec.Header().Get("X-Request-ID"); got != "backend-trace-123" { t.Fatalf("request ID = %q", got) }
}
