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
type downloadRequest struct { RequesterID string `json:"requester_id"`; OwnerID string `json:"owner_id"`; Namespace string `json:"namespace"`; Key string `json:"key"` }
type deleteRequest = downloadRequest
type object struct { OwnerID string; Namespace string; Key string; ContentType string; Size int64; SHA string; Status string; PrimaryVerified *time.Time; ReplicaVerified *time.Time }

func main() {
	cfg := config{
		token: required("MEDIA_INTERNAL_TOKEN"), addr: env("MEDIA_ADDR", ":8096"),
		primary: s3Store{name:"backblaze_b2", endpoint:required("B2_S3_ENDPOINT"), region:required("B2_S3_REGION"), bucket:required("B2_S3_BUCKET"), access:required("B2_S3_ACCESS_KEY_ID"), secret:required("B2_S3_SECRET_ACCESS_KEY")},
		replica: s3Store{name:"cloudflare_r2", endpoint:required("CLOUDFLARE_R2_ENDPOINT"), region:env("CLOUDFLARE_R2_REGION","auto"), bucket:required("CLOUDFLARE_R2_BUCKET"), access:required("CLOUDFLARE_R2_ACCESS_KEY_ID"), secret:required("CLOUDFLARE_R2_SECRET_ACCESS_KEY")},
		uploadTTL: duration("MEDIA_UPLOAD_TTL", 10*time.Minute), downloadTTL: duration("MEDIA_DOWNLOAD_TTL", 5*time.Minute),
	}
	if len(cfg.token)<32 { log.Fatal("MEDIA_INTERNAL_TOKEN must be at least 32 characters") }
	db,err:=pgxpool.New(context.Background(),required("MEDIA_DATABASE_URL")); if err!=nil { log.Fatal(err) }; defer db.Close()
	ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second); defer cancel()
	if err=db.Ping(ctx); err!=nil { log.Fatalf("media database unavailable: %v",err) }
	s:=&server{cfg:cfg,db:db,http:&http.Client{Timeout:90*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}}
	mux:=http.NewServeMux()
	mux.HandleFunc("/healthz",s.health); mux.HandleFunc("/readyz",s.ready)
	mux.HandleFunc("/internal/v1/media/upload-url",s.uploadURL)
	mux.HandleFunc("/internal/v1/media/verify",s.verify)
	mux.HandleFunc("/internal/v1/media/status",s.status)
	mux.HandleFunc("/internal/v1/media/download-url",s.downloadURL)
	mux.HandleFunc("/internal/v1/media/delete",s.delete)
	go s.worker(context.Background())
	srv:=&http.Server{Addr:cfg.addr,Handler:logging(mux),ReadHeaderTimeout:5*time.Second,ReadTimeout:15*time.Second,WriteTimeout:30*time.Second,IdleTimeout:60*time.Second}
	log.Printf("dedicated media service listening on %s",cfg.addr)
	log.Fatal(srv.ListenAndServe())
}
func (s *server) health(w http.ResponseWriter,r *http.Request){w.WriteHeader(200);_,_=w.Write([]byte("ok\n"))}
func (s *server) ready(w http.ResponseWriter,r *http.Request){ctx,c:=context.WithTimeout(r.Context(),2*time.Second);defer c();if err:=s.db.Ping(ctx);err!=nil{http.Error(w,"database unavailable",503);return};w.WriteHeader(200);_,_=w.Write([]byte("ready\n"))}
func (s *server) auth(w http.ResponseWriter,r *http.Request)bool{
	got:=strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"),"Bearer "))
	if got==""||subtle.ConstantTimeCompare([]byte(got),[]byte(s.cfg.token))!=1||r.Header.Get("X-Testagram-API-Version")!=apiVersion { writeErr(w,401,"unauthorized");return false };return true
}
func (s *server) uploadURL(w http.ResponseWriter,r *http.Request){
	if r.Method!=http.MethodPost{method(w,"POST");return};if !s.auth(w,r){return}
	var in uploadRequest;if !decode(w,r,&in)||!validUpload(in){writeErr(w,400,"invalid upload request");return}
	ctx,c:=context.WithTimeout(r.Context(),5*time.Second);defer c()
	exp:=time.Now().UTC().Add(s.cfg.uploadTTL)
	tag,err:=s.db.Exec(ctx,`insert into media_objects(owner_id,namespace,object_key,content_type,size_bytes,status,updated_at) values($1,$2,$3,$4,$5,'upload_pending',now()) on conflict(owner_id,namespace,object_key) do update set content_type=excluded.content_type,size_bytes=excluded.size_bytes,status='upload_pending',sha256=null,primary_verified_at=null,replica_verified_at=null,updated_at=now() where media_objects.status in ('upload_pending','deleted')`,in.OwnerID,in.Namespace,in.Key,in.ContentType,in.SizeBytes)
	if err!=nil||tag.RowsAffected()!=1{writeErr(w,409,"object key already has an active lifecycle");return}
	id:=randomID()
	_,err=s.db.Exec(ctx,`insert into media_upload_grants(id,owner_id,namespace,object_key,max_bytes,expires_at) values($1,$2,$3,$4,$5,$6)`,id,in.OwnerID,in.Namespace,in.Key,in.SizeBytes,exp)
	if err!=nil{writeErr(w,503,"could not persist upload grant");return}
	u,err:=presign(s.cfg.primary,http.MethodPut,in.Key,s.cfg.uploadTTL,in.ContentType)
	if err!=nil{writeErr(w,503,"primary storage signing failed");return}
	writeJSON(w,201,map[string]any{"intent_id":id,"upload_url":u,"expires_at":exp,"max_bytes":in.SizeBytes,"primary":"backblaze_b2"})
}
func (s *server) verify(w http.ResponseWriter,r *http.Request){
	if r.Method!=http.MethodPost{method(w,"POST");return};if !s.auth(w,r){return}
	var in verifyRequest;if !decode(w,r,&in)||!validUpload(in.uploadRequest)||len(in.SHA256)!=64{writeErr(w,400,"invalid verification request");return}
	want,err:=hex.DecodeString(in.SHA256);if err!=nil||len(want)!=32{writeErr(w,400,"invalid SHA-256");return}
	ctx,c:=context.WithTimeout(r.Context(),90*time.Second);defer c()
	var grant bool
	err=s.db.QueryRow(ctx,`select exists(select 1 from media_upload_grants where owner_id=$1 and namespace=$2 and object_key=$3 and max_bytes=$4 and expires_at>now() and consumed_at is null)`,in.OwnerID,in.Namespace,in.Key,in.SizeBytes).Scan(&grant)
	if err!=nil||!grant{writeErr(w,409,"no active upload grant");return}
	resp,err:=s.signedRequest(ctx,s.cfg.primary,http.MethodGet,in.Key,nil,"")
	if err!=nil{writeErr(w,503,"primary storage read failed");return};defer resp.Body.Close()
	if resp.StatusCode!=200{writeErr(w,404,"uploaded object not found");return}
	if resp.ContentLength>=0&&resp.ContentLength!=in.SizeBytes{writeErr(w,409,"object size mismatch");return}
	if got:=strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"),";")[0]);got!=""&&got!=in.ContentType{writeErr(w,409,"object content type mismatch");return}
	h:=sha256.New();n,copyErr:=io.Copy(h,io.LimitReader(resp.Body,in.SizeBytes+1));if copyErr!=nil||n!=in.SizeBytes||subtle.ConstantTimeCompare(h.Sum(nil),want)!=1{writeErr(w,409,"object size or SHA-256 mismatch");return}
	tx,err:=s.db.Begin(ctx);if err!=nil{writeErr(w,503,"durable state unavailable");return};defer tx.Rollback(ctx)
	now:=time.Now().UTC()
	tag,consumeErr:=tx.Exec(ctx,`update media_upload_grants set consumed_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and max_bytes=$4 and expires_at>now() and consumed_at is null`,in.OwnerID,in.Namespace,in.Key,in.SizeBytes)
	if consumeErr!=nil||tag.RowsAffected()!=1{writeErr(w,409,"upload grant already consumed");return}
	_,err=tx.Exec(ctx,`update media_objects set sha256=$4,status='replication_pending',primary_verified_at=$5,updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and size_bytes=$6`,in.OwnerID,in.Namespace,in.Key,strings.ToLower(in.SHA256),now,in.SizeBytes)
	if err==nil{_,err=tx.Exec(ctx,`insert into media_outbox(owner_id,namespace,object_key,operation,status,available_at) values($1,$2,$3,'replicate','pending',now()) on conflict(owner_id,namespace,object_key,operation) do update set status='pending',attempts=0,available_at=now(),last_error=null`,in.OwnerID,in.Namespace,in.Key)}
	if err==nil{err=tx.Commit(ctx)};if err!=nil{writeErr(w,503,"could not persist verification and replication job");return}
	writeJSON(w,202,map[string]string{"status":"replication_pending","sha256":strings.ToLower(in.SHA256)})
}
func (s *server) status(w http.ResponseWriter,r *http.Request){
	if r.Method!=http.MethodGet{method(w,"GET");return};if !s.auth(w,r){return}
	owner,ns,key:=r.URL.Query().Get("owner_id"),r.URL.Query().Get("namespace"),r.URL.Query().Get("key")
	if owner==""||!validNamespace(ns)||key==""||len(key)>1024{writeErr(w,400,"invalid status query");return}
	var o object;err:=s.db.QueryRow(r.Context(),`select owner_id,namespace,object_key,content_type,size_bytes,coalesce(sha256,''),status,primary_verified_at,replica_verified_at from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`,owner,ns,key).Scan(&o.OwnerID,&o.Namespace,&o.Key,&o.ContentType,&o.Size,&o.SHA,&o.Status,&o.PrimaryVerified,&o.ReplicaVerified)
	if errors.Is(err,pgx.ErrNoRows){writeErr(w,404,"object not found");return};if err!=nil{writeErr(w,503,"media state unavailable");return}
	writeJSON(w,200,map[string]any{"status":o.Status,"content_type":o.ContentType,"size_bytes":o.Size,"sha256":o.SHA,"primary_verified_at":o.PrimaryVerified,"replica_verified_at":o.ReplicaVerified})
}
func (s *server) downloadURL(w http.ResponseWriter,r *http.Request){
	if r.Method!=http.MethodPost{method(w,"POST");return};if !s.auth(w,r){return}
	var in downloadRequest;if !decode(w,r,&in)||!validIdentity(in.RequesterID)||!validIdentity(in.OwnerID)||!validNamespace(in.Namespace)||in.Key==""{writeErr(w,400,"invalid download request");return}
	var status string;err:=s.db.QueryRow(r.Context(),`select status from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`,in.OwnerID,in.Namespace,in.Key).Scan(&status)
	if err!=nil||status!="protected"{writeErr(w,404,"protected object not found");return}
	u,err:=presign(s.cfg.replica,http.MethodGet,in.Key,s.cfg.downloadTTL,"");provider:=s.cfg.replica.name
	if err!=nil{u,err=presign(s.cfg.primary,http.MethodGet,in.Key,s.cfg.downloadTTL,"");provider=s.cfg.primary.name}
	if err!=nil{writeErr(w,503,"no verified origin can be signed");return}
	writeJSON(w,200,map[string]string{"url":u,"provider":provider})
}
func (s *server) delete(w http.ResponseWriter,r *http.Request){
	if r.Method!=http.MethodPost{method(w,"POST");return};if !s.auth(w,r){return}
	var in deleteRequest;if !decode(w,r,&in)||!validIdentity(in.RequesterID)||!validIdentity(in.OwnerID)||!validNamespace(in.Namespace)||in.Key==""{writeErr(w,400,"invalid delete request");return}
	ctx,c:=context.WithTimeout(r.Context(),5*time.Second);defer c()
	tx,err:=s.db.Begin(ctx);if err!=nil{writeErr(w,503,"durable state unavailable");return};defer tx.Rollback(ctx)
	var old string;err=tx.QueryRow(ctx,`select status from media_objects where owner_id=$1 and namespace=$2 and object_key=$3 for update`,in.OwnerID,in.Namespace,in.Key).Scan(&old)
	if errors.Is(err,pgx.ErrNoRows){writeJSON(w,200,map[string]string{"status":"deleted"});return};if err!=nil{writeErr(w,503,"media state unavailable");return}
	if old=="deleted"{writeJSON(w,200,map[string]string{"status":"deleted"});return}
	_,err=tx.Exec(ctx,`update media_objects set status='delete_pending',updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3`,in.OwnerID,in.Namespace,in.Key)
	if err==nil{_,err=tx.Exec(ctx,`insert into media_outbox(owner_id,namespace,object_key,operation,status,available_at) values($1,$2,$3,'delete','pending',now()) on conflict(owner_id,namespace,object_key,operation) do update set status='pending',attempts=0,available_at=now(),last_error=null`,in.OwnerID,in.Namespace,in.Key)}
	if err==nil{err=tx.Commit(ctx)};if err!=nil{writeErr(w,503,"could not queue durable deletion");return}
	writeJSON(w,202,map[string]string{"status":"delete_pending"})
}
func (s *server) worker(ctx context.Context){
	t:=time.NewTicker(3*time.Second);defer t.Stop()
	for{select{case<-ctx.Done():return;case<-t.C:if err:=s.processOne(ctx);err!=nil{log.Printf("media outbox worker: %v",err)}}}
}
func (s *server) processOne(parent context.Context)error{
	ctx,c:=context.WithTimeout(parent,2*time.Minute);defer c()
	tx,err:=s.db.Begin(ctx);if err!=nil{return err};defer tx.Rollback(ctx)
	var id int64;var owner,ns,key,op string
	err=tx.QueryRow(ctx,`select id,owner_id,namespace,object_key,operation from media_outbox where status in ('pending','retry','processing') and available_at<=now() and (locked_until is null or locked_until<now()) order by id for update skip locked limit 1`).Scan(&id,&owner,&ns,&key,&op)
	if errors.Is(err,pgx.ErrNoRows){return nil};if err!=nil{return err}
	_,err=tx.Exec(ctx,`update media_outbox set status='processing',locked_until=now()+interval '3 minutes',attempts=attempts+1 where id=$1`,id);if err!=nil{return err};if err=tx.Commit(ctx);err!=nil{return err}
	if op=="replicate"{err=s.replicate(ctx,owner,ns,key)}else if op=="delete"{err=s.deleteOrigins(ctx,owner,ns,key)}else{err=fmt.Errorf("unknown outbox operation %q",op)}
	if err!=nil{
		_,_ = s.db.Exec(ctx,`update media_outbox set status=case when attempts>=12 then 'dead' else 'retry' end,available_at=now()+make_interval(secs=>least(3600,power(2,least(attempts,10))::int)),locked_until=null,last_error=left($2,1000),updated_at=now() where id=$1`,id,err.Error())
		if op=="replicate"{_,_=s.db.Exec(ctx,`update media_objects set status='replication_failed',updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and status<>'delete_pending'`,owner,ns,key)}
		return err
	}
	_,err=s.db.Exec(ctx,`update media_outbox set status='done',locked_until=null,last_error=null,updated_at=now() where id=$1`,id);return err
}
func (s *server) replicate(ctx context.Context,owner,ns,key string)error{
	var size int64;var typ,want string
	if err:=s.db.QueryRow(ctx,`select size_bytes,content_type,sha256 from media_objects where owner_id=$1 and namespace=$2 and object_key=$3`,owner,ns,key).Scan(&size,&typ,&want);err!=nil{return err}
	resp,err:=s.signedRequest(ctx,s.cfg.primary,http.MethodGet,key,nil,"");if err!=nil{return err};if resp.StatusCode!=200{resp.Body.Close();return fmt.Errorf("primary GET status %d",resp.StatusCode)}
	h:=sha256.New();reader:=&lengthReader{Reader:io.TeeReader(io.LimitReader(resp.Body,size+1),h),n:size}
	put,err:=s.signedRequest(ctx,s.cfg.replica,http.MethodPut,key,reader,typ);closeErr:=resp.Body.Close();if err!=nil{return err};if closeErr!=nil{return closeErr};if put.StatusCode<200||put.StatusCode>=300{put.Body.Close();return fmt.Errorf("replica PUT status %d",put.StatusCode)};put.Body.Close()
	if hex.EncodeToString(h.Sum(nil))!=want{return errors.New("primary object changed during replication")}
	check,err:=s.signedRequest(ctx,s.cfg.replica,http.MethodGet,key,nil,"");if err!=nil{return err};defer check.Body.Close();if check.StatusCode!=200{return fmt.Errorf("replica read-back status %d",check.StatusCode)}
	h2:=sha256.New();n,err:=io.Copy(h2,io.LimitReader(check.Body,size+1));if err!=nil||n!=size||hex.EncodeToString(h2.Sum(nil))!=want{return errors.New("replica read-back SHA-256 or size mismatch")}
	now:=time.Now().UTC();_,err=s.db.Exec(ctx,`update media_objects set status='protected',replica_verified_at=$4,updated_at=now() where owner_id=$1 and namespace=$2 and object_key=$3 and status in ('replication_pending','replication_failed')`,owner,ns,key,now);return err
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
func objectURL(s s3Store,key string)(*url.URL,error){
	base,err:=url.Parse(strings.TrimRight(s.endpoint,"/"));if err!=nil||base.Host==""||(base.Scheme!="https"&&base.Scheme!="http")||base.User!=nil{return nil,errors.New("invalid S3 endpoint")}
	base.Path=strings.TrimRight(base.Path,"/")+"/"+s.bucket+"/"+strings.TrimLeft(key,"/");return base,nil
}
func signingKey(secret,day,region string)[]byte{k:=hmacSHA([]byte("AWS4"+secret),day);k=hmacSHA(k,region);k=hmacSHA(k,"s3");return hmacSHA(k,"aws4_request")}
func hmacSHA(key []byte,data string)[]byte{h:=hmac.New(sha256.New,key);_,_=h.Write([]byte(data));return h.Sum(nil)}
func hashHex(b []byte)string{h:=sha256.Sum256(b);return hex.EncodeToString(h[:])}
func validUpload(in uploadRequest)bool{if !validIdentity(in.OwnerID)||!validNamespace(in.Namespace)||in.Key==""||len(in.Key)>1024||strings.HasPrefix(in.Key,"/")||strings.ContainsAny(in.Key,"\\\x00\r\n")||in.ContentType==""||len(in.ContentType)>160||strings.ContainsAny(in.ContentType,"\r\n")||in.SizeBytes<=0||in.SizeBytes>1<<40{return false};for _,part:=range strings.Split(in.Key,"/"){if part=="."||part==".."{return false}};return true}
func validIdentity(v string)bool{return strings.TrimSpace(v)!=""&&len(v)<=128&&!strings.ContainsAny(v,"\x00\r\n")}
func validNamespace(v string)bool{return v=="media-public"||v=="media-private"}
func decode(w http.ResponseWriter,r *http.Request,v any)bool{r.Body=http.MaxBytesReader(w,r.Body,maxJSON);defer r.Body.Close();d:=json.NewDecoder(r.Body);d.DisallowUnknownFields();if d.Decode(v)!=nil{return false};var extra any;return d.Decode(&extra)==io.EOF}
func writeErr(w http.ResponseWriter,status int,msg string){writeJSON(w,status,map[string]string{"error":msg})}
func writeJSON(w http.ResponseWriter,status int,v any){w.Header().Set("Content-Type","application/json");w.WriteHeader(status);_=json.NewEncoder(w).Encode(v)}
func method(w http.ResponseWriter,m string){w.Header().Set("Allow",m);writeErr(w,405,"method not allowed")}
func required(k string)string{v:=strings.TrimSpace(os.Getenv(k));if v==""{log.Fatalf("%s is required",k)};return v}
func env(k,d string)string{if v:=strings.TrimSpace(os.Getenv(k));v!=""{return v};return d}
func duration(k string,d time.Duration)time.Duration{v:=env(k,"");if v==""{return d};x,e:=time.ParseDuration(v);if e!=nil||x<=0{log.Fatalf("invalid %s",k)};return x}
func randomID()string{var b [16]byte;if _,e:=rand.Read(b[:]);e!=nil{return fmt.Sprintf("media-%d",time.Now().UnixNano())};return hex.EncodeToString(b[:])}
func logging(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){start:=time.Now();next.ServeHTTP(w,r);log.Printf("%s %s %s",r.Method,r.URL.Path,time.Since(start))})}

