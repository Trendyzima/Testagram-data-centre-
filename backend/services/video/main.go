package main

import (
  "context"
  "crypto/hmac"
  "crypto/sha256"
  "encoding/hex"
  "encoding/json"
  "errors"
  "fmt"
  "io"
  "log"
  "mime/multipart"
  "net/http"
  "os"
  "path/filepath"
  "strconv"
  "strings"
  "syscall"
  "time"

  "github.com/golang-jwt/jwt/v5"
  "github.com/jackc/pgx/v5/pgxpool"
)

type server struct { db *pgxpool.Pool; mediaDir string; jwtKey []byte; signKey []byte; uploadSlots chan struct{}; accountQuotaBytes int64; minFreeBytes int64 }
type claims struct { jwt.RegisteredClaims }
type channelInput struct { Handle string `json:"handle"`; Name string `json:"name"`; Description string `json:"description"` }

func main() {
  db,err:=pgxpool.New(context.Background(),mustEnv("DATABASE_URL"));if err!=nil{log.Fatal(err)};defer db.Close()
  mediaDir:=env("VIDEO_MEDIA_ROOT","/media")
  accountQuotaBytes,err:=positiveLimitEnv("VIDEO_ACCOUNT_QUOTA_BYTES",20<<30);if err!=nil{log.Fatal(err)}
  minFreeBytes,err:=nonnegativeLimitEnv("VIDEO_MIN_FREE_BYTES",2<<30);if err!=nil{log.Fatal(err)}
  s:=&server{db:db,mediaDir:mediaDir,jwtKey:[]byte(mustEnv("VIDEO_JWT_SECRET")),signKey:[]byte(mustEnv("VIDEO_SIGNING_SECRET")),uploadSlots:make(chan struct{},2),accountQuotaBytes:accountQuotaBytes,minFreeBytes:minFreeBytes}
  if len(s.signKey)<32{log.Fatal("VIDEO_SIGNING_SECRET must be at least 32 bytes")}
  if err:=os.MkdirAll(mediaDir,0750);err!=nil{log.Fatal(err)}
  mux:=http.NewServeMux()
  mux.HandleFunc("/healthz",s.health);mux.HandleFunc("/readyz",s.ready);mux.HandleFunc("/v1/channels",s.channels);mux.HandleFunc("/v1/channels/",s.channelRoute);mux.HandleFunc("/v1/feed",s.feed);mux.HandleFunc("/v1/videos",s.videos);mux.HandleFunc("/v1/videos/",s.videoRoute)
  srv:=&http.Server{Addr:env("VIDEO_ADDR",":8790"),Handler:securityHeaders(mux),ReadHeaderTimeout:10*time.Second,ReadTimeout:2*time.Hour,WriteTimeout:2*time.Hour,IdleTimeout:120*time.Second}
  log.Fatal(srv.ListenAndServe())
}

func (s *server) health(w http.ResponseWriter,r *http.Request){jsonOut(w,200,map[string]any{"ok":true,"service":"video"})}
func (s *server) ready(w http.ResponseWriter,r *http.Request){if err:=s.db.Ping(r.Context());err!=nil{http.Error(w,"database unavailable",503);return};jsonOut(w,200,map[string]any{"ready":true})}

