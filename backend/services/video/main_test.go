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

func TestQuotaAllows(t *testing.T) {
  tests:=[]struct{name string;used,reserved,requested,limit int64;want bool}{
    {"within",5,2,3,10,true},
    {"exact",5,2,3,10,true},
    {"over by one",5,3,3,10,false},
    {"request exceeds limit",0,0,11,10,false},
    {"negative usage",-1,0,1,10,false},
    {"negative reservation",0,-1,1,10,false},
    {"zero request",0,0,0,10,false},
    {"zero quota",0,0,1,0,false},
    {"overflow-safe",9223372036854775807,1,1,9223372036854775807,false},
  }
  for _,tt:=range tests{t.Run(tt.name,func(t *testing.T){if got:=quotaAllows(tt.used,tt.reserved,tt.requested,tt.limit);got!=tt.want{t.Fatalf("quotaAllows(%d,%d,%d,%d)=%v want %v",tt.used,tt.reserved,tt.requested,tt.limit,got,tt.want)}})}
}

func TestUploadLimitEnvironment(t *testing.T) {
  t.Setenv("VIDEO_ACCOUNT_QUOTA_BYTES","1024")
  got,err:=positiveLimitEnv("VIDEO_ACCOUNT_QUOTA_BYTES",2048)
  if err!=nil||got!=1024{t.Fatalf("positive limit = %d, %v",got,err)}
  t.Setenv("VIDEO_ACCOUNT_QUOTA_BYTES","0")
  if _,err:=positiveLimitEnv("VIDEO_ACCOUNT_QUOTA_BYTES",2048);err==nil{t.Fatal("zero account quota should be rejected")}
  t.Setenv("VIDEO_MIN_FREE_BYTES","-1")
  if _,err:=nonnegativeLimitEnv("VIDEO_MIN_FREE_BYTES",2048);err==nil{t.Fatal("negative disk reserve should be rejected")}
}
