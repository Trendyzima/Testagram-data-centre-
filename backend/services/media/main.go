package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	mathrand "math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const apiVersion = "1"
const maxJSON = 64 << 10

type config struct {
	token string
	identityToken string
	addr string
	primary s3Store
	replica s3Store
	identityPrimary s3Store
	metadataPrimary s3Store
	metadataReplica s3Store
	uploadTTL time.Duration
	downloadTTL time.Duration
}
type s3Store struct { name, endpoint, region, bucket, access, secret string }
type server struct { cfg config; db *pgxpool.Pool; http *http.Client }
type uploadRequest struct { OwnerID string `json:"owner_id"`; Namespace string `json:"namespace"`; Key string `json:"key"`; ContentType string `json:"content_type"`; SizeBytes int64 `json:"size_bytes"` }
type verifyRequest struct { uploadRequest; SHA256 string `json:"sha256"` }
type downloadRequest struct { RequesterID string `json:"requester_id"`; OwnerID string `json:"owner_id"`; Namespace string `json:"namespace"`; Key string `json:"key"`; Method string `json:"method,omitempty"` }
type deleteRequest = downloadRequest
type object struct { OwnerID string; Namespace string; Key string; ContentType string; Size int64; SHA string; Status string; PrimaryVerified *time.Time; ReplicaVerified *time.Time }

func validateIdentityStoreIsolation(media, identity s3Store) error {
	if media.bucket == "" || identity.bucket == "" || media.bucket == identity.bucket {
		return errors.New("B2 identity evidence bucket must be distinct from the ordinary-media bucket")
	}
	if media.access == "" || identity.access == "" || media.secret == "" || identity.secret == "" ||
		media.access == identity.access || media.secret == identity.secret {
		return errors.New("B2 identity evidence credentials must be distinct from ordinary-media credentials")
	}
	if media.endpoint != identity.endpoint || media.region != identity.region {
		return errors.New("ordinary-media and identity-evidence B2 stores must use the same B2 endpoint and region")
	}
	return nil
}

func main() {
	cfg := config{
		token: required("MEDIA_INTERNAL_TOKEN"), identityToken: env("IDENTITY_EVIDENCE_INTERNAL_TOKEN", ""), addr: env("MEDIA_ADDR", ":8096"),
		primary: s3Store{name:"backblaze_b2", endpoint:required("B2_S3_ENDPOINT"), region:required("B2_S3_REGION"), bucket:required("B2_S3_BUCKET"), access:required("B2_S3_ACCESS_KEY_ID"), secret:required("B2_S3_SECRET_ACCESS_KEY")},
		replica: s3Store{name:"cloudflare_r2", endpoint:required("CLOUDFLARE_R2_ENDPOINT"), region:env("CLOUDFLARE_R2_REGION","auto"), bucket:required("CLOUDFLARE_R2_BUCKET"), access:required("CLOUDFLARE_R2_ACCESS_KEY_ID"), secret:required("CLOUDFLARE_R2_SECRET_ACCESS_KEY")},
		identityPrimary: s3Store{name:"backblaze_b2_identity_evidence", endpoint:required("B2_S3_ENDPOINT"), region:required("B2_S3_REGION"), bucket:required("B2_S3_IDENTITY_EVIDENCE_PRIVATE_BUCKET"), access:required("B2_S3_IDENTITY_EVIDENCE_ACCESS_KEY_ID"), secret:required("B2_S3_IDENTITY_EVIDENCE_SECRET_ACCESS_KEY")},
		metadataPrimary: s3Store{name:"instabox_vps_metadata", endpoint:required("VPS_MINIO_ENDPOINT"), region:env("VPS_MINIO_REGION","us-east-1"), bucket:required("VPS_MINIO_METADATA_BUCKET"), access:required("VPS_MINIO_METADATA_ACCESS_KEY_ID"), secret:required("VPS_MINIO_METADATA_SECRET_ACCESS_KEY")},
		metadataReplica: s3Store{name:"cloudflare_r2_metadata", endpoint:required("CLOUDFLARE_R2_ENDPOINT"), region:env("CLOUDFLARE_R2_REGION","auto"), bucket:required("CLOUDFLARE_R2_METADATA_BUCKET"), access:required("CLOUDFLARE_R2_ACCESS_KEY_ID"), secret:required("CLOUDFLARE_R2_SECRET_ACCESS_KEY")},
		uploadTTL: duration("MEDIA_UPLOAD_TTL", 45*time.Minute), downloadTTL: duration("MEDIA_DOWNLOAD_TTL", 5*time.Minute),
	}
	if len(cfg.token)<32 { log.Fatal("MEDIA_INTERNAL_TOKEN must be at least 32 characters") }
	if cfg.identityToken != "" && len(cfg.identityToken) < 32 { log.Fatal("IDENTITY_EVIDENCE_INTERNAL_TOKEN must be at least 32 characters when configured") }
	if cfg.identityToken != "" && subtle.ConstantTimeCompare([]byte(cfg.identityToken), []byte(cfg.token)) == 1 { log.Fatal("IDENTITY_EVIDENCE_INTERNAL_TOKEN must be distinct from MEDIA_INTERNAL_TOKEN") }
	if cfg.uploadTTL < time.Minute || cfg.uploadTTL > time.Hour { log.Fatal("MEDIA_UPLOAD_TTL must be between 1 minute and 1 hour") }
	if cfg.downloadTTL < time.Minute || cfg.downloadTTL > 15*time.Minute { log.Fatal("MEDIA_DOWNLOAD_TTL must be between 1 minute and 15 minutes") }
	if err:=validateStore(cfg.primary);err!=nil{log.Fatalf("invalid B2 primary configuration: %v",err)}
	if err:=validateStore(cfg.replica);err!=nil{log.Fatalf("invalid R2 replica configuration: %v",err)}
	if err:=validateStore(cfg.identityPrimary);err!=nil{log.Fatalf("invalid B2 identity evidence configuration: %v",err)}
	if err:=validateIdentityStoreIsolation(cfg.primary,cfg.identityPrimary);err!=nil { log.Fatal(err) }
	if err:=validateStore(cfg.metadataPrimary);err!=nil{log.Fatalf("invalid VPS metadata store configuration: %v",err)}
	if err:=validateStore(cfg.metadataReplica);err!=nil{log.Fatalf("invalid R2 metadata store configuration: %v",err)}
	db,err:=pgxpool.New(context.Background(),required("MEDIA_DATABASE_URL")); if err!=nil { log.Fatal(err) }; defer db.Close()
	ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second); defer cancel()
	if err=db.Ping(ctx); err!=nil { log.Fatalf("media database unavailable: %v",err) }
	s:=&server{cfg:cfg,db:db,http:&http.Client{Timeout:30*time.Minute,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}}
	mux:=http.NewServeMux()
	mux.HandleFunc("/healthz",s.health); mux.HandleFunc("/readyz",s.ready)
	mux.HandleFunc("/internal/v1/media/readyz",s.mediaReady)
	mux.HandleFunc("/internal/v1/media/upload-url",s.uploadURL)
	mux.HandleFunc("/internal/v1/media/verify",s.verify)
	mux.HandleFunc("/internal/v1/media/status",s.status)
	mux.HandleFunc("/internal/v1/media/download-url",s.downloadURL)
	mux.HandleFunc("/internal/v1/media/delete",s.delete)
	mux.HandleFunc("/internal/v1/identity-evidence/readyz",s.identityReady)
	mux.HandleFunc("/internal/v1/identity-evidence/upload-url",s.uploadURL)
	mux.HandleFunc("/internal/v1/identity-evidence/verify",s.verify)
	mux.HandleFunc("/internal/v1/identity-evidence/status",s.status)
	mux.HandleFunc("/internal/v1/identity-evidence/download-url",s.downloadURL)
	mux.HandleFunc("/internal/v1/identity-evidence/delete",s.delete)
	go s.worker(context.Background())
	go s.reconcileWorker(context.Background())
	srv:=&http.Server{Addr:cfg.addr,Handler:logging(mux),ReadHeaderTimeout:5*time.Second,ReadTimeout:15*time.Second,WriteTimeout:30*time.Second,IdleTimeout:60*time.Second}
	log.Printf("dedicated media service listening on %s",cfg.addr)
	log.Fatal(srv.ListenAndServe())
}