func (s *server) channels(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return};user,ok:=s.user(r);if !ok{http.Error(w,"unauthorized",401);return}
  var in channelInput;if json.NewDecoder(io.LimitReader(r.Body,1<<20)).Decode(&in)!=nil||!validHandle(in.Handle)||strings.TrimSpace(in.Name)==""{http.Error(w,"invalid channel",400);return}
  var id string;err:=s.db.QueryRow(r.Context(),"insert into testagram_video.channels(owner_id,handle,name,description) values($1,$2,$3,$4) returning id",user,strings.ToLower(in.Handle),strings.TrimSpace(in.Name),in.Description).Scan(&id)
  if err!=nil{http.Error(w,"channel creation failed",409);return};jsonOut(w,201,map[string]string{"id":id})
}
func (s *server) channelRoute(w http.ResponseWriter,r *http.Request){
  p:=strings.TrimPrefix(r.URL.Path,"/v1/channels/");parts:=strings.Split(strings.Trim(p,"/"),"/")
  if len(parts)!=2||parts[1]!="subscribe"{http.Error(w,"not found",404);return};if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  user,ok:=s.user(r);if !ok{http.Error(w,"unauthorized",401);return}
  if _,err:=s.db.Exec(r.Context(),`insert into testagram_video.subscriptions(channel_id,user_id) values($1,$2) on conflict do nothing`,parts[0],user);err!=nil{http.Error(w,"subscribe failed",500);return}
  if _,err:=s.db.Exec(r.Context(),`update testagram_video.channels set subscriber_count=(select count(*) from testagram_video.subscriptions where channel_id=$1),updated_at=now() where id=$1`,parts[0]);err!=nil{http.Error(w,"subscribe failed",500);return}
  jsonOut(w,200,map[string]bool{"subscribed":true})
}
func (s *server) feed(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodGet{http.Error(w,"method not allowed",405);return}
  rows,err:=s.db.Query(r.Context(),`select id,channel_id,title,description,visibility,status,duration_ms,width,height,view_count,like_count,comment_count,published_at from testagram_video.videos where visibility='public' and status='ready' order by published_at desc nulls last,created_at desc limit 50`)
  if err!=nil{http.Error(w,"feed failed",500);return};defer rows.Close()
  type item struct{ID string `json:"id"`;ChannelID string `json:"channel_id"`;Title string `json:"title"`;Description string `json:"description"`;Visibility string `json:"visibility"`;Status string `json:"status"`;DurationMS int64 `json:"duration_ms"`;Width int `json:"width"`;Height int `json:"height"`;Views int64 `json:"views"`;Likes int64 `json:"likes"`;Comments int64 `json:"comments"`;PublishedAt *time.Time `json:"published_at"`}
  out:=make([]item,0,50);for rows.Next(){var v item;if err:=rows.Scan(&v.ID,&v.ChannelID,&v.Title,&v.Description,&v.Visibility,&v.Status,&v.DurationMS,&v.Width,&v.Height,&v.Views,&v.Likes,&v.Comments,&v.PublishedAt);err!=nil{http.Error(w,"feed failed",500);return};out=append(out,v)}
  if err:=rows.Err();err!=nil{http.Error(w,"feed failed",500);return};jsonOut(w,200,out)
}
func (s *server) comments(w http.ResponseWriter,r *http.Request,id string){
  if r.Method==http.MethodGet{rows,err:=s.db.Query(r.Context(),`select id,user_id,body,created_at from testagram_video.comments where video_id=$1 order by created_at desc limit 100`,id);if err!=nil{http.Error(w,"comments failed",500);return};defer rows.Close()
    type comment struct{ID string `json:"id"`;UserID string `json:"user_id"`;Body string `json:"body"`;CreatedAt time.Time `json:"created_at"`};out:=make([]comment,0,100);for rows.Next(){var x comment;if err:=rows.Scan(&x.ID,&x.UserID,&x.Body,&x.CreatedAt);err!=nil{http.Error(w,"comments failed",500);return};out=append(out,x)};if err:=rows.Err();err!=nil{http.Error(w,"comments failed",500);return};jsonOut(w,200,out);return}
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return};user,ok:=s.user(r);if !ok{http.Error(w,"unauthorized",401);return}
  var in struct{Body string `json:"body"`};if json.NewDecoder(io.LimitReader(r.Body,1<<20)).Decode(&in)!=nil||strings.TrimSpace(in.Body)==""||len(in.Body)>4000{http.Error(w,"invalid comment",400);return}
  if _,err:=s.db.Exec(r.Context(),`insert into testagram_video.comments(video_id,user_id,body) values($1,$2,$3)`,id,user,strings.TrimSpace(in.Body));err!=nil{http.Error(w,"comment failed",500);return}
  if _,err:=s.db.Exec(r.Context(),`update testagram_video.videos set comment_count=(select count(*) from testagram_video.comments where video_id=$1) where id=$1`,id);err!=nil{http.Error(w,"comment failed",500);return};jsonOut(w,201,map[string]bool{"created":true})
}
func (s *server) videos(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  user,ok:=s.user(r);if !ok{http.Error(w,"unauthorized",401);return}
  select {case s.uploadSlots<-struct{}{}: defer func(){<-s.uploadSlots}();default: http.Error(w,"upload capacity reached",http.StatusTooManyRequests);return}
  if err:=ensureFreeDisk(s.mediaDir,s.minFreeBytes);err!=nil{http.Error(w,"storage temporarily unavailable",http.StatusInsufficientStorage);return}
  // Reserve against the authenticated account before ParseMultipartForm can spill
  // attacker-controlled body bytes to temporary disk. Chunked uploads are rejected
  // because their worst-case reservation cannot be established safely in advance.
  if r.ContentLength<=0{http.Error(w,"Content-Length is required for video uploads",http.StatusLengthRequired);return}
  if r.ContentLength>20<<30{http.Error(w,"upload exceeds 20 GiB request limit",http.StatusRequestEntityTooLarge);return}
  r.Body=http.MaxBytesReader(w,r.Body,20<<30)
  reservationID,err:=s.reserveUploadIntent(r.Context(),user,r.ContentLength)
  if err!=nil{
    if errors.Is(err,errVideoQuotaExceeded){http.Error(w,"video storage quota exceeded",http.StatusInsufficientStorage);return}
    log.Printf("reserve video upload intent: %v",err);http.Error(w,"upload reservation failed",500);return
  }
  videoID:=""
  reservationFinalized:=false
  defer func(){
    if !reservationFinalized{
      cleanupCtx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
      defer cancel()
      s.failVideoUpload(cleanupCtx,reservationID,videoID,"upload interrupted or rejected")
    }
  }()
  if err:=r.ParseMultipartForm(64<<20);err!=nil{http.Error(w,"multipart form required or upload too large",400);return}
  if r.MultipartForm!=nil { defer r.MultipartForm.RemoveAll() }
  channelID,title,description,visibility:=r.FormValue("channel_id"),strings.TrimSpace(r.FormValue("title")),r.FormValue("description"),r.FormValue("visibility");if visibility==""{visibility="private"}
  if title==""||len(title)>200||(visibility!="private"&&visibility!="unlisted"&&visibility!="public"){http.Error(w,"invalid video metadata",400);return}
  var owner string;if err:=s.db.QueryRow(r.Context(),"select owner_id from testagram_video.channels where id=$1",channelID).Scan(&owner);err!=nil||owner!=user{http.Error(w,"channel not found",404);return}
  file,hdr,err:=r.FormFile("video");if err!=nil{http.Error(w,"video file required",400);return};defer file.Close()
  if hdr.Size<=0||hdr.Size>20<<30{http.Error(w,"video file must be between 1 byte and 20 GiB",http.StatusRequestEntityTooLarge);return}
  videoID,err=s.bindVideoUpload(r.Context(),reservationID,channelID,user,title,description,visibility,hdr.Size)
  if err!=nil{log.Printf("bind video upload reservation: %v",err);http.Error(w,"upload reservation failed",500);return}
  ext:=safeExt(hdr);dir:=filepath.Join(s.mediaDir,"videos","originals",videoID);if err:=os.MkdirAll(dir,0750);err!=nil{s.failVideoUpload(r.Context(),reservationID,videoID,"storage unavailable");reservationFinalized=true;http.Error(w,"storage unavailable",500);return};path:=filepath.Join(dir,"source"+ext)
  dst,err:=os.OpenFile(path,os.O_CREATE|os.O_WRONLY|os.O_EXCL,0640);if err!=nil{s.failVideoUpload(r.Context(),reservationID,videoID,"storage unavailable");reservationFinalized=true;http.Error(w,"storage unavailable",500);return}
  copied,copyErr:=io.Copy(dst,io.LimitReader(file,(20<<30)+1));closeErr:=dst.Close()
  if copyErr!=nil||closeErr!=nil||copied!=hdr.Size{_ = os.Remove(path);s.failVideoUpload(r.Context(),reservationID,videoID,"upload failed or size mismatch");reservationFinalized=true;http.Error(w,"upload failed or size mismatch",500);return}
  rel:=strings.TrimPrefix(path,s.mediaDir+"/");if err=s.completeVideoUpload(r.Context(),reservationID,videoID,rel,copied);err!=nil{_ = os.Remove(path);s.failVideoUpload(r.Context(),reservationID,videoID,"upload metadata update failed");reservationFinalized=true;http.Error(w,"upload metadata update failed",500);return}
  if _,err=s.db.Exec(r.Context(),"insert into testagram_video.video_jobs(video_id,kind) values($1,'transcode'),($1,'poster')",videoID);err!=nil{_ = os.Remove(path);s.failVideoUpload(r.Context(),reservationID,videoID,"job creation failed");reservationFinalized=true;http.Error(w,"queue failed",500);return}
  reservationFinalized=true
  jsonOut(w,202,map[string]string{"id":videoID,"status":"queued"})
}

