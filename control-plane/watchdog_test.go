package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWatchdogServiceMapping(t *testing.T) {
	cases:=map[string][]string{
		"login":{"algorithm"},
		"feed_open":{"algorithm"},
		"post_publish":{"data-centre"},
		"media_upload":{"cdn","data-centre"},
		"media_view":{"cdn","data-centre"},
		"federation_interaction":{"federation"},
	}
	for event,want:=range cases {
		got,ok:=servicesForImpulse(event)
		if !ok || strings.Join(got,",")!=strings.Join(want,",") { t.Fatalf("%s: got %v (%v), want %v",event,got,ok,want) }
	}
	if _,ok:=servicesForImpulse("arbitrary"); ok { t.Fatal("unsupported event must not be accepted") }
}

func TestWatchdogIngressUsesDedicatedCredential(t *testing.T) {
	s:=&Server{internal:"other-internal-token"}
	req:=httptest.NewRequest(http.MethodPost,"/v1/watchdog/impulses",strings.NewReader("{}"))
	rec:=httptest.NewRecorder()
	if s.watchdogOnly(rec,req) || rec.Code!=http.StatusUnauthorized { t.Fatalf("watchdog ingress must reject missing dedicated credential: %d",rec.Code) }
}

func TestWatchdogNodeRoutesRejectMissingNodeToken(t *testing.T) {
	s:=&Server{}
	for _,handler:=range []http.HandlerFunc{s.watchdogNodeClaimImpulses,s.watchdogNodeAckImpulse} {
		req:=httptest.NewRequest(http.MethodPost,"/v1/nodes/watchdog/claim",strings.NewReader("{}"))
		rec:=httptest.NewRecorder(); handler(rec,req)
		if rec.Code!=http.StatusUnauthorized { t.Fatalf("wanted 401, got %d",rec.Code) }
	}
}
