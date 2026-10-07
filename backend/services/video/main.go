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
  "strings"
  "time"

  "github.com/golang-jwt/jwt/v5"
  "github.com/jackc/pgx/v5/pgxpool"
)

type server struct {
  db *pgxpool.Pool
  mediaDir string
  jwtKey []byte
  signKey []byte
}
type claims struct { jwt.RegisteredClaims }
type channelInput struct { Handle string `json:"handle"`; Name string `json:"name"`; Description string `json:"description"` }

func main() {
  db, err := pgxpool.New(context.Background(), mustEnv("DATABASE_URL")); if err != nil { log.Fatal(err) }; defer db.Close()
  mediaDir := env("VIDEO_MEDIA_ROOT", "/media")
  s := &server{db: db, mediaDir: mediaDir, jwtKey: []byte(mustEnv("VIDEO_JWT_SECRET")), signKey: []byte(mustEnv("VIDEO_SIGNING_SECRET"))}
  if err := os.MkdirAll(mediaDir, 0750); err != nil { log.Fatal(err) }

  mux := http.NewServeMux()
  mux.HandleFunc("/healthz", s.health)
  mux.HandleFunc("/v1/channels", s.channels)
  mux.HandleFunc("/v1/videos", s.videos)
  mux.HandleFunc("/v1/videos/", s.videoRoute)
  srv := &http.Server{Addr: env("VIDEO_ADDR", ":8790"), Handler: securityHeaders(mux), ReadHeaderTimeout: 10*time.Second, ReadTimeout: 60*time.Second, WriteTimeout: 60*time.Second, IdleTimeout: 120*time.Second}
  log.Fatal(srv.ListenAndServe())
}

func (s *server) health(w http.ResponseWriter,r *http.Request) {
  if err:=s.db.Ping(r.Context()); err!=nil { http.Error(w,"database unavailable",503); return }
  jsonOut(w,200,map[string]any{"ok":true,"service":"video"})
}

func (s *server) channels(w http.ResponseWriter,r *http.Request) {
  if r.Method!=http.MethodPost { http.Error(w,"method not allowed",405); return }
  user,ok:=s.user(r); if !ok { http.Error(w,"unauthorized",401); return }
  var in channelInput
  if json.NewDecoder(io.LimitReader(r.Body,1<<20)).Decode(&in)!=nil || !validHandle(in.Handle) || strings.TrimSpace(in.Name)=="" { http.Error(w,"invalid channel",400); return }
  var id string
  err:=s.db.QueryRow(r.Context(),"insert into testagram_video.channels(owner_id,handle,name,description) values($1,$2,$3,$4) returning id",user,strings.ToLower(in.Handle),strings.TrimSpace(in.Name),in.Description).Scan(&id)
  if err!=nil { http.Error(w,"channel creation failed",409); return }
  jsonOut(w,201,map[string]string{"id":id})
}

