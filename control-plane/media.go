package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/Trendyzima/Testagram-data-centre-/internal/objectstorage"
	"github.com/jackc/pgx/v5"
)

var mediaObjectKeyPattern = regexp.MustCompile(`^users/[A-Za-z0-9_-]{1,128}/[a-f0-9]{32}$`)
var mediaSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type mediaUploadURLRequest struct {
	OwnerID     string `json:"owner_id"`
	Namespace   string `json:"namespace"`
	Key         string `json:"key"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

type mediaVerifyRequest struct {
	OwnerID     string `json:"owner_id"`
	Namespace   string `json:"namespace"`
	Key         string `json:"key"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
}

type mediaDownloadRequest struct {
	RequesterID string `json:"requester_id"`
	OwnerID     string `json:"owner_id"`
	Namespace   string `json:"namespace"`
	Key         string `json:"key"`
}

type mediaDeleteRequest struct {
	RequesterID string `json:"requester_id"`
	OwnerID     string `json:"owner_id"`
	Namespace   string `json:"namespace"`
	Key         string `json:"key"`
}

type mediaReplicationJob struct {
	ID          string
	OwnerID     string
	Namespace   string
	Key         string
	ContentType string
	SizeBytes   int64
	SHA256      string
	LeaseToken  string
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request must contain exactly one JSON object")
		}
		return err
	}
	return nil
}

func validMediaRequest(ownerID, namespace, key string) bool {
	return ownerID != "" && len(ownerID) <= 128 &&
		(namespace == "media-private" || namespace == "media-public") &&
		mediaObjectKeyPattern.MatchString(key) &&
		strings.HasPrefix(key, "users/"+ownerID+"/")
}

func (s *Server) mediaUnavailable(w http.ResponseWriter) bool {
	if s.media == nil {
		reply(w, http.StatusServiceUnavailable, map[string]string{"error": "media storage control plane is disabled"})
		return true
	}
	return false
}