type mediaMetadataManifest struct {
	SchemaVersion string `json:"schema_version"`
	OwnerID string `json:"owner_id"`
	Namespace string `json:"namespace"`
	ObjectKey string `json:"object_key"`
	SizeBytes int64 `json:"size_bytes"`
	ContentType string `json:"content_type"`
	SHA256 string `json:"sha256"`
}

func (s *server) writeMetadataSidecars(ctx context.Context, item reconcileItem) error {
	manifest := mediaMetadataManifest{SchemaVersion:"instabox.media-metadata.v1", OwnerID:item.owner, Namespace:item.namespace, ObjectKey:item.key, SizeBytes:item.size, ContentType:item.contentType, SHA256:strings.ToLower(item.expectedSHA)}
	body, err := json.Marshal(manifest)
	if err != nil { return fmt.Errorf("encode media metadata sidecar: %w", err) }
	key := item.key + ".metadata.json"
	metadataPrimary, metadataReplica := s.metadataStoresForNamespace(item.namespace)
	for _, store := range []s3Store{metadataPrimary, metadataReplica} {
		if store.bucket == "" { continue }
		reader := &lengthReader{Reader:bytes.NewReader(body), n:int64(len(body))}
		resp, err := s.signedRequest(ctx, store, http.MethodPut, key, reader, "application/json")
		if err != nil { return fmt.Errorf("%s metadata PUT: %w", store.name, err) }
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 { return fmt.Errorf("%s metadata PUT status %d", store.name, resp.StatusCode) }
		check, err := s.signedRequest(ctx, store, http.MethodGet, key, nil, "")
		if err != nil { return fmt.Errorf("%s metadata read-back: %w", store.name, err) }
		if check.StatusCode != http.StatusOK { check.Body.Close(); return fmt.Errorf("%s metadata read-back status %d", store.name, check.StatusCode) }
		if check.ContentLength >= 0 && check.ContentLength != int64(len(body)) { check.Body.Close(); return fmt.Errorf("%s metadata sidecar size mismatch", store.name) }
		if got := strings.TrimSpace(strings.Split(check.Header.Get("Content-Type"), ";")[0]); got != "" && got != "application/json" { check.Body.Close(); return fmt.Errorf("%s metadata sidecar content type mismatch", store.name) }
		h := sha256.New()
		n, readErr := io.Copy(h, io.LimitReader(check.Body, int64(len(body))+1))
		check.Body.Close()
		if readErr != nil || n != int64(len(body)) || hex.EncodeToString(h.Sum(nil)) != hashHex(body) { return fmt.Errorf("%s metadata sidecar SHA-256 read-back mismatch", store.name) }
	}
	return nil
}

func (s *server) deleteMetadataSidecars(ctx context.Context, namespace, key string) error {
	metadataKey := key + ".metadata.json"
	metadataPrimary, metadataReplica := s.metadataStoresForNamespace(namespace)
	for _, store := range []s3Store{metadataPrimary, metadataReplica} {
		if store.bucket == "" { continue }
		if namespace == "identity-evidence-private" {
			if err := s.deleteAllB2ObjectVersions(ctx, store, metadataKey); err != nil {
				return fmt.Errorf("%s permanent identity metadata purge: %w", store.name, err)
			}
			continue
		}
		resp, err := s.signedRequest(ctx, store, http.MethodDelete, metadataKey, nil, "")
		if err != nil { return fmt.Errorf("%s metadata delete: %w", store.name, err) }
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("%s metadata delete status %d", store.name, resp.StatusCode)
		}
	}
	return nil
}

