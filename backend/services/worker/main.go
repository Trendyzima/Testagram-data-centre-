package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

)

func main() {
	db, err := pgxpool.New(context.Background(), mustEnv("DATABASE_URL")); if err != nil { log.Fatal(err) }; defer db.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) { if err := db.Ping(r.Context()); err != nil { http.Error(w, "database unavailable", 503); return }; w.WriteHeader(http.StatusOK) })
	go func() { srv := &http.Server{Addr: env("WORKER_ADDR", ":8091"), Handler: mux, ReadHeaderTimeout: 5 * time.Second}; log.Printf("Testagram maintenance worker listening on %s", srv.Addr); if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed { log.Fatal(err) } }()
	ticker := time.NewTicker(30 * time.Second); defer ticker.Stop()
	for { if err := maintain(context.Background(), db); err != nil { log.Printf("maintenance: %v", err) }; <-ticker.C }
}

func maintain(ctx context.Context, db *pgxpool.Pool) error {
	if _, err := db.Exec(ctx, "update public.vps_nodes set status='offline' where status='online' and last_seen_at < now()-interval '90 seconds'"); err != nil { return err }
	_, err := db.Exec(ctx, "update public.vps_workloads set status='queued', node_id=null, updated_at=now() where status in ('assigned','running') and node_id in (select id from public.vps_nodes where status='offline')")
	return err
}

func env(k, d string) string { if v := os.Getenv(k); v != "" { return v }; return d }
func mustEnv(k string) string { v := os.Getenv(k); if v == "" { log.Fatalf("%s is required", k) }; return v }