func (s *Server) mediaUploadURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.internalOnly(w, r) || s.mediaUnavailable(w) {
		return
	}
	var in mediaUploadURLRequest
	if err := decodeStrictJSON(w, r, &in); err != nil {
		reply(w, http.StatusBadRequest, map[string]string{"error": "invalid media upload request"})
		return
	}
	if !validMediaRequest(in.OwnerID, in.Namespace, in.Key) || in.ContentType == "" || in.SizeBytes < 1 {
		reply(w, http.StatusBadRequest, map[string]string{"error": "invalid media upload metadata"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	grant, err := s.media.UploadURL(ctx, in.OwnerID, in.Namespace, in.Key, in.ContentType, in.SizeBytes)
	if err != nil {
		reply(w, http.StatusBadRequest, map[string]string{"error": "media upload authorization rejected"})
		return
	}
	var id string
	err = s.db.QueryRow(ctx, `INSERT INTO testagram_storage.media_objects
		(owner_id,namespace,object_key,content_type,size_bytes,status)
		VALUES($1,$2,$3,$4,$5,'upload_pending')
		ON CONFLICT(owner_id,namespace,object_key) DO UPDATE
		SET updated_at=NOW()
		WHERE testagram_storage.media_objects.content_type=EXCLUDED.content_type
		  AND testagram_storage.media_objects.size_bytes=EXCLUDED.size_bytes
		  AND testagram_storage.media_objects.status='upload_pending'
		RETURNING id::text`, in.OwnerID, in.Namespace, in.Key, in.ContentType, in.SizeBytes).Scan(&id)
	if err != nil {
		reply(w, http.StatusConflict, map[string]string{"error": "media upload intent conflicts with an existing object"})
		return
	}
	reply(w, http.StatusCreated, map[string]any{
		"intent_id": id, "upload_url": grant.URL, "expires_at": grant.ExpiresAt,
		"max_bytes": grant.MaxBytes, "primary": grant.Primary,
	})
}

func (s *Server) mediaVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.internalOnly(w, r) || s.mediaUnavailable(w) {
		return
	}
	var in mediaVerifyRequest
	if err := decodeStrictJSON(w, r, &in); err != nil {
		reply(w, http.StatusBadRequest, map[string]string{"error": "invalid media verification request"})
		return
	}
	if !validMediaRequest(in.OwnerID, in.Namespace, in.Key) || in.ContentType == "" || in.SizeBytes < 1 || !mediaSHA256Pattern.MatchString(strings.ToLower(in.SHA256)) {
		reply(w, http.StatusBadRequest, map[string]string{"error": "invalid media verification metadata"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]string{"error": "media ledger unavailable"})
		return
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var status, contentType string
	var size int64
	var existingSHA string
	err = tx.QueryRow(ctx, `SELECT status,content_type,size_bytes,coalesce(sha256,'')
		FROM testagram_storage.media_objects
		WHERE owner_id=$1 AND namespace=$2 AND object_key=$3
		FOR UPDATE`, in.OwnerID, in.Namespace, in.Key).Scan(&status, &contentType, &size, &existingSHA)
	if err == pgx.ErrNoRows {
		reply(w, http.StatusNotFound, map[string]string{"error": "media upload intent not found"})
		return
	}
	if err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]string{"error": "media ledger unavailable"})
		return
	}
	digest := strings.ToLower(in.SHA256)
	if contentType != in.ContentType || size != in.SizeBytes || (existingSHA != "" && existingSHA != digest) {
		reply(w, http.StatusConflict, map[string]string{"error": "media verification metadata conflicts with upload intent"})
		return
	}
	if status == "protected" {
		_ = tx.Commit(ctx)
		reply(w, http.StatusOK, map[string]any{"status": "protected", "sha256": digest})
		return
	}
	if status != "upload_pending" && status != "replication_pending" && status != "replication_failed" {
		reply(w, http.StatusConflict, map[string]string{"error": "media object is not eligible for verification"})
		return
	}
	_, err = tx.Exec(ctx, `UPDATE testagram_storage.media_objects
		SET sha256=$4,status='replication_pending',updated_at=NOW()
		WHERE owner_id=$1 AND namespace=$2 AND object_key=$3`,
		in.OwnerID, in.Namespace, in.Key, digest)
	if err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]string{"error": "media verification state could not be saved"})
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO testagram_storage.media_replication_jobs
		(owner_id,namespace,object_key,content_type,size_bytes,sha256,status)
		VALUES($1,$2,$3,$4,$5,$6,'queued')
		ON CONFLICT(owner_id,namespace,object_key) DO UPDATE
		SET content_type=EXCLUDED.content_type,size_bytes=EXCLUDED.size_bytes,sha256=EXCLUDED.sha256,
		    status='queued',attempts=0,available_at=NOW(),lease_until=NULL,lease_token=NULL,
		    last_error_code=NULL,updated_at=NOW()
		WHERE testagram_storage.media_replication_jobs.status IN ('queued','dead')`,
		in.OwnerID, in.Namespace, in.Key, in.ContentType, in.SizeBytes, digest)
	if err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]string{"error": "media replication job could not be queued"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]string{"error": "media verification transaction could not be committed"})
		return
	}
	reply(w, http.StatusAccepted, map[string]any{"status": "replication_pending", "sha256": digest})
}

func (s *Server) mediaStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if !s.internalOnly(w, r) || s.mediaUnavailable(w) {
		return
	}
	ownerID := r.URL.Query().Get("owner_id")
	namespace := r.URL.Query().Get("namespace")
	key := r.URL.Query().Get("key")
	if !validMediaRequest(ownerID, namespace, key) {
		reply(w, http.StatusBadRequest, map[string]string{"error": "invalid media status key"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var status, contentType, sha256Hex string
	var size int64
	var primaryVerified, replicaVerified *time.Time
	err := s.db.QueryRow(ctx, `SELECT status,content_type,size_bytes,coalesce(sha256,''),primary_verified_at,replica_verified_at
		FROM testagram_storage.media_objects WHERE owner_id=$1 AND namespace=$2 AND object_key=$3`,
		ownerID, namespace, key).Scan(&status, &contentType, &size, &sha256Hex, &primaryVerified, &replicaVerified)
	if err == pgx.ErrNoRows {
		reply(w, http.StatusNotFound, map[string]string{"error": "media object not found"})
		return
	}
	if err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]string{"error": "media status unavailable"})
		return
	}
	reply(w, http.StatusOK, map[string]any{
		"status": status, "content_type": contentType, "size_bytes": size, "sha256": sha256Hex,
		"primary_verified_at": primaryVerified, "replica_verified_at": replicaVerified,
	})
}

func (s *Server) mediaDownloadURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.internalOnly(w, r) || s.mediaUnavailable(w) {
		return
	}
	var in mediaDownloadRequest
	if err := decodeStrictJSON(w, r, &in); err != nil {
		reply(w, http.StatusBadRequest, map[string]string{"error": "invalid media download request"})
		return
	}
	if !validMediaRequest(in.OwnerID, in.Namespace, in.Key) || in.RequesterID == "" {
		reply(w, http.StatusBadRequest, map[string]string{"error": "invalid media download authorization"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var status, digest string
	var size int64
	err := s.db.QueryRow(ctx, `SELECT status,sha256,size_bytes FROM testagram_storage.media_objects
		WHERE owner_id=$1 AND namespace=$2 AND object_key=$3`, in.OwnerID, in.Namespace, in.Key).Scan(&status, &digest, &size)
	if err == pgx.ErrNoRows {
		reply(w, http.StatusNotFound, map[string]string{"error": "verified media object not found"})
		return
	}
	if err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]string{"error": "media ledger unavailable"})
		return
	}
	if status != "protected" {
		reply(w, http.StatusNotFound, map[string]string{"error": "verified media object not found"})
		return
	}
	grant, err := s.media.DownloadURL(ctx, in.RequesterID, in.OwnerID, in.Namespace, in.Key, strings.TrimSpace(digest), size)
	if err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]string{"error": "verified media delivery URL unavailable"})
		return
	}
	reply(w, http.StatusOK, grant)
}

func (s *Server) mediaDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.internalOnly(w, r) || s.mediaUnavailable(w) {
		return
	}
	var in mediaDeleteRequest
	if err := decodeStrictJSON(w, r, &in); err != nil {
		reply(w, http.StatusBadRequest, map[string]string{"error": "invalid media deletion request"})
		return
	}
	if !validMediaRequest(in.OwnerID, in.Namespace, in.Key) || in.RequesterID != in.OwnerID {
		reply(w, http.StatusForbidden, map[string]string{"error": "media deletion is owner scoped"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	_, err := s.db.Exec(ctx, `UPDATE testagram_storage.media_objects SET status='delete_pending',updated_at=NOW()
		WHERE owner_id=$1 AND namespace=$2 AND object_key=$3 AND status <> 'deleted'`,
		in.OwnerID, in.Namespace, in.Key)
	if err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]string{"error": "media deletion state unavailable"})
		return
	}
	if err := s.media.Delete(ctx, in.RequesterID, in.OwnerID, in.Namespace, in.Key); err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]string{"error": "media deletion pending provider recovery"})
		return
	}
	_, err = s.db.Exec(ctx, `UPDATE testagram_storage.media_objects SET status='deleted',deleted_at=NOW(),updated_at=NOW()
		WHERE owner_id=$1 AND namespace=$2 AND object_key=$3`, in.OwnerID, in.Namespace, in.Key)
	if err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]string{"error": "media deletion confirmation could not be persisted"})
		return
	}
	reply(w, http.StatusOK, map[string]any{"status": "deleted"})
}

func (s *Server) runMediaReplication(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if s.media != nil {
			s.processMediaReplicationBatch(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) processMediaReplicationBatch(ctx context.Context) {
	batchCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	rows, err := s.db.Query(batchCtx, `WITH picked AS (
		SELECT id FROM testagram_storage.media_replication_jobs
		WHERE status IN ('queued','processing') AND available_at <= NOW()
		  AND (lease_until IS NULL OR lease_until < NOW())
		ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 5
	)
	UPDATE testagram_storage.media_replication_jobs AS j
	SET status='processing',attempts=j.attempts+1,lease_until=NOW()+interval '60 minutes',
	    lease_token=gen_random_uuid(),updated_at=NOW()
	FROM picked WHERE j.id=picked.id
	RETURNING j.id::text,j.owner_id,j.namespace,j.object_key,j.content_type,j.size_bytes,j.sha256,j.lease_token::text`)
	if err != nil {
		return
	}
	jobs := make([]mediaReplicationJob, 0, 5)
	for rows.Next() {
		var job mediaReplicationJob
		if err := rows.Scan(&job.ID, &job.OwnerID, &job.Namespace, &job.Key, &job.ContentType, &job.SizeBytes, &job.SHA256, &job.LeaseToken); err != nil {
			rows.Close()
			return
		}
		jobs = append(jobs, job)
	}
	rows.Close()
	for _, job := range jobs {
		s.processMediaReplicationJob(ctx, job)
	}
}

func (s *Server) processMediaReplicationJob(ctx context.Context, job mediaReplicationJob) {
	jobCtx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	err := s.media.Replicate(jobCtx, job.OwnerID, job.Namespace, job.Key, job.ContentType, job.SHA256, job.SizeBytes)
	cancel()
	if err == nil {
		tx, txErr := s.db.Begin(ctx)
		if txErr != nil {
			return
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		tag, updateErr := tx.Exec(ctx, `UPDATE testagram_storage.media_replication_jobs
			SET status='completed',lease_until=NULL,lease_token=NULL,last_error_code=NULL,updated_at=NOW()
			WHERE id=$1::uuid AND lease_token=$2::uuid AND status='processing'`, job.ID, job.LeaseToken)
		if updateErr != nil || tag.RowsAffected() != 1 {
			return
		}
		_, updateErr = tx.Exec(ctx, `UPDATE testagram_storage.media_objects
			SET status='protected',primary_verified_at=coalesce(primary_verified_at,NOW()),
			    replica_verified_at=NOW(),updated_at=NOW()
			WHERE owner_id=$1 AND namespace=$2 AND object_key=$3 AND status='replication_pending'`,
			job.OwnerID, job.Namespace, job.Key)
		if updateErr != nil {
			return
		}
		_ = tx.Commit(ctx)
		return
	}

	tx, txErr := s.db.Begin(ctx)
	if txErr != nil {
		return
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var attempts int
	if txErr = tx.QueryRow(ctx, `SELECT attempts FROM testagram_storage.media_replication_jobs
		WHERE id=$1::uuid AND lease_token=$2::uuid FOR UPDATE`, job.ID, job.LeaseToken).Scan(&attempts); txErr != nil {
		return
	}
	state := "queued"
	objectState := "replication_pending"
	if attempts >= 12 {
		state = "dead"
		objectState = "replication_failed"
	}
	_, txErr = tx.Exec(ctx, `UPDATE testagram_storage.media_replication_jobs
		SET status=$3,lease_until=NULL,lease_token=NULL,
		    available_at=CASE WHEN $3='queued' THEN NOW()+LEAST(3600,(5*power(2,LEAST(attempts,10)))::integer)*interval '1 second' ELSE available_at END,
		    last_error_code='replica_transfer_failed',updated_at=NOW()
		WHERE id=$1::uuid AND lease_token=$2::uuid`, job.ID, job.LeaseToken, state)
	if txErr != nil {
		return
	}
	_, txErr = tx.Exec(ctx, `UPDATE testagram_storage.media_objects SET status=$4,updated_at=NOW()
		WHERE owner_id=$1 AND namespace=$2 AND object_key=$3`, job.OwnerID, job.Namespace, job.Key, objectState)
	if txErr != nil {
		return
	}
	_ = tx.Commit(ctx)
}