func (s *server) storesForNamespace(namespace string) (s3Store, s3Store) {
	if namespace == "identity-evidence-private" { return s.cfg.identityPrimary, s3Store{} }
	return s.cfg.primary, s.cfg.replica
}
func (s *server) metadataStoresForNamespace(namespace string) (s3Store, s3Store) {
	if namespace == "identity-evidence-private" { return s.cfg.identityPrimary, s3Store{} }
	return s.cfg.metadataPrimary, s.cfg.metadataReplica
}
func (s *server) health(w http.ResponseWriter,r *http.Request){w.WriteHeader(200);_,_=w.Write([]byte("ok\n"))}
func (s *server) ready(w http.ResponseWriter,r *http.Request){ctx,c:=context.WithTimeout(r.Context(),2*time.Second);defer c();if err:=s.db.Ping(ctx);err!=nil{http.Error(w,"database unavailable",503);return};w.WriteHeader(200);_,_=w.Write([]byte("ready\n"))}
func (s *server) mediaReady(w http.ResponseWriter, r *http.Request) {
	if !s.auth(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if s.db == nil || s.db.Ping(ctx) != nil {
		writeErr(w, http.StatusServiceUnavailable, "media database is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "namespace": "media"})
}
func (s *server) auth(w http.ResponseWriter,r *http.Request)bool{
	authorizationValues:=r.Header.Values("Authorization")
	if len(authorizationValues)!=1 { writeErr(w,401,"unauthorized");return false }
	authorization:=authorizationValues[0]
	if !strings.HasPrefix(authorization,"Bearer ") { writeErr(w,401,"unauthorized");return false }
	got:=strings.TrimPrefix(authorization,"Bearer ")
	if got=="" || got!=strings.TrimSpace(got) { writeErr(w,401,"unauthorized");return false }
	expected:=s.cfg.token
	if strings.HasPrefix(r.URL.Path,"/internal/v1/identity-evidence/") {
		expected=s.cfg.identityToken
	}
	if expected==""||got==""||strings.ContainsAny(got," \t\r\n")||subtle.ConstantTimeCompare([]byte(got),[]byte(expected))!=1||r.Header.Get("X-Testagram-API-Version")!=apiVersion { writeErr(w,401,"unauthorized");return false };return true
}
func (s *server) identityReady(w http.ResponseWriter, r *http.Request) {
	if !s.auth(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if s.db == nil || s.db.Ping(ctx) != nil {
		writeErr(w, http.StatusServiceUnavailable, "identity evidence database is unavailable")
		return
	}
	for _, store := range []s3Store{s.cfg.identityPrimary} {
		if err := s.probeIdentityEvidenceStore(ctx, store); err != nil {
			log.Printf("identity evidence storage readiness failed for %s: %v", store.name, err)
			writeErr(w, http.StatusServiceUnavailable, "identity evidence storage is unavailable")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "namespace": "identity-evidence-private", "provider": "backblaze_b2", "primary": "verified"})
}

func (s *server) probeIdentityEvidenceStore(ctx context.Context, store s3Store) error {
	// ListObjectVersions is read-only and proves the dedicated key has the
	// bucket-scoped list capability required for permanent deletion. Unlike
	// HEAD Bucket, it does not require permission to enumerate bucket names.
	if err := validateStore(store); err != nil { return err }
	if _, err := s.collectB2ObjectVersions(ctx, store, "__testagram_identity_readiness_probe__/"); err != nil {
		return fmt.Errorf("%s identity evidence version-list readiness: %w", store.name, err)
	}
	return nil
}

func (s *server) namespaceAllowedForRoute(w http.ResponseWriter,r *http.Request,namespace string)bool{
	identityRoute:=strings.HasPrefix(r.URL.Path,"/internal/v1/identity-evidence/")
	identityNamespace:=namespace=="identity-evidence-private"
	if identityRoute!=identityNamespace { writeErr(w,403,"media namespace is not allowed on this private route");return false }
	return true
}
func (s *server) uploadURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { method(w, "POST"); return }
	if !s.auth(w, r) { return }
	var in uploadRequest
	if !decode(w, r, &in) || !validUpload(in) { writeErr(w, 400, "invalid upload request"); return }
	if !s.namespaceAllowedForRoute(w, r, in.Namespace) { return }
	id, err := randomID()
	if err != nil { writeErr(w, 503, "secure upload grant generation failed"); return }
	uploadTTL := s.cfg.uploadTTL
	// Identity evidence upload grants are deliberately short-lived. Permanent
	// deletion waits until the signed PUT URL and a short in-flight grace expire.
	if in.Namespace == "identity-evidence-private" && uploadTTL > 5*time.Minute { uploadTTL = 5*time.Minute }
	exp := time.Now().UTC().Add(uploadTTL)
	primary, _ := s.storesForNamespace(in.Namespace)
	u, err := presign(primary, http.MethodPut, in.Key, uploadTTL, in.ContentType)
	if err != nil { writeErr(w, 503, "primary storage signing failed"); return }
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second); defer cancel()
	tx, err := s.db.Begin(ctx)
	if err != nil { writeErr(w, 503, "media state unavailable"); return }
	defer tx.Rollback(ctx)
	lockKey := objectLockKey(in.OwnerID, in.Namespace, in.Key)
	if _, err = tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil { writeErr(w, 503, "media grant lock unavailable"); return }
	tag, err := tx.Exec(ctx, `insert into media_objects(owner_id,namespace,object_key,content_type,size_bytes,status,updated_at)
		values($1,$2,$3,$4,$5,'upload_pending',now())
		on conflict(owner_id,namespace,object_key) do update
		set content_type=excluded.content_type,size_bytes=excluded.size_bytes,status='upload_pending',
			sha256=null,primary_verified_at=null,replica_verified_at=null,updated_at=now()
		where media_objects.status='deleted'
		   or (media_objects.status='upload_pending' and not exists (
				select 1 from media_upload_grants g
				where g.owner_id=excluded.owner_id and g.namespace=excluded.namespace and g.object_key=excluded.object_key
				  and g.expires_at>now() and g.consumed_at is null
		   ))`, in.OwnerID, in.Namespace, in.Key, in.ContentType, in.SizeBytes)
	if err != nil || tag.RowsAffected() != 1 { writeErr(w, 409, "object key already has an active lifecycle or upload grant"); return }
	_, err = tx.Exec(ctx, `delete from media_upload_grants where owner_id=$1 and namespace=$2 and object_key=$3`, in.OwnerID, in.Namespace, in.Key)
	if err == nil { _, err = tx.Exec(ctx, `insert into media_upload_grants(id,owner_id,namespace,object_key,max_bytes,expires_at) values($1,$2,$3,$4,$5,$6)`, id, in.OwnerID, in.Namespace, in.Key, in.SizeBytes, exp) }
	if err == nil { _, err = tx.Exec(ctx, `insert into media_operations(operation_id,owner_id,namespace,object_key,operation,outcome) values($1,$2,$3,$4,'upload_grant','issued')`, id, in.OwnerID, in.Namespace, in.Key) }
	if err == nil { err = tx.Commit(ctx) }
	if err != nil { writeErr(w, 503, "could not persist upload grant"); return }
	writeJSON(w, 201, map[string]any{"intent_id": id, "upload_url": u, "expires_at": exp, "max_bytes": in.SizeBytes, "primary": "backblaze_b2"})
}
func (s *server) verify(w http.ResponseWriter,r *http.Request){
	if r.Method!=http.MethodPost{method(w,"POST");return};if !s.auth(w,r){return}
	var in verifyRequest;if !decode(w,r,&in)||!validUpload(in.uploadRequest)||len(in.SHA256)!=64{writeErr(w,400,"invalid verification request");return};if !s.namespaceAllowedForRoute(w,r,in.Namespace){return}
	want,err:=hex.DecodeString(in.SHA256);if err!=nil||len(want)!=32{writeErr(w,400,"invalid SHA-256");return}
	ctx,c:=context.WithTimeout(r.Context(),5*time.Second);defer c()
	expected:=strings.ToLower(in.SHA256)
	var grant bool
	err=s.db.QueryRow(ctx,`select exists(select 1 from media_upload_grants where owner_id=$1 and namespace=$2 and object_key=$3 and max_bytes=$4 and expires_at>now() and consumed_at is null)`,in.OwnerID,in.Namespace,in.Key,in.SizeBytes).Scan(&grant)
	if err!=nil{writeErr(w,503,"upload grant state unavailable");return}
	if !grant {
		var currentStatus,currentSHA string;var primaryVerified,replicaVerified *time.Time
		stateErr:=s.db.QueryRow(ctx,`select status,coalesce(sha256,''),primary_verified_at,replica_verified_at from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`,in.OwnerID,in.Namespace,in.Key).Scan(&currentStatus,&currentSHA,&primaryVerified,&replicaVerified)
		if stateErr==nil&&currentSHA==expected {
			if currentStatus=="protected"{writeJSON(w,200,map[string]string{"status":"protected","sha256":currentSHA});return}
			if currentStatus=="uploaded"||currentStatus=="replication_pending"||currentStatus=="replication_failed"||currentStatus=="restore_pending" {
				if currentStatus=="replication_failed" {
					op:="verify";nextStatus:="uploaded"
					if in.Namespace == "identity-evidence-private" {
						primaryVerified = nil
						replicaVerified = nil
						_, _ = s.db.Exec(ctx, `update media_objects set primary_verified_at=null,replica_verified_at=null where owner_id=$1 and namespace=$2 and object_key=$3`, in.OwnerID, in.Namespace, in.Key)
					}
					if primaryVerified!=nil { op="replicate";nextStatus="replication_pending" }
					if primaryVerified!=nil && replicaVerified!=nil { op="restore_primary";nextStatus="restore_pending" }
					_,_=s.db.Exec(ctx,`update media_objects set status=$4,updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and status='replication_failed'`,in.OwnerID,in.Namespace,in.Key,nextStatus)
					_,_=s.db.Exec(ctx,`insert into media_outbox(owner_id,namespace,object_key,operation,status,attempts,available_at,locked_until,last_error)
						values($1,$2,$3,$4,'pending',0,now(),null,null)
						on conflict(owner_id,namespace,object_key,operation) do update
						set status='pending',attempts=0,available_at=now(),locked_until=null,last_error=null,updated_at=now()`,
						in.OwnerID,in.Namespace,in.Key,op)
				}
				writeJSON(w,202,map[string]string{"status":"replication_pending","sha256":currentSHA});return
			}
		}
		writeErr(w,409,"no active upload grant");return
	}
	tx,err:=s.db.Begin(ctx);if err!=nil{writeErr(w,503,"durable state unavailable");return};defer tx.Rollback(ctx)
	tag,err:=tx.Exec(ctx,`update media_upload_grants set consumed_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and max_bytes=$4 and expires_at>now() and consumed_at is null`,in.OwnerID,in.Namespace,in.Key,in.SizeBytes)
	if err!=nil||tag.RowsAffected()!=1{writeErr(w,409,"upload grant already consumed");return}
	tag,err=tx.Exec(ctx,`update media_objects set sha256=$4,status='uploaded',primary_verified_at=null,replica_verified_at=null,updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and size_bytes=$5 and status='upload_pending'`,in.OwnerID,in.Namespace,in.Key,expected,in.SizeBytes)
	if err==nil&&tag.RowsAffected()!=1{err=errors.New("object lifecycle changed during verification")}
	if err==nil{_,err=tx.Exec(ctx,`insert into media_outbox(owner_id,namespace,object_key,operation,status,available_at) values($1,$2,$3,'verify','pending',now()) on conflict(owner_id,namespace,object_key,operation) do update set status='pending',attempts=0,available_at=now(),locked_until=null,last_error=null`,in.OwnerID,in.Namespace,in.Key)}
	if err==nil{eventID,idErr:=randomID();if idErr!=nil{err=idErr}else{_,err=tx.Exec(ctx,`insert into media_operations(operation_id,owner_id,namespace,object_key,operation,outcome) values($1,$2,$3,$4,'verify','queued')`,eventID,in.OwnerID,in.Namespace,in.Key)}}
	if err==nil{err=tx.Commit(ctx)};if err!=nil{writeErr(w,503,"could not persist verification job");return}
	writeJSON(w,202,map[string]string{"status":"replication_pending","sha256":expected})
}
func (s *server) status(w http.ResponseWriter,r *http.Request){
	if r.Method!=http.MethodGet{method(w,"GET");return};if !s.auth(w,r){return}
	owner,ns,key:=r.URL.Query().Get("owner_id"),r.URL.Query().Get("namespace"),r.URL.Query().Get("key")
	if !validIdentity(owner)||!validNamespace(ns)||!validObjectForNamespace(ns,key,owner){writeErr(w,400,"invalid status query");return};if !s.namespaceAllowedForRoute(w,r,ns){return}
	var o object;err:=s.db.QueryRow(r.Context(),`select owner_id,namespace,object_key,content_type,size_bytes,coalesce(sha256,''),status,primary_verified_at,replica_verified_at from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`,owner,ns,key).Scan(&o.OwnerID,&o.Namespace,&o.Key,&o.ContentType,&o.Size,&o.SHA,&o.Status,&o.PrimaryVerified,&o.ReplicaVerified)
	if errors.Is(err,pgx.ErrNoRows){writeErr(w,404,"object not found");return};if err!=nil{writeErr(w,503,"media state unavailable");return}
	writeJSON(w,200,map[string]any{"status":o.Status,"content_type":o.ContentType,"size_bytes":o.Size,"sha256":o.SHA,"primary_verified_at":o.PrimaryVerified,"replica_verified_at":o.ReplicaVerified})
}
func (s *server) downloadURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { method(w, "POST"); return }
	if !s.auth(w, r) { return }
	var in downloadRequest
	if !decode(w, r, &in) || !validIdentity(in.RequesterID) || !validIdentity(in.OwnerID) || !validNamespace(in.Namespace) || !validObjectForNamespace(in.Namespace, in.Key, in.OwnerID) { writeErr(w, 400, "invalid download request"); return }; if !s.namespaceAllowedForRoute(w, r, in.Namespace) { return }
	if in.Namespace == "identity-evidence-private" && in.RequesterID != in.OwnerID { writeErr(w, 403, "identity evidence is restricted to its owning Backend application"); return }
	if in.Method == "" { in.Method = http.MethodGet }; if in.Method != http.MethodGet && in.Method != http.MethodHead { writeErr(w, 400, "download method must be GET or HEAD"); return }
	var status, contentType string; var size int64
	err := s.db.QueryRow(r.Context(), `select status,size_bytes,content_type from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`, in.OwnerID, in.Namespace, in.Key).Scan(&status, &size, &contentType)
	if err != nil || status != "protected" { writeErr(w, 404, "protected object not found"); return }
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second); defer cancel()
	primary, replica := s.storesForNamespace(in.Namespace)
	for _, store := range []s3Store{replica, primary} {
		if store.bucket == "" { continue }
		resp, headErr := s.signedRequest(ctx, store, http.MethodHead, in.Key, nil, "")
		if headErr != nil { continue }
		gotType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
		present := resp.StatusCode == http.StatusOK && resp.ContentLength == size && (gotType == "" || gotType == contentType)
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); resp.Body.Close()
		if !present { continue }
		u, signErr := presign(store, in.Method, in.Key, s.cfg.downloadTTL, "")
		if signErr == nil { writeJSON(w, 200, map[string]string{"url": u, "provider": store.name}); return }
	}
	writeErr(w, 503, "no verified origin is currently readable")
}
func (s *server) delete(w http.ResponseWriter,r *http.Request){
	if r.Method!=http.MethodPost{method(w,"POST");return};if !s.auth(w,r){return}
	var in deleteRequest;if !decode(w,r,&in)||!validIdentity(in.RequesterID)||!validIdentity(in.OwnerID)||!validNamespace(in.Namespace)||!validObjectForNamespace(in.Namespace,in.Key,in.OwnerID){writeErr(w,400,"invalid delete request");return};if !s.namespaceAllowedForRoute(w,r,in.Namespace){return}
	if in.Namespace == "identity-evidence-private" && in.RequesterID != in.OwnerID { writeErr(w,403,"identity evidence is restricted to its owning Backend application"); return }
	ctx,c:=context.WithTimeout(r.Context(),5*time.Second);defer c()
	tx,err:=s.db.Begin(ctx);if err!=nil{writeErr(w,503,"durable state unavailable");return};defer tx.Rollback(ctx)
	var old string;err=tx.QueryRow(ctx,`select status from media_objects where owner_id=$1 and namespace=$2 and object_key=$3 for update`,in.OwnerID,in.Namespace,in.Key).Scan(&old)
	if errors.Is(err,pgx.ErrNoRows){writeJSON(w,200,map[string]string{"status":"deleted"});return};if err!=nil{writeErr(w,503,"media state unavailable");return}
	if old=="deleted"{writeJSON(w,200,map[string]string{"status":"deleted"});return}
	if old=="delete_pending" { var jobStatus string; if err=tx.QueryRow(ctx,`select status from media_outbox where owner_id=$1 and namespace=$2 and object_key=$3 and operation='delete'`,in.OwnerID,in.Namespace,in.Key).Scan(&jobStatus);err!=nil{writeErr(w,503,"deletion job state unavailable");return};if jobStatus=="dead"{_,err=tx.Exec(ctx,`update media_outbox set status='retry',attempts=0,available_at=now(),locked_until=null,last_error=null,updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and operation='delete' and status='dead'`,in.OwnerID,in.Namespace,in.Key);if err!=nil{writeErr(w,503,"could not retry dead-letter deletion");return};if err=tx.Commit(ctx);err!=nil{writeErr(w,503,"could not persist deletion retry");return}};writeJSON(w,202,map[string]string{"status":"delete_pending"});return }
	_,err=tx.Exec(ctx,`update media_objects set status='delete_pending',updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3`,in.OwnerID,in.Namespace,in.Key)
	if err==nil{_,err=tx.Exec(ctx,`update media_outbox set status='cancelled',locked_until=null,last_error='superseded by delete' where owner_id=$1 and namespace=$2 and object_key=$3 and operation in ('verify','replicate','restore_primary') and status in ('pending','retry')`,in.OwnerID,in.Namespace,in.Key)}
	if err==nil{_,err=tx.Exec(ctx,`insert into media_outbox(owner_id,namespace,object_key,operation,status,available_at) values($1,$2,$3,'delete','pending',now()) on conflict(owner_id,namespace,object_key,operation) do update set status='pending',attempts=0,available_at=now(),last_error=null`,in.OwnerID,in.Namespace,in.Key)}
	if err==nil{eventID,idErr:=randomID();if idErr!=nil{err=idErr}else{_,err=tx.Exec(ctx,`insert into media_operations(operation_id,owner_id,namespace,object_key,operation,outcome) values($1,$2,$3,$4,'delete','queued')`,eventID,in.OwnerID,in.Namespace,in.Key)}}
	if err==nil{err=tx.Commit(ctx)};if err!=nil{writeErr(w,503,"could not queue durable deletion");return}
	writeJSON(w,202,map[string]string{"status":"delete_pending"})
}
func mediaRetryDelaySeconds(attempt int) int {
	if attempt < 1 { attempt = 1 }
	base := 5
	for i := 1; i < attempt && base < 3600; i++ {
		base *= 2
		if base > 3600 { base = 3600 }
	}
	jitterWindow := base / 5
	delay := base + mathrand.Intn(2*jitterWindow+1) - jitterWindow
	if delay < 1 { return 1 }
	if delay > 3600 { return 3600 }
	return delay
}

