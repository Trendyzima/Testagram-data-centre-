package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Device struct {
	ID string `json:"id"`
	UserID string `json:"userId"`
	Channel string `json:"channel"`
	Address string `json:"address"`
	Enabled bool `json:"enabled"`
}

type Broker struct {
	mu sync.RWMutex
	devices map[string]Device
}

func main() {
	b := &Broker{devices: map[string]Device{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/v1/devices", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { http.Error(w, "method not allowed", 405); return }
		var d Device
		if err := json.NewDecoder(http.MaxBytesReader(w,r.Body,32<<10)).Decode(&d); err != nil || d.ID=="" || d.UserID=="" || d.Address=="" {
			http.Error(w,"invalid device",400); return
		}
		if d.Channel=="" { d.Channel="websocket" }
		d.Enabled=true
		b.mu.Lock(); b.devices[d.ID]=d; b.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("/v1/devices/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete { http.Error(w,"method not allowed",405); return }
		id:=strings.TrimPrefix(r.URL.Path,"/v1/devices/")
		b.mu.Lock(); delete(b.devices,id); b.mu.Unlock(); w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1/notifications", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { http.Error(w,"method not allowed",405); return }
		var n map[string]any
		if err:=json.NewDecoder(http.MaxBytesReader(w,r.Body,64<<10)).Decode(&n); err!=nil { http.Error(w,"invalid notification",400); return }
		n["queuedAt"]=time.Now().UTC()
		b.mu.RLock(); count:=len(b.devices); b.mu.RUnlock()
		log.Printf("queued notification for %d registered devices",count)
		_ = json.NewEncoder(w).Encode(map[string]any{"queued":true,"deviceCount":count})
	})
	addr:=os.Getenv("NOTIFICATION_ADDR"); if addr=="" { addr=":8090" }
	log.Fatal(http.ListenAndServe(addr,mux))
}
