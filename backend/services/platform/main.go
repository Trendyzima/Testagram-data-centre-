package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type server struct {
	db            *pgxpool.Pool
	internal      string
	ready         atomic.Bool
	configWrites  atomic.Uint64
	eventsStored  atomic.Uint64
	auditWrites   atomic.Uint64
}

type flagRequest struct {
	Key     string          `json:"key"`
	Enabled bool            `json:"enabled"`
	Public  bool            `json:"public"`
	Value   json.RawMessage `json:"value"`
}
type eventRequest struct {
	EventName   string          `json:"event_name"`
	UserID      *string         `json:"user_id"`
	SessionHash string          `json:"session_hash"`
	Properties  json.RawMessage `json:"properties"`
}
type auditRequest struct {
	ActorID      *string         `json:"actor_id"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Metadata     json.RawMessage `json:"metadata"`
}

func main() {
	ctx := context.Background()
	dbURL := required("DATABASE_URL")
	internal := required("PLATFORM_INTERNAL_TOKEN")
	if !validInternalToken(internal) {
		log.Fatal("PLATFORM_INTERNAL_TOKEN must contain at least 32 non-whitespace characters")
	}
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(ctx); err != nil {
		log.Fatalf("platform PostgreSQL is unavailable: %v", err)
	}
	s := &server{db: db, internal: internal}
	s.ready.Store(true)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.readyz)
	mux.HandleFunc("/metrics", s.metrics)
	mux.HandleFunc("/v1/config/set", s.configSet)
	mux.HandleFunc("/v1/events", s.events)
	mux.HandleFunc("/v1/audit", s.audit)
	mux.HandleFunc("/v1/config/", s.publicConfig)

	addr := env("PLATFORM_ADDR", ":8095")
	log.Printf("testagram platform listening on %s", addr)
	log.Fatal((&http.Server{Addr: addr, Handler: requestLog(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}).ListenAndServe())
}
func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) { return }
	w.WriteHeader(200)
	_, _ = w.Write([]byte("ok\n"))
}
func (s *server) readyz(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) { return }
	if !s.ready.Load() {
		http.Error(w, "not ready", 503)
		return
	}
	if err := s.db.Ping(r.Context()); err != nil {
		http.Error(w, "database unavailable", 503)
		return
	}
	_, _ = w.Write([]byte("ready\n"))
}
func (s *server) metrics(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) { return }
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "testagram_platform_config_writes_total %d\n", s.configWrites.Load())
	fmt.Fprintf(w, "testagram_platform_events_stored_total %d\n", s.eventsStored.Load())
	fmt.Fprintf(w, "testagram_platform_audit_writes_total %d\n", s.auditWrites.Load())
}
func (s *server) authorize(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") ||
		!validInternalToken(s.internal) ||
		subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.internal)) != 1 {
		http.Error(w, "unauthorized", 401)
		return false
	}
	return true
}
func (s *server) configSet(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) { return }
	if !s.authorize(w, r) {
		return
	}
	var in flagRequest
	if !decodeJSON(w, r, &in) || !validKey(in.Key) || len(in.Value) == 0 || !json.Valid(in.Value) {
		http.Error(w, "invalid request", 400)
		return
	}
	_, err := s.db.Exec(r.Context(), `insert into testagram_platform.feature_flags(key,enabled,public,value,version,updated_at) values($1,$2,$3,$4::jsonb,1,now()) on conflict(key) do update set enabled=excluded.enabled,public=excluded.public,value=excluded.value,version=testagram_platform.feature_flags.version+1,updated_at=now()`, in.Key, in.Enabled, in.Public, string(in.Value))
	if err != nil {
		http.Error(w, "database unavailable", 503)
		return
	}
	s.configWrites.Add(1)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) { return }
	if !s.authorize(w, r) {
		return
	}
	var in eventRequest
	if !decodeJSON(w, r, &in) || len(in.EventName) < 1 || len(in.EventName) > 160 || !validJSONObject(in.Properties) {
		http.Error(w, "invalid request", 400)
		return
	}
	var userID any
	if in.UserID != nil {
		userID = *in.UserID
	}
	_, err := s.db.Exec(r.Context(), `insert into testagram_platform.events(event_name,user_id,session_hash,properties) values($1,$2,$3,$4::jsonb)`, in.EventName, userID, in.SessionHash, string(in.Properties))
	if err != nil {
		http.Error(w, "database unavailable", 503)
		return
	}
	s.eventsStored.Add(1)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) audit(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) { return }
	if !s.authorize(w, r) {
		return
	}
	var in auditRequest
	if !decodeJSON(w, r, &in) || len(in.Action) < 1 || len(in.Action) > 160 || len(in.ResourceType) < 1 || len(in.ResourceType) > 160 || !validJSONObject(in.Metadata) || strings.TrimSpace(in.ResourceID) == "" {
		http.Error(w, "invalid request", 400)
		return
	}
	var actorID any
	if in.ActorID != nil {
		actorID = *in.ActorID
	}
	_, err := s.db.Exec(r.Context(), `insert into testagram_platform.audit_events(actor_id,action,resource_type,resource_id,metadata) values($1,$2,$3,$4,$5::jsonb)`, actorID, in.Action, in.ResourceType, in.ResourceID, string(in.Metadata))
	if err != nil {
		http.Error(w, "database unavailable", 503)
		return
	}
	s.auditWrites.Add(1)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) publicConfig(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) { return }
	key := strings.TrimPrefix(r.URL.Path, "/v1/config/")
	if !validKey(key) {
		http.Error(w, "invalid key", 400)
		return
	}
	var value []byte
	err := s.db.QueryRow(r.Context(), `select value from testagram_platform.feature_flags where key=$1 and public=true and enabled=true`, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "not found", 404)
		return
	}
	if err != nil {
		http.Error(w, "database unavailable", 503)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30, stale-while-revalidate=60")
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(value)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return false
	}
	var trailing any
	return d.Decode(&trailing) == io.EOF
}
func validJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 || !json.Valid(raw) { return false }
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil { return false }
	return true
}

func validInternalToken(token string) bool {
	return len(token) >= 32 && strings.TrimSpace(token) == token && !strings.ContainsAny(token, " \t\r\n")
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method { return true }
	w.Header().Set("Allow", method)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}
func validKey(s string) bool {
	if len(s) < 1 || len(s) > 160 {
		return false
	}
	for _, c := range s {
		if !(c == '-' || c == '_' || c == '.' || c == ':' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}
func required(k string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		log.Fatalf("%s is required", k)
	}
	return v
}
func env(k, d string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return d
}
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}