func (s *server) worker(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.enqueueLegacyIdentityEvidencePurges(ctx); err != nil {
				log.Printf("legacy identity evidence purge enqueue: %v", err)
			}
			if err := s.processOne(ctx); err != nil {
				log.Printf("media outbox worker: %v", err)
			}
		}
	}
}

// enqueueLegacyIdentityEvidencePurges repairs objects marked deleted by the
// former delete-marker-only implementation. The durable marker is written
// only after all B2 versions and delete markers have been permanently erased.
func (s *server) enqueueLegacyIdentityEvidencePurges(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `
		WITH legacy AS (
			UPDATE media_objects
			SET status='delete_pending', updated_at=now()
			WHERE namespace='identity-evidence-private'
			  AND status='deleted'
			  AND identity_evidence_purged_at IS NULL
			RETURNING owner_id, namespace, object_key
		)
		INSERT INTO media_outbox(owner_id,namespace,object_key,operation,status,attempts,available_at,locked_until,last_error)
		SELECT owner_id,namespace,object_key,'delete','pending',0,now(),NULL,'requeued for permanent B2 version purge'
		FROM legacy
		ON CONFLICT(owner_id,namespace,object_key,operation) DO UPDATE
		SET status='pending', attempts=0, available_at=now(), locked_until=NULL,
		    last_error='requeued for permanent B2 version purge', updated_at=now()
		WHERE media_outbox.status <> 'processing'
	`)
	return err
}
func (s *server) processOne(parent context.Context)error{
	ctx,c:=context.WithTimeout(parent,30*time.Minute);defer c()
	tx,err:=s.db.Begin(ctx);if err!=nil{return err};defer tx.Rollback(ctx)
	var id int64;var attempts int;var owner,ns,key,op string
	err=tx.QueryRow(ctx,`select o.id,o.attempts,o.owner_id,o.namespace,o.object_key,o.operation from media_outbox o where o.status in ('pending','retry','processing') and o.available_at<=now() and (o.locked_until is null or o.locked_until<now()) and not exists (select 1 from media_outbox active where active.owner_id=o.owner_id and active.namespace=o.namespace and active.object_key=o.object_key and active.status='processing' and active.id<>o.id) order by o.id for update of o skip locked limit 1`).Scan(&id,&attempts,&owner,&ns,&key,&op)
	if errors.Is(err,pgx.ErrNoRows){return nil};if err!=nil{return err}
	_,err=tx.Exec(ctx,`update media_outbox set status='processing',locked_until=now()+interval '35 minutes',attempts=attempts+1 where id=$1`,id);if err!=nil{return err};if err=tx.Commit(ctx);err!=nil{return err};attempts++
	switch op {
	case "verify":
		err = s.verifyPrimary(ctx, owner, ns, key)
	case "replicate":
		if ns == "identity-evidence-private" {
			_, _ = s.db.Exec(ctx, `update media_objects set primary_verified_at=null,replica_verified_at=null,status='uploaded' where owner_id=$1 and namespace=$2 and object_key=$3 and status not in ('delete_pending','deleted')`, owner, ns, key)
			err = s.verifyPrimary(ctx, owner, ns, key)
		} else {
			err = s.replicate(ctx, owner, ns, key)
		}
	case "restore_primary":
		err = s.restorePrimary(ctx, owner, ns, key)
	case "delete":
		err = s.deleteOrigins(ctx, owner, ns, key)
	default:
		err = fmt.Errorf("unknown outbox operation %q", op)
	}
	if err!=nil{
		var objectStatus string
		stateErr:=s.db.QueryRow(ctx,`select status from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`,owner,ns,key).Scan(&objectStatus)
		if stateErr==nil&&(objectStatus=="deleted"||(objectStatus=="delete_pending"&&op!="delete")) { _,cancelErr:=s.db.Exec(ctx,`update media_outbox set status='cancelled',locked_until=null,last_error='superseded by delete',updated_at=now() where id=$1 and status='processing'`,id);return cancelErr }
		var grantWait uploadGrantActiveError
		if errors.As(err,&grantWait) {
			tag,updateErr:=s.db.Exec(ctx,`update media_outbox set status='retry',available_at=$3,locked_until=null,last_error=left($2,1000),updated_at=now() where id=$1 and status='processing'`,id,err.Error(),grantWait.retryAt)
			if updateErr!=nil{return updateErr};if tag.RowsAffected()==0{return nil};return err
		}
		delaySeconds:=mediaRetryDelaySeconds(attempts)
		tag,updateErr:=s.db.Exec(ctx,`update media_outbox set status=case when attempts>=12 then 'dead' else 'retry' end,available_at=now()+make_interval(secs=>$3),locked_until=null,last_error=left($2,1000),updated_at=now() where id=$1 and status='processing'`,id,err.Error(),delaySeconds)
		if updateErr!=nil{return updateErr};if tag.RowsAffected()==0{return nil}
		if op=="replicate"||op=="verify" {
			_,_=s.db.Exec(ctx,`update media_objects set status='replication_failed',updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and status in ('replication_pending','uploaded')`,owner,ns,key)
		} else if op=="restore_primary" {
			// Keep the object quarantined and retryable while attempts remain.
			// Only expose replication_failed after the durable outbox dead-letters it.
			_,_=s.db.Exec(ctx,`update media_objects set status='replication_failed',updated_at=now()
				where owner_id=$1 and namespace=$2 and object_key=$3 and status='restore_pending'
				and exists(select 1 from media_outbox where owner_id=$1 and namespace=$2 and object_key=$3 and operation='restore_primary' and status='dead')`,owner,ns,key)
		}
		return err
	}
	tag,err:=s.db.Exec(ctx,`update media_outbox set status='done',locked_until=null,last_error=null,updated_at=now() where id=$1 and status='processing'`,id);if err!=nil{return err};if tag.RowsAffected()==0{return nil};return nil
}
func (s *server) verifyPrimary(ctx context.Context,owner,ns,key string)error{
	var size int64;var typ,want string
	if err:=s.db.QueryRow(ctx,`select size_bytes,content_type,sha256 from media_objects where owner_id=$1 and namespace=$2 and object_key=$3 and status in ('uploaded','replication_failed') and primary_verified_at is null`,owner,ns,key).Scan(&size,&typ,&want);err!=nil{return err}
	primary, _ := s.storesForNamespace(ns)
	resp,err:=s.signedRequest(ctx,primary,http.MethodGet,key,nil,"");if err!=nil{return err};if resp.StatusCode!=200{resp.Body.Close();return fmt.Errorf("primary object GET status %d",resp.StatusCode)};defer resp.Body.Close()
	if resp.ContentLength>=0&&resp.ContentLength!=size{return errors.New("primary object size mismatch")}
	if got:=strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"),";")[0]);got!=""&&got!=typ{return errors.New("primary object content type mismatch")}
	h:=sha256.New();n,err:=io.Copy(h,io.LimitReader(resp.Body,size+1));if err!=nil{return err};if n!=size||hex.EncodeToString(h.Sum(nil))!=want{return errors.New("primary object size or SHA-256 mismatch")}
	if ns == "identity-evidence-private" {
		item := reconcileItem{owner:owner,namespace:ns,key:key,contentType:typ,size:size,expectedSHA:want}
		if err := s.writeMetadataSidecars(ctx, item); err != nil { return err }
		tag, err := s.db.Exec(ctx, `update media_objects set status='protected',primary_verified_at=now(),replica_verified_at=null,reconciled_at=now(),reconciliation_claimed_at=null,updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and status in ('uploaded','replication_failed') and primary_verified_at is null`, owner, ns, key)
		if err != nil { return err }
		if tag.RowsAffected() != 1 { return errors.New("identity evidence lifecycle changed during B2 verification") }
		return nil
	}
	tx,err:=s.db.Begin(ctx);if err!=nil{return err};defer tx.Rollback(ctx)
	now:=time.Now().UTC()
	tag,err:=tx.Exec(ctx,`update media_objects set status='replication_pending',primary_verified_at=$4,updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and status in ('uploaded','replication_failed') and primary_verified_at is null`,owner,ns,key,now)
	if err==nil&&tag.RowsAffected()!=1{err=errors.New("object lifecycle changed during primary verification")}
	if err==nil{_,err=tx.Exec(ctx,`insert into media_outbox(owner_id,namespace,object_key,operation,status,available_at) values($1,$2,$3,'replicate','pending',now()) on conflict(owner_id,namespace,object_key,operation) do update set status='pending',attempts=0,available_at=now(),locked_until=null,last_error=null`,owner,ns,key)}
	if err==nil{eventID,idErr:=randomID();if idErr!=nil{err=idErr}else{_,err=tx.Exec(ctx,`insert into media_operations(operation_id,owner_id,namespace,object_key,operation,outcome) values($1,$2,$3,$4,'replicate','queued')`,eventID,owner,ns,key)}}
	if err==nil{err=tx.Commit(ctx)};return err
}
func (s *server) replicate(ctx context.Context,owner,ns,key string)error{
	if ns == "identity-evidence-private" { return errors.New("identity evidence must never be replicated to another provider") }
	var size int64;var typ,want string
	if err:=s.db.QueryRow(ctx,`select size_bytes,content_type,sha256 from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`,owner,ns,key).Scan(&size,&typ,&want);err!=nil{return err}
	primary, replica := s.storesForNamespace(ns)
	resp,err:=s.signedRequest(ctx,primary,http.MethodGet,key,nil,"");if err!=nil{return err};if resp.StatusCode!=200{resp.Body.Close();return fmt.Errorf("primary GET status %d",resp.StatusCode)};if resp.ContentLength>=0&&resp.ContentLength!=size{resp.Body.Close();return errors.New("primary object size changed before replication")}
	h:=sha256.New();reader:=&lengthReader{Reader:io.TeeReader(io.LimitReader(resp.Body,size+1),h),n:size}
	put,err:=s.signedRequest(ctx,replica,http.MethodPut,key,reader,typ);closeErr:=resp.Body.Close();if err!=nil{return err};if closeErr!=nil{return closeErr};if put.StatusCode<200||put.StatusCode>=300{put.Body.Close();return fmt.Errorf("replica PUT status %d",put.StatusCode)};put.Body.Close()
	if hex.EncodeToString(h.Sum(nil))!=want{return errors.New("primary object changed during replication")}
	check,err:=s.signedRequest(ctx,replica,http.MethodGet,key,nil,"");if err!=nil{return err};defer check.Body.Close();if check.StatusCode!=200{return fmt.Errorf("replica read-back status %d",check.StatusCode)}
	h2:=sha256.New();n,err:=io.Copy(h2,io.LimitReader(check.Body,size+1));if err!=nil||n!=size||hex.EncodeToString(h2.Sum(nil))!=want{return errors.New("replica read-back SHA-256 or size mismatch")}
	if err:=s.writeMetadataSidecars(ctx,reconcileItem{owner:owner,namespace:ns,key:key,contentType:typ,size:size,expectedSHA:want});err!=nil{return err}
	now:=time.Now().UTC();_,err=s.db.Exec(ctx,`update media_objects set status='protected',replica_verified_at=$4,updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and status in ('replication_pending','replication_failed')`,owner,ns,key,now);return err
}
// restorePrimary rebuilds a missing or corrupt primary only from a replica that
// independently matches the durable size and SHA-256. The object remains restore_pending
// (and therefore unavailable for download) until the restored primary passes a full read-back.
// The outbox's per-object processing lease serializes this with deletion and other repairs.
func (s *server) restorePrimary(ctx context.Context,owner,ns,key string)error{
	if ns == "identity-evidence-private" { return errors.New("identity evidence is B2-only and cannot be restored from another provider") }
	var size int64
	var typ,want,status string
	if err:=s.db.QueryRow(ctx,`select size_bytes,content_type,sha256,status from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`,owner,ns,key).Scan(&size,&typ,&want,&status);err!=nil{return err}
	if status=="delete_pending"||status=="deleted"{return nil}
	if status!="restore_pending"{return fmt.Errorf("primary restore refused for lifecycle state %q",status)}
	item:=reconcileItem{owner:owner,namespace:ns,key:key,contentType:typ,size:size,expectedSHA:want}
	// Verify the source completely before overwriting the primary.
	primary, replica := s.storesForNamespace(ns)
	ok,err:=s.originMatches(ctx,replica,item)
	if err!=nil{return fmt.Errorf("verify replica before primary restore: %w",err)}
	if !ok{return errors.New("replica failed size, content-type or SHA-256 verification; primary restore refused")}
	source,err:=s.signedRequest(ctx,replica,http.MethodGet,key,nil,"")
	if err!=nil{return err}
	if source.StatusCode!=http.StatusOK{source.Body.Close();return fmt.Errorf("replica GET status %d during primary restore",source.StatusCode)}
	if source.ContentLength>=0&&source.ContentLength!=size{source.Body.Close();return errors.New("replica size changed after preflight verification")}
	if got:=strings.TrimSpace(strings.Split(source.Header.Get("Content-Type"),";")[0]);got!=""&&got!=typ{source.Body.Close();return errors.New("replica content type changed after preflight verification")}
	h:=sha256.New()
	reader:=&lengthReader{Reader:io.TeeReader(io.LimitReader(source.Body,size+1),h),n:size}
	put,putErr:=s.signedRequest(ctx,primary,http.MethodPut,key,reader,typ)
	closeErr:=source.Body.Close()
	if putErr!=nil{return putErr}
	if closeErr!=nil{put.Body.Close();return closeErr}
	if put.StatusCode<200||put.StatusCode>=300{put.Body.Close();return fmt.Errorf("primary restore PUT status %d",put.StatusCode)}
	io.Copy(io.Discard,io.LimitReader(put.Body,4096));put.Body.Close()
	if hex.EncodeToString(h.Sum(nil))!=want{return errors.New("replica bytes changed during primary restore; restored object not trusted")}
	// Verify the actual primary by a fresh full GET, not merely the PUT response.
	ok,err=s.originMatches(ctx,primary,item)
	if err!=nil{return fmt.Errorf("primary read-back after restore: %w",err)}
	if !ok{return errors.New("restored primary failed size, content-type or SHA-256 read-back")}
	if err:=s.writeMetadataSidecars(ctx,item);err!=nil{return err}
	tag,err:=s.db.Exec(ctx,`update media_objects set status='protected',primary_verified_at=now(),reconciled_at=now(),reconciliation_claimed_at=null,updated_at=now()
		where owner_id=$1 and namespace=$2 and object_key=$3 and status='restore_pending' and size_bytes=$4 and sha256=$5`,owner,ns,key,size,want)
	if err!=nil{return err}
	if tag.RowsAffected()!=1{return errors.New("object lifecycle or checksum changed before primary restore could be committed")}
	return nil
}

