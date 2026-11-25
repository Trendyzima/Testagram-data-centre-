package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Trendyzima/Testagram-data-centre-/backend/internal/nativeauth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type service struct {
	db             *pgxpool.Pool
	authJWTSecret  []byte
	internal       string
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
	internal := mustEnv("NOTIFICATION_INTERNAL_TOKEN")
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	authSecret := mustEnv("TESTAGRAM_AUTH_JWT_SECRET")
	if len(authSecret) < 32 {
		log.Fatal("TESTAGRAM_AUTH_JWT_SECRET must contain at least 32 characters")
	}
	s := &service{db: db, authJWTSecret: []byte(authSecret), internal: internal}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.ready)
	mux.HandleFunc("/v1/devices", s.devices)
	mux.HandleFunc("/v1/notifications", s.notifications)
	mux.HandleFunc("/v1/stream", s.stream)
	mux.HandleFunc("/v2/stream", s.ledgerStream)

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
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.user(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var in deviceInput
	if err := decodeJSON(w, r, &in); err != nil || strings.TrimSpace(in.ID) == "" || strings.TrimSpace(in.Address) == "" {
		http.Error(w, "invalid device", http.StatusBadRequest)
		return
	}
	channel := strings.TrimSpace(in.Channel)
	if channel == "" {
		channel = "sse"
	}
	if len(in.ID) > 200 || len(in.Address) > 2000 || len(channel) > 40 {
		http.Error(w, "device fields too long", http.StatusBadRequest)
		return
	}
	_, err := s.db.Exec(r.Context(), `insert into public.testagram_notification_devices
		(id,user_id,channel,address,enabled,last_seen_at,updated_at)
		values($1,$2,$3,$4,true,now(),now())
		on conflict(id) do update set user_id=excluded.user_id,channel=excluded.channel,
		address=excluded.address,enabled=true,last_seen_at=now(),updated_at=now()`,
		in.ID, user, channel, in.Address)
	if err != nil {
		http.Error(w, "device registration failed", http.StatusInternalServerError)
		return
	}
	jsonOut(w, http.StatusCreated, map[string]bool{"registered": true})
}

func (s *service) notifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if bearer(r) != s.internal || s.internal == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var in notificationInput
	if err := decodeJSON(w, r, &in); err != nil || in.UserID == "" || strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Body) == "" {
		http.Error(w, "invalid notification", http.StatusBadRequest)
		return
	}
	if len(in.Title) > 200 || len(in.Body) > 4000 {
		http.Error(w, "notification too large", http.StatusBadRequest)
		return
	}
	if in.Payload == nil {
		in.Payload = map[string]any{}
	}
	var id string
	err := s.db.QueryRow(r.Context(), `insert into public.testagram_notifications
		(user_id,title,body,payload) values($1,$2,$3,$4) returning id`,
		in.UserID, strings.TrimSpace(in.Title), strings.TrimSpace(in.Body), in.Payload).Scan(&id)
	if err != nil {
		http.Error(w, "notification queue failed", http.StatusInternalServerError)
		return
	}
	jsonOut(w, http.StatusAccepted, map[string]string{"id": id, "status": "queued"})
}