func (s *server) videos(w http.ResponseWriter,r *http.Request) {
  if r.Method!=http.MethodPost { http.Error(w,"method not allowed",405); return }
  user,ok:=s.user(r); if !ok { http.Error(w,"unauthorized",401); return }
  if err:=r.ParseMultipartForm(64<<20); err!=nil { http.Error(w,"multipart form required",400); return }
  channelID,title,description,visibility:=r.FormValue("channel_id"),strings.TrimSpace(r.FormValue("title")),r.FormValue("description"),r.FormValue("visibility")
  if visibility=="" { visibility="private" }
  if title=="" || len(title)>200 || (visibility!="private"&&visibility!="unlisted"&&visibility!="public") { http.Error(w,"invalid video metadata",400); return }

  var owner string
  if err:=s.db.QueryRow(r.Context(),"select owner_id from testagram_video.channels where id=$1",channelID).Scan(&owner); err!=nil || owner!=user { http.Error(w,"channel not found",404); return }
  file,hdr,err:=r.FormFile("video"); if err!=nil { http.Error(w,"video file required",400); return }; defer file.Close()

  var id string
  if err:=s.db.QueryRow(r.Context(),"insert into testagram_video.videos(channel_id,owner_id,title,description,visibility,status) values($1,$2,$3,$4,$5,'uploading') returning id",channelID,user,title,description,visibility).Scan(&id); err!=nil { http.Error(w,"video creation failed",500); return }

  ext:=safeExt(hdr); dir:=filepath.Join(s.mediaDir,"originals",id)
  if err:=os.MkdirAll(dir,0750); err!=nil { http.Error(w,"storage unavailable",500); return }
  path:=filepath.Join(dir,"source"+ext)
  dst,err:=os.OpenFile(path,os.O_CREATE|os.O_WRONLY|os.O_EXCL,0640); if err!=nil { http.Error(w,"storage unavailable",500); return }
  _,copyErr:=io.Copy(dst,io.LimitReader(file,20<<30)); closeErr:=dst.Close()
  if copyErr!=nil||closeErr!=nil { _=os.Remove(path); _,_=s.db.Exec(r.Context(),"update testagram_video.videos set status='failed',processing_error=$2 where id=$1",id,"upload failed"); http.Error(w,"upload failed",500); return }

  rel:=strings.TrimPrefix(path,s.mediaDir+"/")
  if _,err=s.db.Exec(r.Context(),"update testagram_video.videos set original_object_key=$2,status='queued',updated_at=now() where id=$1",id,rel); err!=nil { http.Error(w,"queue failed",500); return }
  if _,err=s.db.Exec(r.Context(),"insert into testagram_video.video_jobs(video_id,kind) values($1,'transcode'),($1,'poster')",id); err!=nil { http.Error(w,"queue failed",500); return }
  jsonOut(w,202,map[string]string{"id":id,"status":"queued"})
}

func (s *server) videoRoute(w http.ResponseWriter,r *http.Request) {
  p:=strings.TrimPrefix(r.URL.Path,"/v1/videos/"); parts:=strings.Split(strings.Trim(p,"/"),"/"); if len(parts)<2 { http.Error(w,"not found",404); return }
  id:=parts[0]
  switch parts[1] {
  case "publish": s.publish(w,r,id)
  case "view": s.view(w,r,id)
  case "like": s.like(w,r,id)
  case "manifest": s.manifest(w,r,id)
  case "hls": if len(parts)==3 { s.hls(w,r,id,parts[2]) } else { http.Error(w,"not found",404) }
  default: http.Error(w,"not found",404)
  }
}

func (s *server) publish(w http.ResponseWriter,r *http.Request,id string) {
  if r.Method!=http.MethodPost { http.Error(w,"method not allowed",405); return }; user,ok:=s.user(r); if !ok { http.Error(w,"unauthorized",401); return }
  var owner,status string; if err:=s.db.QueryRow(r.Context(),"select owner_id,status from testagram_video.videos where id=$1",id).Scan(&owner,&status); err!=nil||owner!=user { http.Error(w,"not found",404); return }
  if status!="ready" { http.Error(w,"video not ready",409); return }
  if _,err:=s.db.Exec(r.Context(),"update testagram_video.videos set visibility='public',published_at=now(),updated_at=now() where id=$1",id); err!=nil { http.Error(w,"publish failed",500); return }
  jsonOut(w,200,map[string]string{"status":"public"})
}

func (s *server) view(w http.ResponseWriter,r *http.Request,id string) {
  if r.Method!=http.MethodPost { http.Error(w,"method not allowed",405); return }
  var status,visibility string; if err:=s.db.QueryRow(r.Context(),"select status,visibility from testagram_video.videos where id=$1",id).Scan(&status,&visibility); err!=nil||status!="ready"||visibility=="private" { http.Error(w,"not available",404); return }
  session:=r.Header.Get("X-Playback-Session"); if session=="" { session=r.RemoteAddr+"|"+time.Now().UTC().Format("2006-01-02") }
  sum:=sha256.Sum256([]byte(session)); h:=hex.EncodeToString(sum[:])
  tag,err:=s.db.Exec(r.Context(),"insert into testagram_video.video_views(video_id,session_hash) values($1,$2) on conflict do nothing",id,h); if err!=nil { http.Error(w,"view failed",500); return }
  if tag.RowsAffected()>0 { _,_=s.db.Exec(r.Context(),"update testagram_video.videos set view_count=view_count+1 where id=$1",id) }
  jsonOut(w,202,map[string]bool{"counted":tag.RowsAffected()>0})
}