func (s *server) deleteOrigins(ctx context.Context, owner, namespace, key string) error {
	// A presigned PUT remains valid at B2 even after its database grant is
	// consumed or the object is hidden. Wait until every issued URL has expired
	// plus a bounded in-flight upload grace before enumerating versions.
	var uploadURLStillValid bool
	if err := s.db.QueryRow(ctx, `
		select exists(
			select 1 from media_upload_grants
			where owner_id=$1 and namespace=$2 and object_key=$3
			  and expires_at + interval '60 seconds' > now()
		)
	`, owner, namespace, key).Scan(&uploadURLStillValid); err != nil {
		return fmt.Errorf("check outstanding presigned upload grants: %w", err)
	}
	if uploadURLStillValid {
		var retryAt time.Time
		if err := s.db.QueryRow(ctx, `
			select max(expires_at + interval '60 seconds')
			from media_upload_grants where owner_id=$1 and namespace=$2 and object_key=$3
			  and expires_at + interval '60 seconds' > now()
		`, owner, namespace, key).Scan(&retryAt); err != nil {
			return fmt.Errorf("read presigned upload grant expiry: %w", err)
		}
		return uploadGrantActiveError{retryAt: retryAt}
	}
	primary, replica := s.storesForNamespace(namespace)
	if namespace == "identity-evidence-private" {
		if primary.bucket == "" { return errors.New("dedicated B2 identity evidence bucket is not configured") }
		if err := s.deleteAllB2ObjectVersions(ctx, primary, key); err != nil {
			return fmt.Errorf("permanent identity evidence purge: %w", err)
		}
	} else {
		for _, store := range []s3Store{primary, replica} {
			if store.bucket == "" { continue }
			resp, err := s.signedRequest(ctx, store, http.MethodDelete, key, nil, "")
			if err != nil { return err }
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); resp.Body.Close()
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
				return fmt.Errorf("%s delete status %d", store.name, resp.StatusCode)
			}
		}
	}
	if err := s.deleteMetadataSidecars(ctx, namespace, key); err != nil { return err }
	tx, err := s.db.Begin(ctx)
	if err != nil { return fmt.Errorf("begin media deletion finalization: %w", err) }
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `delete from media_upload_grants where owner_id=$1 and namespace=$2 and object_key=$3`, owner, namespace, key); err != nil {
		return fmt.Errorf("remove expired media upload grants: %w", err)
	}
	if namespace == "identity-evidence-private" {
		if _, err := tx.Exec(ctx, `update media_objects set status='deleted',identity_evidence_purged_at=now(),updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3`, owner, namespace, key); err != nil {
			return fmt.Errorf("persist permanently purged identity evidence state: %w", err)
		}
	} else if _, err := tx.Exec(ctx, `update media_objects set status='deleted',updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3`, owner, namespace, key); err != nil {
		return fmt.Errorf("persist deleted media state: %w", err)
	}
	if err := tx.Commit(ctx); err != nil { return fmt.Errorf("commit media deletion finalization: %w", err) }
	return nil
}

