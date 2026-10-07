package main

import (
  "net/http"
  "net/http/httptest"
  "testing"
  "time"
)

func TestPlaybackTokenRoundTrip(t *testing.T) {
  s:=&server{signKey:[]byte("test-secret")}
  token:=s.sign("video-1",time.Now().Add(time.Minute))
  if !s.validToken("video-1",token){t.Fatal("expected valid token")}
  if s.validToken("video-2",token){t.Fatal("token crossed video boundary")}
  if s.validToken("video-1",token+".x"){t.Fatal("malformed token accepted")}
}

func TestSecurityHeaders(t *testing.T) {
  h:=securityHeaders(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusNoContent)}))
  rr:=httptest.NewRecorder()
  h.ServeHTTP(rr,httptest.NewRequest(http.MethodGet,"/healthz",nil))
  if rr.Header().Get("X-Content-Type-Options")!="nosniff"{t.Fatal("missing nosniff header")}
}
