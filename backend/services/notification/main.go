package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type service struct {
	db         *pgxpool.Pool
	jwtKey     []byte
	internal   string
}

type notification struct {
	ID      string         `json:"id"`
	UserID  string         `json:"user_id"`
	Title   string         `json:"title"`
	Body    string         `json:"body"`
	Payload map[string]any `json:"payload"`
}

type notificationInput struct {
	UserID  string         `json:"user_id"`
	Title   string         `json:"title"`
	Body    string         `json:"body"`
	Payload map[string]any `json:"payload"`
}

type deviceInput struct {
	ID      string `json:"id"`
	Channel string `json:"channel"`
	Address string `json:"address"`
}

func main() {
	ctx := context.Background()
	dsn := mustEnv("DATABASE_URL")
	jwtSecret := mustEnv("NOTIFICATION_JWT_SECRET")
	internal := mustEnv("NOTIFICATION_INTERNAL_TOKEN")
	db, err := pgxpool.New(ctx, dsn)
	if err != nil { log.Fatal(err) }
	defer db.Close()
	s := &service{db: db, jwtKey: []byte(jwtSecret), internal: internal}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.ready)
	mux.HandleFunc("/v1/devices", s.devices)
	mux.HandleFunc("/v1/notifications", s.notifications)
	mux.HandleFunc("/v1/stream", s.stream)

	addr := env("NOTIFICATION_ADDR", ":8090")
	srv := &http.Server{
		Addr: addr, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 0, IdleTimeout: 120 * time.Second,
	}
	log.Printf("Testagram notification service listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}

func (s *service) health(w http.ResponseWriter, _ *http.Request) {
	jsonOut(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *service) ready(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(r.Context()); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	jsonOut(w, http.StatusOK, map[string]bool{"ready": true})
}

func (s *service) devices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed); return
	}
	user, ok := s.user(r)
	if !ok { http.Error(w, "unauthorized", http.StatusUnauthorized); return }
	var in deviceInput
	if err := decodeJSON(w, r, &in); err != nil || strings.TrimSpace(in.ID) == "" || strings.TrimSpace(in.Address) == "" {
		http.Error(w, "invalid device", http.StatusBadRequest); return
	}
	channel := strings.TrimSpace(in.Channel)
	if channel == "" { channel = "sse" }
	if len(in.ID) > 200 || len(in.Address) > 2000 || len(channel) > 40 {
		http.Error(w, "device fields too long", http.StatusBadRequest); return
	}
	_, err := s.db.Exec(r.Context(), `insert into public.testagram_notification_devices
		(id,user_id,channel,address,enabled,last_seen_at,updated_at)
		values($1,$2,$3,$4,true,now(),now())
		on conflict(id) do update set user_id=excluded.user_id,channel=excluded.channel,
		address=excluded.address,enabled=true,last_seen_at=now(),updated_at=now()`,
		in.ID, user, channel, in.Address)
	if err != nil { http.Error(w, "device registration failed", http.StatusInternalServerError); return }
	jsonOut(w, http.StatusCreated, map[string]bool{"registered": true})
}

func (s *service) notifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed); return
	}
	if bearer(r) != s.internal || s.internal == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized); return
	}
	var in notificationInput
	if err := decodeJSON(w, r, &in); err != nil || in.UserID == "" || strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Body) == "" {
		http.Error(w, "invalid notification", http.StatusBadRequest); return
	}
	if len(in.Title) > 200 || len(in.Body) > 4000 {
		http.Error(w, "notification too large", http.StatusBadRequest); return
	}
	if in.Payload == nil { in.Payload = map[string]any{} }
	var id string
	err := s.db.QueryRow(r.Context(), `insert into public.testagram_notifications
		(user_id,title,body,payload) values($1,$2,$3,$4) returning id`,
		in.UserID, strings.TrimSpace(in.Title), strings.TrimSpace(in.Body), in.Payload).Scan(&id)
	if err != nil { http.Error(w, "notification queue failed", http.StatusInternalServerError); return }
	jsonOut(w, http.StatusAccepted, map[string]string{"id": id, "status": "queued"})
}

func (s *service) stream(w http.ResponseWriter, r *http.Request) {
	user, ok := s.user(r)
	if !ok { http.Error(w, "unauthorized", http.StatusUnauthorized); return }
	flusher, ok := w.(http.Flusher)
	if !ok { http.Error(w, "streaming unsupported", http.StatusInternalServerError); return }
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.deliverOne(r.Context(), user, w, flusher); err != nil {
			if !errors.Is(err, context.Canceled) { log.Printf("notification stream: %v", err) }
			return
		}
		select {
		case <-r.Context().Done(): return
		case <-ticker.C:
		}
	}
}

func (s *service) deliverOne(ctx context.Context, user string, w http.ResponseWriter, flusher http.Flusher) error {
	tx, err := s.db.Begin(ctx)
	if err != nil { return err }
	defer tx.Rollback(ctx)
	var n notification
	err = tx.QueryRow(ctx, `select id,user_id,title,body,payload from public.testagram_notifications
		where user_id=$1 and status='queued' and available_at<=now()
		order by created_at for update skip locked limit 1`, user).
		Scan(&n.ID, &n.UserID, &n.Title, &n.Body, &n.Payload)
	if err != nil {
		return nil
	}
	if _, err = tx.Exec(ctx, `update public.testagram_notifications
		set status='delivered',delivered_at=now(),updated_at=now(),attempts=attempts+1 where id=$1`, n.ID); err != nil { return err }
	if err = tx.Commit(ctx); err != nil { return err }
	data, _ := json.Marshal(n)
	_, err = w.Write([]byte("event: notification\nid: " + n.ID + "\ndata: " + string(data) + "\n\n"))
	if err == nil { flusher.Flush() }
	return err
}

func (s *service) user(r *http.Request) (string, bool) {
	raw := bearer(r)
	if raw == "" { return "", false }
	token, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != "HS256" { return nil, errors.New("algorithm rejected") }
		return s.jwtKey, nil
	})
	if err != nil || !token.Valid { return "", false }
	sub, err := token.Claims.GetSubject()
	return sub, err == nil && sub != ""
}

func bearer(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("Authorization"))
	return strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(dst)
}

func jsonOut(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func env(k, d string) string { if v := os.Getenv(k); v != "" { return v }; return d }
func mustEnv(k string) string { v := os.Getenv(k); if v == "" { log.Fatalf("%s is required", k) }; return v }