type uploadGrantActiveError struct { retryAt time.Time }
func (e uploadGrantActiveError) Error() string {
	return "presigned upload URL remains valid; deletion deferred until " + e.retryAt.UTC().Format(time.RFC3339)
}


func (s *server) signedRequest(ctx context.Context, store s3Store, method, key string, body io.Reader, contentType string) (*http.Response, error) {
	u, err := objectURL(store, key)
	if err != nil { return nil, err }
	return s.signedRequestURL(ctx, store, method, u, body, contentType)
}

func (s *server) signedRequestURL(ctx context.Context, store s3Store, method string, u *url.URL, body io.Reader, contentType string) (*http.Response, error) {
	payload := "UNSIGNED-PAYLOAD"
	date := time.Now().UTC().Format("20060102T150405Z")
	day := date[:8]
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil { return nil, err }
	if sized, ok := body.(interface{ Len64() int64 }); ok { req.ContentLength = sized.Len64() }
	req.Header.Set("X-Amz-Date", date)
	req.Header.Set("X-Amz-Content-Sha256", payload)
	if contentType != "" { req.Header.Set("Content-Type", contentType) }
	canonicalHeaders := "host:" + u.Host + "\nx-amz-content-sha256:" + payload + "\nx-amz-date:" + date + "\n"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	if contentType != "" {
		canonicalHeaders = "content-type:" + contentType + "\n" + canonicalHeaders
		signedHeaders = "content-type;" + signedHeaders
	}
	canonicalQuery := u.Query().Encode()
	canonical := method + "\n" + u.EscapedPath() + "\n" + canonicalQuery + "\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + payload
	scope := day + "/" + store.region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + date + "\n" + scope + "\n" + hashHex([]byte(canonical))
	signature := hex.EncodeToString(hmacSHA(signingKey(store.secret, day, store.region), toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+store.access+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
	return s.http.Do(req)
}

type b2ObjectVersion struct {
	Key string `xml:"Key"`
	VersionID string `xml:"VersionId"`
	DeleteMarker bool
}

type b2VersionListPage struct {
	Versions []b2ObjectVersion `xml:"Version"`
	DeleteMarkers []b2ObjectVersion `xml:"DeleteMarker"`
	IsTruncated bool `xml:"IsTruncated"`
	NextKeyMarker string `xml:"NextKeyMarker"`
	NextVersionIDMarker string `xml:"NextVersionIdMarker"`
}

func (s *server) listB2ObjectVersionsPage(ctx context.Context, store s3Store, prefix, keyMarker, versionIDMarker string) (b2VersionListPage, error) {
	var page b2VersionListPage
	if err := validateStore(store); err != nil { return page, err }
	base, err := url.Parse(strings.TrimRight(store.endpoint, "/"))
	if err != nil { return page, err }
	base.Path = "/" + store.bucket + "/"
	query := url.Values{}
	query.Set("versions", "")
	query.Set("prefix", prefix)
	query.Set("max-keys", "1000")
	if keyMarker != "" { query.Set("key-marker", keyMarker) }
	if versionIDMarker != "" { query.Set("version-id-marker", versionIDMarker) }
	base.RawQuery = query.Encode()
	resp, err := s.signedRequestURL(ctx, store, http.MethodGet, base, nil, "")
	if err != nil { return page, fmt.Errorf("%s list object versions: %w", store.name, err) }
	defer resp.Body.Close()
	const maxXMLBytes = 8 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxXMLBytes+1))
	if err != nil { return page, fmt.Errorf("%s read object version listing: %w", store.name, err) }
	if len(body) > maxXMLBytes { return page, fmt.Errorf("%s object version listing exceeded %d bytes", store.name, maxXMLBytes) }
	if resp.StatusCode != http.StatusOK { return page, fmt.Errorf("%s list object versions status %d: %s", store.name, resp.StatusCode, strings.TrimSpace(string(body))) }
	if err := xml.Unmarshal(body, &page); err != nil { return page, fmt.Errorf("%s parse object version listing: %w", store.name, err) }
	for i := range page.DeleteMarkers { page.DeleteMarkers[i].DeleteMarker = true }
	return page, nil
}

