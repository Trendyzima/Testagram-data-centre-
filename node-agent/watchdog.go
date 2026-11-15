package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type watchdogImpulse struct {
	ID       int64    `json:"id"`
	EventType string  `json:"eventType"`
	Services []string `json:"services"`
	Attempts int      `json:"attempts"`
}

type watchdogClaimResponse struct {
	Impulses []watchdogImpulse `json:"impulses"`
}

type watchdogAckRequest struct {
	ID      int64  `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type composeTarget struct {
	file    string
	profile string
	service string
}

// Targets are fixed in code. An impulse cannot provide a Compose filename,
// profile, service, image, command, environment variable or host path.
func (a *Agent) watchdogTargets(service string) ([]composeTarget, error) {
	switch service {
	case "algorithm":
		return []composeTarget{{file:a.watchdogDataCentreCompose, service:"algorithm"}}, nil
	case "worker":
		return []composeTarget{{file:a.watchdogDataCentreCompose, service:"worker"}}, nil
	case "notification":
		return []composeTarget{{file:a.watchdogDataCentreCompose, service:"notification"}}, nil
	case "video-worker":
		return []composeTarget{{file:a.watchdogDataCentreCompose, service:"video-worker"}}, nil
	case "media":
		return []composeTarget{{file:a.watchdogDataCentreCompose, profile:"media", service:"media"}}, nil
	case "federation":
		return []composeTarget{{file:a.watchdogDataCentreCompose, service:"federation-api"}}, nil
	case "cdn":
		return []composeTarget{{file:a.watchdogBackendCompose, service:"media-cache"}}, nil
	default:
		return nil, fmt.Errorf("watchdog service %q is not allowlisted", service)
	}
}

func (a *Agent) watchdogPost(path string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil { return err }
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(a.watchdogControl,"/")+path, bytes.NewReader(raw))
	if err != nil { return err }
	req.Header.Set("Authorization","Bearer "+a.watchdogToken)
	req.Header.Set("Content-Type","application/json")
	client:=&http.Client{Timeout:12*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}
	resp,err:=client.Do(req)
	if err!=nil{return err}
	defer resp.Body.Close()
	if resp.StatusCode<200 || resp.StatusCode>=300 {
		data,_:=io.ReadAll(io.LimitReader(resp.Body,2048))
		return fmt.Errorf("watchdog control plane returned %d: %s",resp.StatusCode,strings.TrimSpace(string(data)))
	}
	if out!=nil { return json.NewDecoder(io.LimitReader(resp.Body,1<<20)).Decode(out) }
	return nil
}

func (a *Agent) claimWatchdogImpulses() ([]watchdogImpulse,error) {
	var out watchdogClaimResponse
	if err:=a.watchdogPost("/v1/watchdog/impulses/claim?limit=10",map[string]any{},&out);err!=nil{return nil,err}
	return out.Impulses,nil
}

func (a *Agent) acknowledgeWatchdogImpulse(id int64, success bool, cause error) error {
	in:=watchdogAckRequest{ID:id,Success:success}
	if cause!=nil { in.Error=cause.Error(); if len(in.Error)>500 { in.Error=in.Error[:500] } }
	return a.watchdogPost("/v1/watchdog/impulses/ack",in,nil)
}

func (a *Agent) activateWatchdogService(service string) error {
	targets,err:=a.watchdogTargets(service)
	if err!=nil{return err}
	for _,target:=range targets {
		if !filepath.IsAbs(target.file) { return fmt.Errorf("watchdog Compose file for %s must be an absolute path",service) }
		if info,statErr:=os.Stat(target.file);statErr!=nil || !info.Mode().IsRegular() {
			return fmt.Errorf("watchdog Compose file for %s is unavailable",service)
		}
		if err:=a.composeEnsureRunning(target);err!=nil{return fmt.Errorf("activate %s: %w",target.service,err)}
	}
	return nil
}

func (a *Agent) composeArgs(target composeTarget, action string) []string {
	args:=[]string{"compose","--project-directory",filepath.Dir(target.file),"-f",target.file}
	if target.profile!="" { args=append(args,"--profile",target.profile) }
	switch action {
	case "up":
		args=append(args,"up","-d","--no-deps","--no-recreate","--pull","never",target.service)
	case "ps":
		args=append(args,"ps","-q",target.service)
	}
	return args
}

func (a *Agent) composeEnsureRunning(target composeTarget) error {
	docker,err:=exec.LookPath("docker")
	if err!=nil{return errors.New("Docker CLI is required for watchdog service activation")}
	ctx,cancel:=context.WithTimeout(context.Background(),2*time.Minute)
	defer cancel()
	check:=exec.CommandContext(ctx,docker,"compose","version")
	if output,err:=check.CombinedOutput();err!=nil{return fmt.Errorf("Docker Compose plugin unavailable: %s",strings.TrimSpace(string(output)))}

	// Never pull images, run dependency migrations, or recreate a live service
	// in response to user traffic. Dependencies and databases are managed by
	// their own deployment lifecycle.
	up:=exec.CommandContext(ctx,docker,a.composeArgs(target,"up")...)
	if output,err:=up.CombinedOutput();err!=nil{return fmt.Errorf("Compose up failed: %s: %w",strings.TrimSpace(string(output)),err)}

	deadline:=time.Now().Add(75*time.Second)
	for time.Now().Before(deadline) {
		ps:=exec.CommandContext(ctx,docker,a.composeArgs(target,"ps")...)
		output,psErr:=ps.Output()
		id:=strings.TrimSpace(string(output))
		if psErr==nil && id!="" {
			inspect:=exec.CommandContext(ctx,docker,"inspect","--format={{.State.Status}}:{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}",id)
			stateOutput,inspectErr:=inspect.Output()
			if inspectErr==nil {
				state:=strings.TrimSpace(string(stateOutput))
				if strings.HasPrefix(state,"running:healthy") || state=="running:none" { return nil }
				if strings.HasPrefix(state,"running:unhealthy") { return fmt.Errorf("service %s failed its container health check",target.service) }
			}
		}
		time.Sleep(3*time.Second)
	}
	return fmt.Errorf("service %s did not become healthy within 75 seconds",target.service)
}

func (a *Agent) processWatchdogImpulse(impulse watchdogImpulse) error {
	if impulse.ID<1 || len(impulse.Services)==0 || len(impulse.Services)>5 {
		return errors.New("control plane returned an invalid watchdog impulse")
	}
	seen:=map[string]bool{}
	for _,service:=range impulse.Services {
		if seen[service] { continue }
		seen[service]=true
		if err:=a.activateWatchdogService(service);err!=nil{return err}
	}
	return nil
}

func (a *Agent) pollWatchdog() error {
	impulses,err:=a.claimWatchdogImpulses()
	if err!=nil{return err}
	for _,impulse:=range impulses {
		activationErr:=a.processWatchdogImpulse(impulse)
		if ackErr:=a.acknowledgeWatchdogImpulse(impulse.ID,activationErr==nil,activationErr);ackErr!=nil {
			return fmt.Errorf("acknowledge impulse %d: %w",impulse.ID,ackErr)
		}
		if activationErr!=nil { return fmt.Errorf("impulse %d activation: %w",impulse.ID,activationErr) }
	}
	return nil
}
