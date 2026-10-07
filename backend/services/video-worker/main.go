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
  if _,err=tx.Exec(ctx,`update testagram_video.video_jobs set status='failed',lease_until=null,last_error=coalesce(last_error,'processing lease exhausted'),updated_at=now() where status='running' and lease_until<now() and attempts>=3`);err!=nil{return err}
  if _,err=tx.Exec(ctx,`update testagram_video.videos v set status='failed',processing_error='video processing lease exhausted',updated_at=now() where status in ('queued','processing') and exists (select 1 from testagram_video.video_jobs j where j.video_id=v.id and j.status='failed' and j.attempts>=3)`);err!=nil{return err}
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
    _,_=db.Exec(ctx,`update testagram_video.videos set status=case when exists (select 1 from testagram_video.video_jobs where video_id=$1 and attempts>=3 and status='failed') then 'failed' else 'queued' end,processing_error=$2,updated_at=now() where id=$1`,j.VideoID,workErr.Error())
    return workErr
  }
  if _,err=db.Exec(ctx,`update testagram_video.video_jobs set status='done',lease_until=null,updated_at=now() where id=$1`,j.ID);err!=nil{return err}
  if j.Kind=="poster"{if _,err=db.Exec(ctx,`update testagram_video.videos set poster_object_key=$2,updated_at=now() where id=$1`,j.VideoID,filepath.Join("videos","posters",j.VideoID,"poster.jpg"));err!=nil{return err}}
  if j.Kind=="transcode"{if _,err=db.Exec(ctx,`update testagram_video.videos set status='processing',processing_error=null,updated_at=now() where id=$1`,j.VideoID);err!=nil{return err}}
  _,err=db.Exec(ctx,`update testagram_video.videos v set status='ready',processing_error=null,updated_at=now() where v.id=$1 and exists (select 1 from testagram_video.video_jobs t where t.video_id=v.id and t.kind='transcode' and t.status='done') and exists (select 1 from testagram_video.video_jobs p where p.video_id=v.id and p.kind='poster' and p.status='done')`,j.VideoID)
  return err
}

func transcode(root,id,source string)error{
  out:=filepath.Join(root,"videos","hls",id);if err:=os.MkdirAll(out,0750);err!=nil{return err}
  filter:="[0:v]split=3[v1][v2][v3];[v1]scale=w=426:h=240:force_original_aspect_ratio=decrease:force_divisible_by=2[v1o];[v2]scale=w=640:h=360:force_original_aspect_ratio=decrease:force_divisible_by=2[v2o];[v3]scale=w=1280:h=720:force_original_aspect_ratio=decrease:force_divisible_by=2[v3o]"
  args:=[]string{"-hide_banner","-loglevel","error","-i",source,"-filter_complex",filter,"-map","[v1o]","-c:v:0","h264","-b:v:0","500k","-map","[v2o]","-c:v:1","h264","-b:v:1","900k","-map","[v3o]","-c:v:2","h264","-b:v:2","2500k","-f","hls","-hls_time","4","-hls_playlist_type","vod","-hls_flags","independent_segments","-master_pl_name","master.m3u8","-var_stream_map","v:0,name:240p v:1,name:360p v:2,name:720p",filepath.Join(out,"%v.m3u8")}
  if hasAudio(source){
    args=[]string{"-hide_banner","-loglevel","error","-i",source,"-filter_complex",filter,"-map","[v1o]","-map","0:a:0","-c:v:0","h264","-b:v:0","500k","-map","[v2o]","-map","0:a:0","-c:v:1","h264","-b:v:1","900k","-map","[v3o]","-map","0:a:0","-c:v:2","h264","-b:v:2","2500k","-c:a","aac","-b:a","128k","-f","hls","-hls_time","4","-hls_playlist_type","vod","-hls_flags","independent_segments","-master_pl_name","master.m3u8","-var_stream_map","v:0,a:0,name:240p v:1,a:1,name:360p v:2,a:2,name:720p",filepath.Join(out,"%v.m3u8")}
  }
  return exec.Command("ffmpeg",args...).Run()
}
func hasAudio(source string)bool{out,err:=exec.Command("ffprobe","-v","error","-select_streams","a:0","-show_entries","stream=index","-of","csv=p=0",source).Output();return err==nil&&len(out)>0}
func poster(root,id,source string)error{out:=filepath.Join(root,"videos","posters",id);if err:=os.MkdirAll(out,0750);err!=nil{return err};return exec.Command("ffmpeg","-hide_banner","-loglevel","error","-i",source,"-frames:v","1","-vf","scale=1280:-2",filepath.Join(out,"poster.jpg")).Run()}
func env(k,d string)string{if v:=os.Getenv(k);v!=""{return v};return d}
func mustEnv(k string)string{v:=os.Getenv(k);if v==""{log.Fatalf("%s is required",k)};return v}