var errVideoQuotaExceeded = errors.New("video account quota exceeded")

func positiveLimitEnv(name string, fallback int64) (int64, error) {
  raw:=strings.TrimSpace(os.Getenv(name));if raw==""{return fallback,nil}
  value,err:=strconv.ParseInt(raw,10,64);if err!=nil||value<=0{return 0,fmt.Errorf("%s must be a positive integer",name)}
  return value,nil
}
func nonnegativeLimitEnv(name string, fallback int64) (int64, error) {
  raw:=strings.TrimSpace(os.Getenv(name));if raw==""{return fallback,nil}
  value,err:=strconv.ParseInt(raw,10,64);if err!=nil||value<0{return 0,fmt.Errorf("%s must be a non-negative integer",name)}
  return value,nil
}
func quotaAllows(used,reserved,requested,limit int64) bool {
  if used<0||reserved<0||requested<=0||limit<=0||requested>limit{return false}
  remaining:=limit-requested
  return used<=remaining && reserved<=remaining-used
}
func ensureFreeDisk(root string, minimum int64) error {
  var stat syscall.Statfs_t
  if err:=syscall.Statfs(root,&stat);err!=nil{return err}
  free:=int64(stat.Bavail)*int64(stat.Bsize)
  if free<minimum{return fmt.Errorf("free disk %d bytes is below required reserve %d bytes",free,minimum)}
  return nil
}
func (s *server) reserveUploadIntent(ctx context.Context,ownerID string,requestBytes int64)(string,error){
  tx,err:=s.db.Begin(ctx);if err!=nil{return "",err};defer tx.Rollback(ctx)
  if _,err=tx.Exec(ctx,"select pg_advisory_xact_lock(hashtextextended($1,0))",ownerID);err!=nil{return "",err}
  var used,reserved int64
  if err=tx.QueryRow(ctx,"select coalesce(sum(original_size_bytes),0) from testagram_video.videos where owner_id=$1 and status <> 'failed'",ownerID).Scan(&used);err!=nil{return "",err}
  if err=tx.QueryRow(ctx,"select coalesce(sum(reserved_bytes),0) from testagram_video.video_upload_reservations where owner_id=$1 and status='reserved' and expires_at>now()",ownerID).Scan(&reserved);err!=nil{return "",err}
  if !quotaAllows(used,reserved,requestBytes,s.accountQuotaBytes){return "",errVideoQuotaExceeded}
  var reservationID string
  if err=tx.QueryRow(ctx,"insert into testagram_video.video_upload_reservations(owner_id,reserved_bytes,expires_at) values($1,$2,now()+interval '24 hours') returning reservation_id",ownerID,requestBytes).Scan(&reservationID);err!=nil{return "",err}
  if err=tx.Commit(ctx);err!=nil{return "",err}
  return reservationID,nil
}
func (s *server) bindVideoUpload(ctx context.Context,reservationID,channelID,ownerID,title,description,visibility string,size int64)(string,error){
  tx,err:=s.db.Begin(ctx);if err!=nil{return "",err};defer tx.Rollback(ctx)
  var reservedOwner string;var reservedBytes int64
  if err=tx.QueryRow(ctx,"select owner_id,reserved_bytes from testagram_video.video_upload_reservations where reservation_id=$1 and status='reserved' and expires_at>now() for update",reservationID).Scan(&reservedOwner,&reservedBytes);err!=nil{return "",err}
  if reservedOwner!=ownerID||size<=0||size>reservedBytes{return "",errors.New("upload reservation owner or size mismatch")}
  var id string
  if err=tx.QueryRow(ctx,"insert into testagram_video.videos(channel_id,owner_id,title,description,visibility,status) values($1,$2,$3,$4,$5,'uploading') returning id",channelID,ownerID,title,description,visibility).Scan(&id);err!=nil{return "",err}
  if _,err=tx.Exec(ctx,"update testagram_video.video_upload_reservations set video_id=$2,reserved_bytes=$3,updated_at=now() where reservation_id=$1",reservationID,id,size);err!=nil{return "",err}
  if err=tx.Commit(ctx);err!=nil{return "",err}
  return id,nil
}
func (s *server) completeVideoUpload(ctx context.Context,reservationID,id,key string,size int64)error{
  tx,err:=s.db.Begin(ctx);if err!=nil{return err};defer tx.Rollback(ctx)
  if _,err=tx.Exec(ctx,"update testagram_video.videos set original_object_key=$2,original_size_bytes=$3,status='queued',updated_at=now() where id=$1",id,key,size);err!=nil{return err}
  tag,err:=tx.Exec(ctx,"update testagram_video.video_upload_reservations set status='completed',updated_at=now() where reservation_id=$1 and video_id=$2 and status='reserved'",reservationID,id);if err!=nil{return err};if tag.RowsAffected()!=1{return errors.New("active upload reservation was not found")}
  return tx.Commit(ctx)
}
func (s *server) failVideoUpload(ctx context.Context,reservationID,id,reason string){
  if id!=""{_,_=s.db.Exec(ctx,"update testagram_video.videos set status='failed',processing_error=$2,updated_at=now() where id=$1",id,reason)}
  _,_=s.db.Exec(ctx,"update testagram_video.video_upload_reservations set status='released',updated_at=now() where reservation_id=$1 and status='reserved'",reservationID)
}

