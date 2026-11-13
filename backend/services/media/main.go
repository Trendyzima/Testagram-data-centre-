package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
	addr string
	primary s3Store
	replica s3Store
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

func main() {
	cfg := config{
		token: required("MEDIA_INTERNAL_TOKEN"), addr: env("MEDIA_ADDR", ":8096"),
		primary: s3Store{name:"backblaze_b2", endpoint:required("B2_S3_ENDPOINT"), region:required("B2_S3_REGION"), bucket:required("B2_S3_BUCKET"), access:required("B2_S3_ACCESS_KEY_ID"), secret:required("B2_S3_SECRET_ACCESS_KEY")},
		replica: s3Store{name:"cloudflare_r2", endpoint:required("CLOUDFLARE_R2_ENDPOINT"), region:env("CLOUDFLARE_R2_REGION","auto"), bucket:required("CLOUDFLARE_R2_BUCKET"), access:required("CLOUDFLARE_R2_ACCESS_KEY_ID"), secret:required("CLOUDFLARE_R2_SECRET_ACCESS_KEY")},
		uploadTTL: duration("MEDIA_UPLOAD_TTL", 45*time.Minute), downloadTTL: duration("MEDIA_DOWNLOAD_TTL", 5*time.Minute),
	}
	if len(cfg.token)<32 { log.Fatal("MEDIA_INTERNAL_TOKEN must be at least 32 characters") }
	if cfg.uploadTTL < time.Minute || cfg.uploadTTL > time.Hour { log.Fatal("MEDIA_UPLOAD_TTL must be between 1 minute and 1 hour") }
	if cfg.downloadTTL < time.Minute || cfg.downloadTTL > 15*time.Minute { log.Fatal("MEDIA_DOWNLOAD_TTL must be between 1 minute and 15 minutes") }
	if err:=validateStore(cfg.primary);err!=nil{log.Fatalf("invalid B2 primary configuration: %v",err)}
	if err:=validateStore(cfg.replica);err!=nil{log.Fatalf("invalid R2 replica configuration: %v",err)}
	db,err:=pgxpool.New(context.Background(),required("MEDIA_DATABASE_URL")); if err!=nil { log.Fatal(err) }; defer db.Close()
	ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second); defer cancel()
	if err=db.Ping(ctx); err!=nil { log.Fatalf("media database unavailable: %v",err) }
	s:=&server{cfg:cfg,db:db,http:&http.Client{Timeout:30*time.Minute,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}}
	mux:=http.NewServeMux()
	mux.HandleFunc("/healthz",s.health); mux.HandleFunc("/readyz",s.ready)
	mux.HandleFunc("/internal/v1/media/upload-url",s.uploadURL)
	mux.HandleFunc("/internal/v1/media/verify",s.verify)
	mux.HandleFunc("/internal/v1/media/status",s.status)
	mux.HandleFunc("/internal/v1/media/download-url",s.downloadURL)
	mux.HandleFunc("/internal/v1/media/delete",s.delete)
	go s.worker(context.Background())
	go s.reconcileWorker(context.Background())
	srv:=&http.Server{Addr:cfg.addr,Handler:logging(mux),ReadHeaderTimeout:5*time.Second,ReadTimeout:15*time.Second,WriteTimeout:30*time.Second,IdleTimeout:60*time.Second}
	log.Printf("dedicated media service listening on %s",cfg.addr)
	log.Fatal(srv.ListenAndServe())
}
func (s *server) health(w http.ResponseWriter,r *http.Request){w.WriteHeader(200);_,_=w.Write([]byte("ok\n"))}
func (s *server) ready(w http.ResponseWriter,r *http.Request){ctx,c:=context.WithTimeout(r.Context(),2*time.Second);defer c();if err:=s.db.Ping(ctx);err!=nil{http.Error(w,"database unavailable",503);return};w.WriteHeader(200);_,_=w.Write([]byte("ready\n"))}
func (s *server) auth(w http.ResponseWriter,r *http.Request)bool{
	authorization:=r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization,"Bearer ") { writeErr(w,401,"unauthorized");return false }
	got:=strings.TrimSpace(strings.TrimPrefix(authorization,"Bearer "))
	if got==""||strings.ContainsAny(got," \t\r\n")||subtle.ConstantTimeCompare([]byte(got),[]byte(s.cfg.token))!=1||r.Header.Get("X-Testagram-API-Version")!=apiVersion { writeErr(w,401,"unauthorized");return false };return true
}
func (s *server) uploadURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { method(w, "POST"); return }
	if !s.auth(w, r) { return }
	var in uploadRequest
	if !decode(w, r, &in) || !validUpload(in) { writeErr(w, 400, "invalid upload request"); return }
	id, err := randomID()
	if err != nil { writeErr(w, 503, "secure upload grant generation failed"); return }
	exp := time.Now().UTC().Add(s.cfg.uploadTTL)
	u, err := presign(s.cfg.primary, http.MethodPut, in.Key, s.cfg.uploadTTL, in.ContentType)
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
	var in verifyRequest;if !decode(w,r,&in)||!validUpload(in.uploadRequest)||len(in.SHA256)!=64{writeErr(w,400,"invalid verification request");return}
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
	if !validIdentity(owner)||!validNamespace(ns)||!validMediaKey(key,owner){writeErr(w,400,"invalid status query");return}
	var o object;err:=s.db.QueryRow(r.Context(),`select owner_id,namespace,object_key,content_type,size_bytes,coalesce(sha256,''),status,primary_verified_at,replica_verified_at from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`,owner,ns,key).Scan(&o.OwnerID,&o.Namespace,&o.Key,&o.ContentType,&o.Size,&o.SHA,&o.Status,&o.PrimaryVerified,&o.ReplicaVerified)
	if errors.Is(err,pgx.ErrNoRows){writeErr(w,404,"object not found");return};if err!=nil{writeErr(w,503,"media state unavailable");return}
	writeJSON(w,200,map[string]any{"status":o.Status,"content_type":o.ContentType,"size_bytes":o.Size,"sha256":o.SHA,"primary_verified_at":o.PrimaryVerified,"replica_verified_at":o.ReplicaVerified})
}
func (s *server) downloadURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { method(w, "POST"); return }
	if !s.auth(w, r) { return }
	var in downloadRequest
	if !decode(w, r, &in) || !validIdentity(in.RequesterID) || !validIdentity(in.OwnerID) || !validNamespace(in.Namespace) || !validMediaKey(in.Key, in.OwnerID) { writeErr(w, 400, "invalid download request"); return }
	if in.Method == "" { in.Method = http.MethodGet }; if in.Method != http.MethodGet && in.Method != http.MethodHead { writeErr(w, 400, "download method must be GET or HEAD"); return }
	var status, contentType string; var size int64
	err := s.db.QueryRow(r.Context(), `select status,size_bytes,content_type from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`, in.OwnerID, in.Namespace, in.Key).Scan(&status, &size, &contentType)
	if err != nil || status != "protected" { writeErr(w, 404, "protected object not found"); return }
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second); defer cancel()
	for _, store := range []s3Store{s.cfg.replica, s.cfg.primary} {
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
	var in deleteRequest;if !decode(w,r,&in)||!validIdentity(in.RequesterID)||!validIdentity(in.OwnerID)||!validNamespace(in.Namespace)||!validMediaKey(in.Key,in.OwnerID){writeErr(w,400,"invalid delete request");return}
	ctx,c:=context.WithTimeout(r.Context(),5*time.Second);defer c()
	tx,err:=s.db.Begin(ctx);if err!=nil{writeErr(w,503,"durable state unavailable");return};defer tx.Rollback(ctx)
	var old string;err=tx.QueryRow(ctx,`select status from media_objects where owner_id=$1 and namespace=$2 and object_key=$3 for update`,in.OwnerID,in.Namespace,in.Key).Scan(&old)
	if errors.Is(err,pgx.ErrNoRows){writeJSON(w,200,map[string]string{"status":"deleted"});return};if err!=nil{writeErr(w,503,"media state unavailable");return}
	if old=="deleted"{writeJSON(w,200,map[string]string{"status":"deleted"});return}
	if old=="delete_pending" { var jobStatus string; if err=tx.QueryRow(ctx,`select status from media_outbox where owner_id=$1 and namespace=$2 and object_key=$3 and operation='delete'`,in.OwnerID,in.Namespace,in.Key).Scan(&jobStatus);err!=nil{writeErr(w,503,"deletion job state unavailable");return};if jobStatus=="dead"{_,err=tx.Exec(ctx,`update media_outbox set status='retry',attempts=0,available_at=now(),locked_until=null,last_error=null,updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and operation='delete' and status='dead'`,in.OwnerID,in.Namespace,in.Key);if err!=nil{writeErr(w,503,"could not retry dead-letter deletion");return};if err=tx.Commit(ctx);err!=nil{writeErr(w,503,"could not persist deletion retry");return}};writeJSON(w,202,map[string]string{"status":"delete_pending"});return }
	_,err=tx.Exec(ctx,`update media_objects set status='delete_pending',updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3`,in.OwnerID,in.Namespace,in.Key)
	if err==nil{_,err=tx.Exec(ctx,`update media_outbox set status='cancelled',locked_until=null,last_error='superseded by delete' where owner_id=$1 and namespace=$2 and object_key=$3 and operation in ('verify','replicate') and status in ('pending','retry')`,in.OwnerID,in.Namespace,in.Key)}
	if err==nil{_,err=tx.Exec(ctx,`insert into media_outbox(owner_id,namespace,object_key,operation,status,available_at) values($1,$2,$3,'delete','pending',now()) on conflict(owner_id,namespace,object_key,operation) do update set status='pending',attempts=0,available_at=now(),last_error=null`,in.OwnerID,in.Namespace,in.Key)}
	if err==nil{eventID,idErr:=randomID();if idErr!=nil{err=idErr}else{_,err=tx.Exec(ctx,`insert into media_operations(operation_id,owner_id,namespace,object_key,operation,outcome) values($1,$2,$3,$4,'delete','queued')`,eventID,in.OwnerID,in.Namespace,in.Key)}}
	if err==nil{err=tx.Commit(ctx)};if err!=nil{writeErr(w,503,"could not queue durable deletion");return}
	writeJSON(w,202,map[string]string{"status":"delete_pending"})
}
func (s *server) worker(ctx context.Context){
	t:=time.NewTicker(3*time.Second);defer t.Stop()
	for{select{case<-ctx.Done():return;case<-t.C:if err:=s.processOne(ctx);err!=nil{log.Printf("media outbox worker: %v",err)}}}
}
func (s *server) processOne(parent context.Context)error{
	ctx,c:=context.WithTimeout(parent,30*time.Minute);defer c()
	tx,err:=s.db.Begin(ctx);if err!=nil{return err};defer tx.Rollback(ctx)
	var id int64;var owner,ns,key,op string
	err=tx.QueryRow(ctx,`select o.id,o.owner_id,o.namespace,o.object_key,o.operation from media_outbox o where o.status in ('pending','retry','processing') and o.available_at<=now() and (o.locked_until is null or o.locked_until<now()) and not exists (select 1 from media_outbox active where active.owner_id=o.owner_id and active.namespace=o.namespace and active.object_key=o.object_key and active.status='processing' and active.id<>o.id) order by o.id for update of o skip locked limit 1`).Scan(&id,&owner,&ns,&key,&op)
	if errors.Is(err,pgx.ErrNoRows){return nil};if err!=nil{return err}
	_,err=tx.Exec(ctx,`update media_outbox set status='processing',locked_until=now()+interval '35 minutes',attempts=attempts+1 where id=$1`,id);if err!=nil{return err};if err=tx.Commit(ctx);err!=nil{return err}
	switch op{case "verify":err=s.verifyPrimary(ctx,owner,ns,key);case "replicate":err=s.replicate(ctx,owner,ns,key);case "restore_primary":err=s.restorePrimary(ctx,owner,ns,key);case "delete":err=s.deleteOrigins(ctx,owner,ns,key);default:err=fmt.Errorf("unknown outbox operation %q",op)}
	if err!=nil{
		var objectStatus string
		stateErr:=s.db.QueryRow(ctx,`select status from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`,owner,ns,key).Scan(&objectStatus)
		if stateErr==nil&&(objectStatus=="delete_pending"||objectStatus=="deleted") { _,cancelErr:=s.db.Exec(ctx,`update media_outbox set status='cancelled',locked_until=null,last_error='superseded by delete',updated_at=now() where id=$1 and status='processing'`,id);return cancelErr }
		tag,updateErr:=s.db.Exec(ctx,`update media_outbox set status=case when attempts>=12 then 'dead' else 'retry' end,available_at=now()+make_interval(secs=>least(3600,power(2,least(attempts,10))::int)),locked_until=null,last_error=left($2,1000),updated_at=now() where id=$1 and status='processing'`,id,err.Error())
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
	resp,err:=s.signedRequest(ctx,s.cfg.primary,http.MethodGet,key,nil,"");if err!=nil{return err};if resp.StatusCode!=200{resp.Body.Close();return fmt.Errorf("primary object GET status %d",resp.StatusCode)};defer resp.Body.Close()
	if resp.ContentLength>=0&&resp.ContentLength!=size{return errors.New("primary object size mismatch")}
	if got:=strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"),";")[0]);got!=""&&got!=typ{return errors.New("primary object content type mismatch")}
	h:=sha256.New();n,err:=io.Copy(h,io.LimitReader(resp.Body,size+1));if err!=nil{return err};if n!=size||hex.EncodeToString(h.Sum(nil))!=want{return errors.New("primary object size or SHA-256 mismatch")}
	tx,err:=s.db.Begin(ctx);if err!=nil{return err};defer tx.Rollback(ctx)
	now:=time.Now().UTC()
	tag,err:=tx.Exec(ctx,`update media_objects set status='replication_pending',primary_verified_at=$4,updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and status in ('uploaded','replication_failed') and primary_verified_at is null`,owner,ns,key,now)
	if err==nil&&tag.RowsAffected()!=1{err=errors.New("object lifecycle changed during primary verification")}
	if err==nil{_,err=tx.Exec(ctx,`insert into media_outbox(owner_id,namespace,object_key,operation,status,available_at) values($1,$2,$3,'replicate','pending',now()) on conflict(owner_id,namespace,object_key,operation) do update set status='pending',attempts=0,available_at=now(),locked_until=null,last_error=null`,owner,ns,key)}
	if err==nil{eventID,idErr:=randomID();if idErr!=nil{err=idErr}else{_,err=tx.Exec(ctx,`insert into media_operations(operation_id,owner_id,namespace,object_key,operation,outcome) values($1,$2,$3,$4,'replicate','queued')`,eventID,owner,ns,key)}}
	if err==nil{err=tx.Commit(ctx)};return err
}
func (s *server) replicate(ctx context.Context,owner,ns,key string)error{
	var size int64;var typ,want string
	if err:=s.db.QueryRow(ctx,`select size_bytes,content_type,sha256 from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`,owner,ns,key).Scan(&size,&typ,&want);err!=nil{return err}
	resp,err:=s.signedRequest(ctx,s.cfg.primary,http.MethodGet,key,nil,"");if err!=nil{return err};if resp.StatusCode!=200{resp.Body.Close();return fmt.Errorf("primary GET status %d",resp.StatusCode)};if resp.ContentLength>=0&&resp.ContentLength!=size{resp.Body.Close();return errors.New("primary object size changed before replication")}
	h:=sha256.New();reader:=&lengthReader{Reader:io.TeeReader(io.LimitReader(resp.Body,size+1),h),n:size}
	put,err:=s.signedRequest(ctx,s.cfg.replica,http.MethodPut,key,reader,typ);closeErr:=resp.Body.Close();if err!=nil{return err};if closeErr!=nil{return closeErr};if put.StatusCode<200||put.StatusCode>=300{put.Body.Close();return fmt.Errorf("replica PUT status %d",put.StatusCode)};put.Body.Close()
	if hex.EncodeToString(h.Sum(nil))!=want{return errors.New("primary object changed during replication")}
	check,err:=s.signedRequest(ctx,s.cfg.replica,http.MethodGet,key,nil,"");if err!=nil{return err};defer check.Body.Close();if check.StatusCode!=200{return fmt.Errorf("replica read-back status %d",check.StatusCode)}
	h2:=sha256.New();n,err:=io.Copy(h2,io.LimitReader(check.Body,size+1));if err!=nil||n!=size||hex.EncodeToString(h2.Sum(nil))!=want{return errors.New("replica read-back SHA-256 or size mismatch")}
	now:=time.Now().UTC();_,err=s.db.Exec(ctx,`update media_objects set status='protected',replica_verified_at=$4,updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and status in ('replication_pending','replication_failed')`,owner,ns,key,now);return err
}
// restorePrimary rebuilds a missing or corrupt primary only from a replica that
// independently matches the durable size and SHA-256. The object remains restore_pending
// (and therefore unavailable for download) until the restored primary passes a full read-back.
// The outbox's per-object processing lease serializes this with deletion and other repairs.
func (s *server) restorePrimary(ctx context.Context,owner,ns,key string)error{
	var size int64
	var typ,want,status string
	if err:=s.db.QueryRow(ctx,`select size_bytes,content_type,sha256,status from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`,owner,ns,key).Scan(&size,&typ,&want,&status);err!=nil{return err}
	if status=="delete_pending"||status=="deleted"{return nil}
	if status!="restore_pending"{return fmt.Errorf("primary restore refused for lifecycle state %q",status)}
	item:=reconcileItem{owner:owner,namespace:ns,key:key,contentType:typ,size:size,expectedSHA:want}
	// Verify the source completely before overwriting the primary.
	ok,err:=s.originMatches(ctx,s.cfg.replica,item)
	if err!=nil{return fmt.Errorf("verify replica before primary restore: %w",err)}
	if !ok{return errors.New("replica failed size, content-type or SHA-256 verification; primary restore refused")}
	source,err:=s.signedRequest(ctx,s.cfg.replica,http.MethodGet,key,nil,"")
	if err!=nil{return err}
	if source.StatusCode!=http.StatusOK{source.Body.Close();return fmt.Errorf("replica GET status %d during primary restore",source.StatusCode)}
	if source.ContentLength>=0&&source.ContentLength!=size{source.Body.Close();return errors.New("replica size changed after preflight verification")}
	if got:=strings.TrimSpace(strings.Split(source.Header.Get("Content-Type"),";")[0]);got!=""&&got!=typ{source.Body.Close();return errors.New("replica content type changed after preflight verification")}
	h:=sha256.New()
	reader:=&lengthReader{Reader:io.TeeReader(io.LimitReader(source.Body,size+1),h),n:size}
	put,putErr:=s.signedRequest(ctx,s.cfg.primary,http.MethodPut,key,reader,typ)
	closeErr:=source.Body.Close()
	if putErr!=nil{return putErr}
	if closeErr!=nil{put.Body.Close();return closeErr}
	if put.StatusCode<200||put.StatusCode>=300{put.Body.Close();return fmt.Errorf("primary restore PUT status %d",put.StatusCode)}
	io.Copy(io.Discard,io.LimitReader(put.Body,4096));put.Body.Close()
	if hex.EncodeToString(h.Sum(nil))!=want{return errors.New("replica bytes changed during primary restore; restored object not trusted")}
	// Verify the actual primary by a fresh full GET, not merely the PUT response.
	ok,err=s.originMatches(ctx,s.cfg.primary,item)
	if err!=nil{return fmt.Errorf("primary read-back after restore: %w",err)}
	if !ok{return errors.New("restored primary failed size, content-type or SHA-256 read-back")}
	tag,err:=s.db.Exec(ctx,`update media_objects set status='protected',primary_verified_at=now(),reconciled_at=now(),reconciliation_claimed_at=null,updated_at=now()
		where owner_id=$1 and namespace=$2 and object_key=$3 and status='restore_pending' and size_bytes=$4 and sha256=$5`,owner,ns,key,size,want)
	if err!=nil{return err}
	if tag.RowsAffected()!=1{return errors.New("object lifecycle or checksum changed before primary restore could be committed")}
	return nil
}

func (s *server) deleteOrigins(ctx context.Context,owner,ns,key string)error{
	for _,store:=range []s3Store{s.cfg.primary,s.cfg.replica}{resp,err:=s.signedRequest(ctx,store,http.MethodDelete,key,nil,"");if err!=nil{return err};io.Copy(io.Discard,io.LimitReader(resp.Body,4096));resp.Body.Close();if resp.StatusCode!=200&&resp.StatusCode!=204&&resp.StatusCode!=404{return fmt.Errorf("%s delete status %d",store.name,resp.StatusCode)}}
	_,err:=s.db.Exec(ctx,`update media_objects set status='deleted',updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3`,owner,ns,key);return err
}
func (s *server) signedRequest(ctx context.Context,store s3Store,method,key string,body io.Reader,contentType string)(*http.Response,error){
	u,err:=objectURL(store,key);if err!=nil{return nil,err};payload:="UNSIGNED-PAYLOAD";date:=time.Now().UTC().Format("20060102T150405Z");day:=date[:8]
	req,err:=http.NewRequestWithContext(ctx,method,u.String(),body);if err!=nil{return nil,err};if sized,ok:=body.(interface{Len64() int64});ok{req.ContentLength=sized.Len64()}
	req.Header.Set("X-Amz-Date",date);req.Header.Set("X-Amz-Content-Sha256",payload);if contentType!=""{req.Header.Set("Content-Type",contentType)}
	canonicalHeaders:="host:"+u.Host+"\nx-amz-content-sha256:"+payload+"\nx-amz-date:"+date+"\n";signed:="host;x-amz-content-sha256;x-amz-date"
	if contentType!=""{canonicalHeaders="content-type:"+contentType+"\n"+canonicalHeaders;signed="content-type;"+signed}
	canonical:=method+"\n"+u.EscapedPath()+"\n\n"+canonicalHeaders+"\n"+signed+"\n"+payload
	scope:=day+"/"+store.region+"/s3/aws4_request";toSign:="AWS4-HMAC-SHA256\n"+date+"\n"+scope+"\n"+hashHex([]byte(canonical))
	sig:=hex.EncodeToString(hmacSHA(signingKey(store.secret,day,store.region),toSign))
	req.Header.Set("Authorization","AWS4-HMAC-SHA256 Credential="+store.access+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
	return s.http.Do(req)
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
func validateStore(s s3Store)error{u,err:=url.Parse(strings.TrimSpace(s.endpoint));if err!=nil||u.Scheme!="https"||u.Host==""||u.User!=nil||u.RawQuery!=""||u.Fragment!=""||(u.Path!=""&&u.Path!="/"){return errors.New("S3 endpoint must be an HTTPS origin without credentials, path, query or fragment")};if s.region==""||len(s.region)>64||strings.ContainsAny(s.region,"/\\\r\n")||s.bucket==""||len(s.bucket)>255||strings.ContainsAny(s.bucket,"/\\\r\n")||s.access==""||s.secret==""||strings.ContainsAny(s.access+s.secret,"\r\n"){return errors.New("S3 region, bucket and credentials must be valid non-empty single-line values")};return nil}
func objectURL(s s3Store,key string)(*url.URL,error){
	if err:=validateStore(s);err!=nil{return nil,err};if !validObjectKey(key){return nil,errors.New("invalid object key")};base,err:=url.Parse(strings.TrimRight(s.endpoint,"/"));if err!=nil{return nil,err}
	base.Path="/"+s.bucket+"/"+key;return base,nil
}
func signingKey(secret,day,region string)[]byte{k:=hmacSHA([]byte("AWS4"+secret),day);k=hmacSHA(k,region);k=hmacSHA(k,"s3");return hmacSHA(k,"aws4_request")}
func hmacSHA(key []byte,data string)[]byte{h:=hmac.New(sha256.New,key);_,_=h.Write([]byte(data));return h.Sum(nil)}
func hashHex(b []byte)string{h:=sha256.Sum256(b);return hex.EncodeToString(h[:])}
func validUpload(in uploadRequest)bool{return validIdentity(in.OwnerID)&&validNamespace(in.Namespace)&&validMediaKey(in.Key,in.OwnerID)&&allowedMediaType(in.ContentType)&&in.SizeBytes>0&&in.SizeBytes<=5<<30}
func allowedMediaType(v string)bool{switch v{case "image/jpeg","image/png","image/webp","image/gif","image/avif","video/mp4","video/webm":return true};return false}
func validMediaKey(key,owner string)bool{if !validObjectKey(key)||!validIdentity(owner){return false};parts:=strings.Split(key,"/");if len(parts)!=3||parts[0]!="users"||parts[1]!=owner||len(parts[2])!=32||strings.ToLower(parts[2])!=parts[2]{return false};_,err:=hex.DecodeString(parts[2]);return err==nil}
func objectLockKey(owner,namespace,key string)string{return fmt.Sprintf("%d:%s:%d:%s:%s",len(owner),owner,len(namespace),namespace,key)}
func validObjectKey(key string)bool{if key==""||len(key)>1024||strings.HasPrefix(key,"/")||strings.ContainsAny(key,"\\\x00\r\n"){return false};for _,part:=range strings.Split(key,"/"){if part==""||part=="."||part==".."{return false}};return true}
func validIdentity(v string)bool{if len(v)<1||len(v)>128{return false};for _,r:=range v{if !(r>='a'&&r<='z'||r>='A'&&r<='Z'||r>='0'&&r<='9'||r=='-'||r=='_'||r=='.'){return false}};return true}
func validNamespace(v string)bool{return v=="media-public"||v=="media-private"}
func decode(w http.ResponseWriter,r *http.Request,v any)bool{r.Body=http.MaxBytesReader(w,r.Body,maxJSON);defer r.Body.Close();d:=json.NewDecoder(r.Body);d.DisallowUnknownFields();if d.Decode(v)!=nil{return false};var extra any;return d.Decode(&extra)==io.EOF}
func writeErr(w http.ResponseWriter,status int,msg string){writeJSON(w,status,map[string]string{"error":msg})}
func writeJSON(w http.ResponseWriter,status int,v any){w.Header().Set("Content-Type","application/json");w.WriteHeader(status);_=json.NewEncoder(w).Encode(v)}
func method(w http.ResponseWriter,m string){w.Header().Set("Allow",m);writeErr(w,405,"method not allowed")}
func required(k string)string{v:=strings.TrimSpace(os.Getenv(k));if v==""{log.Fatalf("%s is required",k)};return v}
func env(k,d string)string{if v:=strings.TrimSpace(os.Getenv(k));v!=""{return v};return d}
func duration(k string,d time.Duration)time.Duration{v:=env(k,"");if v==""{return d};x,e:=time.ParseDuration(v);if e!=nil||x<=0{log.Fatalf("invalid %s",k)};return x}
func randomID()(string,error){var b [16]byte;if _,e:=rand.Read(b[:]);e!=nil{return "",e};return hex.EncodeToString(b[:]),nil}
func logging(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){start:=time.Now();next.ServeHTTP(w,r);log.Printf("%s %s %s",r.Method,r.URL.Path,time.Since(start))})}

