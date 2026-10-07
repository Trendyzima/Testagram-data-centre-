package main

import (
  "context"
  "fmt"
  "log"
  "os"
  "os/exec"
  "path/filepath"
  "time"
  "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgxpool"
)

type job struct { ID string; VideoID string; Kind string; Source string }

func main() {
  db,err:=pgxpool.New(context.Background(),mustEnv("DATABASE_URL")); if err!=nil{log.Fatal(err)}; defer db.Close()
  root:=env("VIDEO_MEDIA_ROOT","/media")
  for { if err:=runOne(context.Background(),db,root);err!=nil{log.Printf("video job: %v",err);time.Sleep(3*time.Second)}else{time.Sleep(500*time.Millisecond)} }
}

func runOne(ctx context.Context,db *pgxpool.Pool,root string)error{
  tx,err:=db.Begin(ctx);if err!=nil{return err};defer tx.Rollback(ctx)
  var j job
  err=tx.QueryRow(ctx,`with picked as (
    select id from testagram_video.video_jobs
    where (status='queued' and available_at<=now()) or (status='running' and lease_until<now() and attempts<3)
    order by created_at for update skip locked limit 1
  )
  update testagram_video.video_jobs q
  set status='running',attempts=q.attempts+1,lease_until=now()+interval '15 minutes',updated_at=now()
  from picked where q.id=picked.id
  returning q.id,q.video_id,q.kind,(select original_object_key from testagram_video.videos v where v.id=q.video_id)`).Scan(&j.ID,&j.VideoID,&j.Kind,&j.Source)
  if err==pgx.ErrNoRows{return nil};if err!=nil{return err};if err=tx.Commit(ctx);err!=nil{return err}

  var workErr error
  switch j.Kind{case "poster":workErr=poster(root,j.VideoID,filepath.Join(root,j.Source));case "transcode":workErr=transcode(root,j.VideoID,filepath.Join(root,j.Source));default:workErr=fmt.Errorf("unknown job %s",j.Kind)}
  if workErr!=nil{
    _,_=db.Exec(ctx,`update testagram_video.video_jobs set status=case when attempts>=3 then 'failed' else 'queued' end,available_at=now()+interval '30 seconds',last_error=$2,lease_until=null,updated_at=now() where id=$1`,j.ID,workErr.Error())
    if j.Kind=="transcode"{_,_=db.Exec(ctx,`update testagram_video.videos set status=case when (select attempts from testagram_video.video_jobs where id=$2)>=3 then 'failed' else 'queued' end,processing_error=$3,updated_at=now() where id=$1`,j.VideoID,j.ID,workErr.Error())}
    return workErr
  }
  if _,err=db.Exec(ctx,`update testagram_video.video_jobs set status='done',lease_until=null,updated_at=now() where id=$1`,j.ID);err!=nil{return err}
  if j.Kind=="transcode"{_,err=db.Exec(ctx,`update testagram_video.videos set status='ready',processing_error=null,updated_at=now() where id=$1`,j.VideoID)}
  return err
}

func transcode(root,id,source string)error{
  out:=filepath.Join(root,"hls",id);if err:=os.MkdirAll(out,0750);err!=nil{return err}
  args:=[]string{"-hide_banner","-loglevel","error","-i",source,"-filter_complex","[0:v]split=3[v1][v2][v3];[v1]scale=w=426:h=240:force_original_aspect_ratio=decrease[v1o];[v2]scale=w=640:h=360:force_original_aspect_ratio=decrease[v2o];[v3]scale=w=1280:h=720:force_original_aspect_ratio=decrease[v3o]","-map","[v1o]","-map","0:a?","-c:v:0","h264","-b:v:0","500k","-map","[v2o]","-map","0:a?","-c:v:1","h264","-b:v:1","900k","-map","[v3o]","-map","0:a?","-c:v:2","h264","-b:v:2","2500k","-c:a","aac","-b:a","128k","-f","hls","-hls_time","4","-hls_playlist_type","vod","-hls_flags","independent_segments","-master_pl_name","master.m3u8","-var_stream_map","v:0,a:0,name:240p v:1,a:1,name:360p v:2,a:2,name:720p",filepath.Join(out,"%v.m3u8")}
  return exec.Command("ffmpeg",args...).Run()
}
func poster(root,id,source string)error{out:=filepath.Join(root,"hls",id);if err:=os.MkdirAll(out,0750);err!=nil{return err};return exec.Command("ffmpeg","-hide_banner","-loglevel","error","-i",source,"-frames:v","1","-vf","scale=1280:-2",filepath.Join(out,"poster.jpg")).Run()}
func env(k,d string)string{if v:=os.Getenv(k);v!=""{return v};return d}
func mustEnv(k string)string{v:=os.Getenv(k);if v==""{log.Fatalf("%s is required",k)};return v}