func (s *server) videoRoute(w http.ResponseWriter,r *http.Request){
  p:=strings.TrimPrefix(r.URL.Path,"/v1/videos/");parts:=strings.Split(strings.Trim(p,"/"),"/");if len(parts)<2{http.Error(w,"not found",404);return};id:=parts[0]
  switch parts[1]{case "publish":s.publish(w,r,id);case "view":s.view(w,r,id);case "like":s.like(w,r,id);case "comments":s.comments(w,r,id);case "manifest":s.manifest(w,r,id);case "hls":if len(parts)==3{s.hls(w,r,id,parts[2])}else{http.Error(w,"not found",404)};default:http.Error(w,"not found",404)}
}
func (s *server) publish(w http.ResponseWriter,r *http.Request,id string){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return};user,ok:=s.user(r);if !ok{http.Error(w,"unauthorized",401);return};var owner,status string
  if err:=s.db.QueryRow(r.Context(),"select owner_id,status from testagram_video.videos where id=$1",id).Scan(&owner,&status);err!=nil||owner!=user{http.Error(w,"not found",404);return};if status!="ready"{http.Error(w,"video not ready",409);return}
  if _,err:=s.db.Exec(r.Context(),"update testagram_video.videos set visibility='public',published_at=now(),updated_at=now() where id=$1",id);err!=nil{http.Error(w,"publish failed",500);return};jsonOut(w,200,map[string]string{"status":"public"})
}
func (s *server) view(w http.ResponseWriter,r *http.Request,id string){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return};var status,visibility string
  if err:=s.db.QueryRow(r.Context(),"select status,visibility from testagram_video.videos where id=$1",id).Scan(&status,&visibility);err!=nil||status!="ready"||visibility=="private"{http.Error(w,"not available",404);return}
  session:=r.Header.Get("X-Playback-Session");if session==""{session=r.RemoteAddr+"|"+time.Now().UTC().Format("2006-01-02")};sum:=sha256.Sum256([]byte(session));h:=hex.EncodeToString(sum[:])
  tag,err:=s.db.Exec(r.Context(),"insert into testagram_video.video_views(video_id,session_hash) values($1,$2) on conflict do nothing",id,h);if err!=nil{http.Error(w,"view failed",500);return};if tag.RowsAffected()>0{_,_=s.db.Exec(r.Context(),"update testagram_video.videos set view_count=view_count+1 where id=$1",id)};jsonOut(w,202,map[string]bool{"counted":tag.RowsAffected()>0})
}
func (s *server) like(w http.ResponseWriter,r *http.Request,id string){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return};user,ok:=s.user(r);if !ok{http.Error(w,"unauthorized",401);return}
  if _,err:=s.db.Exec(r.Context(),"insert into testagram_video.video_likes(video_id,user_id) values($1,$2) on conflict do nothing",id,user);err!=nil{http.Error(w,"like failed",500);return}
  if _,err:=s.db.Exec(r.Context(),"update testagram_video.videos set like_count=(select count(*) from testagram_video.video_likes where video_id=$1) where id=$1",id);err!=nil{http.Error(w,"like failed",500);return};jsonOut(w,200,map[string]bool{"liked":true})
}
func (s *server) manifest(w http.ResponseWriter,r *http.Request,id string){
  var status,visibility string;if err:=s.db.QueryRow(r.Context(),"select status,visibility from testagram_video.videos where id=$1",id).Scan(&status,&visibility);err!=nil||status!="ready"||visibility=="private"{http.Error(w,"not available",404);return}
  http.Redirect(w,r,fmt.Sprintf("/v1/videos/%s/hls/master.m3u8?token=%s",id,s.sign(id,time.Now().Add(6*time.Hour))),http.StatusTemporaryRedirect)
}
func (s *server) hls(w http.ResponseWriter,r *http.Request,id,file string){
  token:=r.URL.Query().Get("token");if !s.validToken(id,token){http.Error(w,"forbidden",403);return};if strings.Contains(file,"..")||strings.ContainsAny(file,"/\\"){http.Error(w,"bad path",400);return}
  path:=filepath.Join(s.mediaDir,"videos","hls",id,file);data,err:=os.ReadFile(path);if err!=nil{http.NotFound(w,r);return}
  if strings.HasSuffix(file,".m3u8"){lines:=strings.Split(string(data),"\n");for i,line:=range lines{if line!=""&&!strings.HasPrefix(line,"#")&&!strings.Contains(line,"?token="){lines[i]=line+"?token="+token}};w.Header().Set("Content-Type","application/vnd.apple.mpegurl");w.Header().Set("Cache-Control","private, no-store");_,_=w.Write([]byte(strings.Join(lines,"\n")));return}
  w.Header().Set("Cache-Control","private, max-age=300");http.ServeFile(w,r,path)
}
func (s *server) user(r *http.Request)(string,bool){
  raw:=strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"),"Bearer "));if raw==""{return "",false}
  token,err:=jwt.ParseWithClaims(raw,&claims{},func(t *jwt.Token)(any,error){if t.Method.Alg()!="HS256"{return nil,errors.New("algorithm rejected")};return s.jwtKey,nil})
  if err!=nil||!token.Valid{return "",false};sub,err:=token.Claims.GetSubject();if err!=nil||sub==""{return "",false};return sub,true
}
func (s *server) sign(id string,exp time.Time)string{mac:=hmac.New(sha256.New,s.signKey);_,_=mac.Write([]byte(id+"|"+fmt.Sprint(exp.Unix())));return hex.EncodeToString(mac.Sum(nil))+"."+fmt.Sprint(exp.Unix())}
func (s *server) validToken(id,t string)bool{p:=strings.Split(t,".");if len(p)!=2{return false};var exp int64;if _,e:=fmt.Sscan(p[1],&exp);e!=nil||time.Now().Unix()>exp{return false};mac:=hmac.New(sha256.New,s.signKey);_,_=mac.Write([]byte(id+"|"+p[1]));return hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))),[]byte(p[0]))}
func securityHeaders(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Content-Type-Options","nosniff");next.ServeHTTP(w,r)})}
func jsonOut(w http.ResponseWriter,status int,v any){w.Header().Set("Content-Type","application/json");w.WriteHeader(status);_=json.NewEncoder(w).Encode(v)}
func validHandle(v string)bool{if len(v)<3||len(v)>40{return false};for _,c:=range v{if !(c=='-'||c=='_'||c=='.'||c>='a'&&c<='z'||c>='0'&&c<='9'){return false}};return true}
func safeExt(h *multipart.FileHeader)string{ext:=strings.ToLower(filepath.Ext(h.Filename));switch ext{case ".mp4",".mov",".mkv",".webm":return ext};return ".bin"}
func env(k,d string)string{if v:=os.Getenv(k);v!=""{return v};return d}
func mustEnv(k string)string{v:=os.Getenv(k);if v==""{log.Fatalf("%s is required",k)};return v}