func (s *server) collectB2ObjectVersions(ctx context.Context, store s3Store, key string) ([]b2ObjectVersion, error) {
	if key == "" || strings.ContainsAny(key, "\r\n") { return nil, errors.New("invalid object key for permanent B2 deletion") }
	var versions []b2ObjectVersion
	seenIDs := make(map[string]struct{})
	seenMarkers := make(map[string]struct{})
	keyMarker, versionIDMarker := "", ""
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		page, err := s.listB2ObjectVersionsPage(ctx, store, key, keyMarker, versionIDMarker)
		if err != nil { return nil, err }
		entries := append(append([]b2ObjectVersion{}, page.Versions...), page.DeleteMarkers...)
		for _, entry := range entries {
			if entry.Key != key { continue }
			if entry.VersionID == "" { return nil, errors.New("B2 returned an object version without a version ID") }
			if _, exists := seenIDs[entry.VersionID]; exists { continue }
			seenIDs[entry.VersionID] = struct{}{}
			versions = append(versions, entry)
			if len(versions) > 10000 { return nil, errors.New("identity evidence object exceeds the 10000-version permanent deletion safety limit") }
		}
		if !page.IsTruncated { return versions, nil }
		nextKey, nextVersion := page.NextKeyMarker, page.NextVersionIDMarker
		marker := nextKey + "\x00" + nextVersion
		if nextKey == "" && nextVersion == "" { return nil, errors.New("B2 truncated object version listing omitted pagination markers") }
		if marker == keyMarker+"\x00"+versionIDMarker { return nil, errors.New("B2 object version pagination marker did not advance") }
		if _, exists := seenMarkers[marker]; exists { return nil, errors.New("B2 object version pagination repeated a marker") }
		seenMarkers[marker] = struct{}{}
		keyMarker, versionIDMarker = nextKey, nextVersion
	}
	return nil, errors.New("B2 object version listing exceeded the 100-page safety limit")
}

func (s *server) deleteAllB2ObjectVersions(ctx context.Context, store s3Store, key string) error {
	versions, err := s.collectB2ObjectVersions(ctx, store, key)
	if err != nil { return err }
	for _, version := range versions {
		u, err := objectURL(store, key)
		if err != nil { return err }
		query := url.Values{}
		query.Set("versionId", version.VersionID)
		u.RawQuery = query.Encode()
		resp, err := s.signedRequestURL(ctx, store, http.MethodDelete, u, nil, "")
		if err != nil { return fmt.Errorf("%s permanently delete version %q: %w", store.name, version.VersionID, err) }
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("%s permanently delete version %q status %d", store.name, version.VersionID, resp.StatusCode)
		}
	}
	remaining, err := s.collectB2ObjectVersions(ctx, store, key)
	if err != nil { return fmt.Errorf("%s verify permanent deletion: %w", store.name, err) }
	if len(remaining) != 0 {
		return fmt.Errorf("%s retained %d object versions or delete markers after permanent deletion", store.name, len(remaining))
	}
	return nil
}