func (s *service) stream(w http.ResponseWriter, r *http.Request) {
	user, ok := s.user(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.deliverOne(r.Context(), user, w, flusher); err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("notification stream: %v", err)
			}
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *service) deliverOne(ctx context.Context, user string, w http.ResponseWriter, flusher http.Flusher) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var n notification
	err = tx.QueryRow(ctx, `select id,user_id,title,body,payload from public.testagram_notifications
		where user_id=$1 and status='queued' and available_at<=now()
		order by created_at for update skip locked limit 1`, user).
		Scan(&n.ID, &n.UserID, &n.Title, &n.Body, &n.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	data, _ := json.Marshal(n)
	if _, err = w.Write([]byte("event: notification\nid: " + n.ID + "\ndata: " + string(data) + "\n\n")); err != nil {
		return err
	}
	flusher.Flush()
	if _, err = tx.Exec(ctx, `update public.testagram_notifications
		set status='delivered',delivered_at=now(),updated_at=now(),attempts=attempts+1 where id=$1`, n.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type ledgerNotificationCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

type ledgerNotification struct {
	ID        string         `json:"id"`
	ActorID   string         `json:"actor_id,omitempty"`
	PostID    string         `json:"post_id,omitempty"`
	ReplyID   string         `json:"reply_id,omitempty"`
	Kind      string         `json:"kind"`
	Payload   map[string]any `json:"payload"`
	ReadAt    *time.Time     `json:"read_at,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

func encodeLedgerCursor(cursor ledgerNotificationCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeLedgerCursor(value string) (ledgerNotificationCursor, error) {
	var cursor ledgerNotificationCursor
	if value == "" {
		return cursor, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) > 512 {
		return cursor, errors.New("invalid notification cursor")
	}
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.CreatedAt.IsZero() ||
		len(cursor.ID) != 36 || strings.Count(cursor.ID, "-") != 4 {
		return ledgerNotificationCursor{}, errors.New("invalid notification cursor")
	}
	return cursor, nil
}

// ledgerStream is the real-time projection of Backend's canonical notification
// inbox. It never writes a second notification queue or marks inbox rows read.
// The Backend remains the only event producer and read-state authority.
func (s *service) ledgerStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.user(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	cursor, err := decodeLedgerCursor(strings.TrimSpace(r.Header.Get("Last-Event-ID")))
	if err != nil {
		http.Error(w, "invalid notification cursor", http.StatusBadRequest)
		return
	}
	if cursor.CreatedAt.IsZero() {
		if err := s.db.QueryRow(r.Context(), "SELECT now() - interval '30 seconds'").Scan(&cursor.CreatedAt); err != nil {
			http.Error(w, "notification inbox unavailable", http.StatusServiceUnavailable)
			return
		}
		cursor.CreatedAt = cursor.CreatedAt.UTC()
		cursor.ID = "00000000-0000-0000-0000-000000000000"
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	poll := time.NewTicker(time.Second)
	heartbeat := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer heartbeat.Stop()
	for {
		delivered, next, streamErr := s.deliverLedgerOne(r.Context(), user, cursor, w, flusher)
		if streamErr != nil {
			if !errors.Is(streamErr, context.Canceled) {
				log.Printf("ledger notification stream: %v", streamErr)
			}
			return
		}
		if delivered {
			cursor = next
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": keep-alive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *service) deliverLedgerOne(ctx context.Context, user string, cursor ledgerNotificationCursor, w http.ResponseWriter, flusher http.Flusher) (bool, ledgerNotificationCursor, error) {
	var item ledgerNotification
	err := s.db.QueryRow(ctx, `
		SELECT id::text, COALESCE(actor_id::text,''), COALESCE(post_id::text,''),
		       COALESCE(reply_id::text,''), kind, payload, read_at, created_at
		FROM testagram_local.notifications
		WHERE recipient_id=$1
		  AND (created_at > $2 OR (created_at = $2 AND id > $3::uuid))
		ORDER BY created_at ASC, id ASC
		LIMIT 1
	`, user, cursor.CreatedAt, cursor.ID).Scan(
		&item.ID, &item.ActorID, &item.PostID, &item.ReplyID,
		&item.Kind, &item.Payload, &item.ReadAt, &item.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, cursor, nil
	}
	if err != nil {
		return false, cursor, err
	}
	next := ledgerNotificationCursor{CreatedAt: item.CreatedAt.UTC(), ID: item.ID}
	data, err := json.Marshal(item)
	if err != nil {
		return false, cursor, err
	}
	frame := "event: notification\nid: " + encodeLedgerCursor(next) + "\ndata: " + string(data) + "\n\n"
	if _, err := w.Write([]byte(frame)); err != nil {
		return false, cursor, err
	}
	flusher.Flush()
	return true, next, nil
}

func (s *service) user(r *http.Request) (string, bool) {
	raw := bearer(r)
	if raw == "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	userID, err := nativeauth.ResolveUserID(ctx, s.db, raw, s.authJWTSecret)
	return userID, err == nil
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

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("%s is required", k)
	}
	return v
}
