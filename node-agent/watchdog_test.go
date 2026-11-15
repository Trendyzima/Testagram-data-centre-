package main

import (
	"errors"
	"strings"
	"testing"
)

func TestWatchdogTargetsAreAllowlisted(t *testing.T) {
	a:=&Agent{watchdogDataCentreCompose:"/opt/testagram/data-centre/backend/docker-compose.yml",watchdogBackendCompose:"/opt/testagram/backend/deploy/self-hosted/docker-compose.yml"}
	cases:=map[string]composeTarget{
		"algorithm":{file:a.watchdogDataCentreCompose,service:"algorithm"},
		"worker":{file:a.watchdogDataCentreCompose,service:"worker"},
		"notification":{file:a.watchdogDataCentreCompose,service:"notification"},
		"video-worker":{file:a.watchdogDataCentreCompose,service:"video-worker"},
		"media":{file:a.watchdogDataCentreCompose,profile:"media",service:"media"},
		"federation":{file:a.watchdogDataCentreCompose,service:"federation-api"},
		"cdn":{file:a.watchdogBackendCompose,service:"media-cache"},
	}
	for name,want:=range cases {
		got,err:=a.watchdogTargets(name)
		if err!=nil || len(got)!=1 || got[0]!=want { t.Fatalf("%s: got %#v, %v; want %#v",name,got,err,want) }
	}
	if _,err:=a.watchdogTargets("postgres");err==nil { t.Fatal("database must never be activated or controlled by user impulses") }
	if _,err:=a.watchdogTargets("caddy");err==nil { t.Fatal("public ingress must never be controlled by user impulses") }
	if _,err:=a.watchdogTargets("arbitrary-command");err==nil { t.Fatal("arbitrary services must be rejected") }
}

func TestWatchdogComposeArgumentsAreNonDestructive(t *testing.T) {
	a:=&Agent{}
	got:=a.composeArgs(composeTarget{file:"/opt/testagram/backend/docker-compose.yml",profile:"media",service:"media"},"up")
	joined:=strings.Join(got," ")
	for _,required:=range []string{"--profile media","up -d --no-deps --no-recreate --pull never media"} {
		if !strings.Contains(joined,required) { t.Fatalf("missing %q in %q",required,joined) }
	}
	for _,forbidden:=range []string{"down","rm ","pull","migrate","postgres"} {
		if strings.Contains(joined,forbidden) { t.Fatalf("unsafe argument %q in %q",forbidden,joined) }
	}
}

func TestProcessWatchdogImpulseRejectsInvalidAndUnknownServices(t *testing.T) {
	a:=&Agent{}
	if err:=a.processWatchdogImpulse(watchdogImpulse{});err==nil { t.Fatal("empty impulse should fail") }
	err:=a.processWatchdogImpulse(watchdogImpulse{ID:1,Services:[]string{"arbitrary"}})
	if err==nil || errors.Is(err,nil) { t.Fatal("unknown service should fail") }
}