func presign(store s3Store,method,key string,ttl time.Duration,contentType string)(string,error){
	u,err:=objectURL(store,key);if err!=nil{return "",err};now:=time.Now().UTC();date:=now.Format("20060102T150405Z");day:=date[:8];scope:=day+"/"+store.region+"/s3/aws4_request";signed:="host";if contentType!=""{signed="content-type;host"}
	q:=u.Query();q.Set("X-Amz-Algorithm","AWS4-HMAC-SHA256");q.Set("X-Amz-Credential",store.access+"/"+scope);q.Set("X-Amz-Date",date);q.Set("X-Amz-Expires",strconv.Itoa(int(ttl.Seconds())));q.Set("X-Amz-SignedHeaders",signed);u.RawQuery=q.Encode()
	headers:="host:"+u.Host+"\n";if contentType!=""{headers="content-type:"+contentType+"\n"+headers}
	canonical:=method+"\n"+u.EscapedPath()+"\n"+u.Query().Encode()+"\n"+headers+"\n"+signed+"\nUNSIGNED-PAYLOAD"
	toSign:="AWS4-HMAC-SHA256\n"+date+"\n"+scope+"\n"+hashHex([]byte(canonical));sig:=hex.EncodeToString(hmacSHA(signingKey(store.secret,day,store.region),toSign));q=u.Query();q.Set("X-Amz-Signature",sig);u.RawQuery=q.Encode();return u.String(),nil
}
type lengthReader struct{io.Reader;n int64}
func(r *lengthReader)Len64()int64{return r.n}
func validateStore(s s3Store)error{u,err:=url.Parse(strings.TrimSpace(s.endpoint));privateHTTP := u != nil && u.Scheme=="http" && (u.Hostname()=="object-storage" || u.Hostname()=="localhost" || u.Hostname()=="127.0.0.1")
	if err!=nil||u.Host==""||(!privateHTTP&&u.Scheme!="https")||u.User!=nil||u.RawQuery!=""||u.Fragment!=""||(u.Path!=""&&u.Path!="/"){return errors.New("S3 endpoint must be HTTPS, or private Docker object-storage HTTP, without credentials, path, query or fragment")};if s.region==""||len(s.region)>64||strings.ContainsAny(s.region,"/\\\r\n")||s.bucket==""||len(s.bucket)>255||strings.ContainsAny(s.bucket,"/\\\r\n")||s.access==""||s.secret==""||strings.ContainsAny(s.access+s.secret,"\r\n"){return errors.New("S3 region, bucket and credentials must be valid non-empty single-line values")};return nil}
func objectURL(s s3Store,key string)(*url.URL,error){
	if err:=validateStore(s);err!=nil{return nil,err};if !validObjectKey(key){return nil,errors.New("invalid object key")};base,err:=url.Parse(strings.TrimRight(s.endpoint,"/"));if err!=nil{return nil,err}
	base.Path="/"+s.bucket+"/"+key;return base,nil
}
func signingKey(secret,day,region string)[]byte{k:=hmacSHA([]byte("AWS4"+secret),day);k=hmacSHA(k,region);k=hmacSHA(k,"s3");return hmacSHA(k,"aws4_request")}
func hmacSHA(key []byte,data string)[]byte{h:=hmac.New(sha256.New,key);_,_=h.Write([]byte(data));return h.Sum(nil)}
func hashHex(b []byte)string{h:=sha256.Sum256(b);return hex.EncodeToString(h[:])}
func validUpload(in uploadRequest) bool {
	if !validIdentity(in.OwnerID) || !validNamespace(in.Namespace) || !validObjectKey(in.Key) || in.SizeBytes < 1 {
		return false
	}
	if in.Namespace == "identity-evidence-private" {
		return validIdentityEvidenceKey(in.Key, in.OwnerID) && identityEvidenceKeyMatchesType(in.Key, in.ContentType) && allowedIdentityEvidenceType(in.ContentType) && in.SizeBytes <= 15<<20
	}
	return validMediaKey(in.Key, in.OwnerID) && allowedMediaType(in.ContentType) && in.SizeBytes <= 5<<30
}
func allowedMediaType(v string) bool {
	switch v { case "image/jpeg", "image/png", "image/webp", "image/gif", "image/avif", "video/mp4", "video/webm": return true }
	return false
}
func allowedIdentityEvidenceType(v string) bool {
	switch v { case "image/jpeg", "image/png", "image/webp", "application/pdf": return true }
	return false
}
func validMediaKey(key, owner string) bool {
	if !validObjectKey(key) || !validIdentity(owner) { return false }
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "users" || parts[1] != owner || len(parts[2]) != 32 || strings.ToLower(parts[2]) != parts[2] { return false }
	_, err := hex.DecodeString(parts[2])
	return err == nil
}
func identityEvidenceKeyMatchesType(key, contentType string) bool {
	parts := strings.Split(key, "/")
	if len(parts) != 4 { return false }
	name := parts[3]
	switch contentType {
	case "image/jpeg": return strings.HasPrefix(name[36:], ".jpg-")
	case "image/png": return strings.HasPrefix(name[36:], ".png-")
	case "image/webp": return strings.HasPrefix(name[36:], ".webp-")
	case "application/pdf": return strings.HasPrefix(name[36:], ".pdf-")
	default: return false
	}
}
func validIdentityEvidenceKey(key, owner string) bool {
	if !validObjectKey(key) || !validIdentity(owner) { return false }
	parts := strings.Split(key, "/")
	if len(parts) != 4 || parts[0] != "identity" || parts[1] != owner || !validUUID(parts[2]) { return false }
	name := parts[3]
	if len(name) < 36+5+1+32 || !validUUID(name[:36]) { return false }
	rest := name[36:]
	ext := ""
	for _, candidate := range []string{".jpg", ".png", ".webp", ".pdf"} {
		if strings.HasPrefix(rest, candidate+"-") { ext = candidate; rest = strings.TrimPrefix(rest, candidate+"-"); break }
	}
	if ext == "" || len(rest) < 32 || len(rest) > 128 { return false }
	for _, r := range rest {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') { return false }
	}
	return true
}
func validUUID(value string) bool {
	if len(value) != 36 { return false }
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 { if r != '-' { return false }; continue }
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') { return false }
	}
	return true
}
func objectLockKey(owner,namespace,key string)string{return fmt.Sprintf("%d:%s:%d:%s:%s",len(owner),owner,len(namespace),namespace,key)}
func validObjectKey(key string)bool{if key==""||len(key)>1024||strings.HasPrefix(key,"/")||strings.ContainsAny(key,"\\\x00\r\n"){return false};for _,part:=range strings.Split(key,"/"){if part==""||part=="."||part==".."{return false}};return true}
func validIdentity(v string)bool{if len(v)<1||len(v)>128{return false};for _,r:=range v{if !(r>='a'&&r<='z'||r>='A'&&r<='Z'||r>='0'&&r<='9'||r=='-'||r=='_'||r=='.'){return false}};return true}
func validNamespace(v string)bool{return v=="media-public"||v=="media-private"||v=="identity-evidence-private"}
func validObjectForNamespace(namespace,key,owner string)bool{if namespace=="identity-evidence-private"{return validIdentityEvidenceKey(key,owner)};return validMediaKey(key,owner)}

func decode(w http.ResponseWriter,r *http.Request,v any)bool{r.Body=http.MaxBytesReader(w,r.Body,maxJSON);defer r.Body.Close();d:=json.NewDecoder(r.Body);d.DisallowUnknownFields();if d.Decode(v)!=nil{return false};var extra any;return d.Decode(&extra)==io.EOF}
func writeErr(w http.ResponseWriter,status int,msg string){writeJSON(w,status,map[string]string{"error":msg})}
func writeJSON(w http.ResponseWriter,status int,v any){w.Header().Set("Content-Type","application/json");w.WriteHeader(status);_=json.NewEncoder(w).Encode(v)}
func method(w http.ResponseWriter,m string){w.Header().Set("Allow",m);writeErr(w,405,"method not allowed")}
func required(k string)string{v:=strings.TrimSpace(os.Getenv(k));if v==""{log.Fatalf("%s is required",k)};return v}
func env(k,d string)string{if v:=strings.TrimSpace(os.Getenv(k));v!=""{return v};return d}
func duration(k string,d time.Duration)time.Duration{v:=env(k,"");if v==""{return d};x,e:=time.ParseDuration(v);if e!=nil||x<=0{log.Fatalf("invalid %s",k)};return x}
func randomID()(string,error){var b [16]byte;if _,e:=rand.Read(b[:]);e!=nil{return "",e};return hex.EncodeToString(b[:]),nil}
func logging(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){start:=time.Now();next.ServeHTTP(w,r);log.Printf("%s %s %s",r.Method,r.URL.Path,time.Since(start))})}

