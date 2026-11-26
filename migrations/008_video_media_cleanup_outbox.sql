-- Durable local video-media deletion intents.
-- Intentionally no FK to videos: cleanup must survive the product row deletion.
create table if not exists testagram_video.video_media_cleanup_jobs (
  video_id uuid primary key,
  status text not null default 'pending'
    check (status in ('pending','running','done','failed')),
  attempts integer not null default 0 check (attempts >= 0),
  available_at timestamptz not null default now(),
  lease_until timestamptz,
  last_error text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  check ((status = 'running') = (lease_until is not null))
);

create index if not exists video_media_cleanup_ready_idx
  on testagram_video.video_media_cleanup_jobs(status, available_at, created_at)
  where status in ('pending','running');

alter table testagram_video.video_media_cleanup_jobs enable row level security;