func (s *server) like(w http.ResponseWriter,r *http.Request,id string) {
  if r.Method!=http.MethodPost { http.Error(w,"method not allowed",405); return }; user,ok:=s.user(r); if !ok { http.Error(w,"unauthorized",401); return }
  _,err:=s.db.Exec(r.Context(),"insert into testagram_video.video_likes(video_id,user_id) values($1,$2) on conflict do nothing",id,user); if err!=nil { http.Error(w,"like failed",500); return }
  _,err=s.db.Exec(r.Context(),"update testagram_video.videos set like_count=(select count(*) from testagram_video.video_likes where video_id=$1) where id=$1",id); if err!=nil { http.Error(w,"like failed",500); return }
  jsonOut(w,200,map[string]bool{"liked":true})
}

func (s *server) manifest(w http.ResponseWriter,r *http.Request,id string) {
  var status,visibility string; if err:=s.db.QueryRow(r.Context(),"select status,visibility from testagram_video.videos where id=$1",id).Scan(&status,&visibility); err!=nil||status!="ready"||visibility=="private" { http.Error(w,"not available",404); return }
  http.Redirect(w,r,http.StatusTemporaryRedirect,fmt.Sprintf("/v1/videos/%s/hls/master.m3u8?token=%s",id,s.sign(id,time.Now().Add(6*time.Hour))))
}

func (s *server) hls(w http.ResponseWriter,r *http.Request,id,file string) {
  if !s.validToken(id,r.URL.Query().Get("token")) { http.Error(w,"forbidden",403); return }
  if strings.Contains(file,"..")||strings.ContainsAny(file,"/\\") { http.Error(w,"bad path",400); return }
  http.ServeFile(w,r,filepath.Join(s.mediaDir,"hls",id,file))
}

func (s *server) user(r *http.Request)(string,bool) {
  raw:=strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"),"Bearer ")); if raw=="" { return "",false }
  token,err:=jwt.ParseWithClaims(raw,&claims{},func(t *jwt.Token)(any,error){ if t.Method.Alg()!="HS256" { return nil,errors.New("algorithm rejected") }; return s.jwtKey,nil })
  if err!=nil||!token.Valid { return "",false }; sub,err:=token.Claims.GetSubject(); if err!=nil||sub=="" { return "",false }; return sub,true
}

func (s *server) sign(id string,exp time.Time)string { mac:=hmac.New(sha256.New,s.signKey); _,_=mac.Write([]byte(id+"|"+fmt.Sprint(exp.Unix()))); return hex.EncodeToString(mac.Sum(nil))+"."+fmt.Sprint(exp.Unix()) }
func (s *server) validToken(id,t string)bool { p:=strings.Split(t,"."); if len(p)!=2{return false}; var exp int64; if _,e:=fmt.Sscan(p[1],&exp);e!=nil||time.Now().Unix()>exp{return false}; mac:=hmac.New(sha256.New,s.signKey); _,_=mac.Write([]byte(id+"|"+p[1])); return hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))),[]byte(p[0])) }
func securityHeaders(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Content-Type-Options","nosniff");next.ServeHTTP(w,r)})}
func jsonOut(w http.ResponseWriter,status int,v any){w.Header().Set("Content-Type","application/json");w.WriteHeader(status);_=json.NewEncoder(w).Encode(v)}
func validHandle(v string)bool{if len(v)<3||len(v)>40{return false};for _,c:=range v{if !(c=='-'||c=='_'||c=='.'||c>='a'&&c<='z'||c>='0'&&c<='9'){return false}};return true}
func safeExt(h *multipart.FileHeader)string{ext:=strings.ToLower(filepath.Ext(h.Filename));switch ext{case ".mp4",".mov",".mkv",".webm":return ext};return ".bin"}
func env(k,d string)string{if v:=os.Getenv(k);v!=""{return v};return d}
func mustEnv(k string)string{v:=os.Getenv(k);if v==""{log.Fatalf("%s is required",k)};return v}
