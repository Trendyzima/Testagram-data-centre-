create extension if not exists pgcrypto;
create schema if not exists testagram_video;

create table if not exists testagram_video.channels (
  id uuid primary key default gen_random_uuid(),
  owner_id uuid not null,
  handle text not null unique,
  name text not null,
  description text not null default '',
  avatar_object_key text,
  banner_object_key text,
  subscriber_count bigint not null default 0,
  video_count bigint not null default 0,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists testagram_video.videos (
  id uuid primary key default gen_random_uuid(),
  channel_id uuid not null references testagram_video.channels(id) on delete cascade,
  owner_id uuid not null,
  title text not null,
  description text not null default '',
  visibility text not null default 'private' check (visibility in ('private','unlisted','public')),
  status text not null default 'uploading' check (status in ('uploading','queued','processing','ready','failed','blocked')),
  original_object_key text,
  poster_object_key text,
  duration_ms bigint not null default 0,
  width integer not null default 0,
  height integer not null default 0,
  view_count bigint not null default 0,
  like_count bigint not null default 0,
  comment_count bigint not null default 0,
  processing_error text,
  published_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists testagram_video.renditions (
  id uuid primary key default gen_random_uuid(),
  video_id uuid not null references testagram_video.videos(id) on delete cascade,
  profile text not null check (profile in ('240p','360p','480p','720p','1080p')),
  width integer not null,
  height integer not null,
  bitrate_kbps integer not null,
  object_prefix text not null,
  created_at timestamptz not null default now(),
  unique(video_id, profile)
);

create table if not exists testagram_video.video_jobs (
  id uuid primary key default gen_random_uuid(),
  video_id uuid not null references testagram_video.videos(id) on delete cascade,
  kind text not null check (kind in ('transcode','poster')),
  status text not null default 'queued' check (status in ('queued','running','done','failed')),
  attempts integer not null default 0,
  available_at timestamptz not null default now(),
  lease_until timestamptz,
  last_error text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists testagram_video.video_views (
  video_id uuid not null references testagram_video.videos(id) on delete cascade,
  viewer_id uuid,
  session_hash text not null,
  created_at timestamptz not null default now(),
  primary key(video_id, session_hash)
);

create table if not exists testagram_video.video_likes (
  video_id uuid not null references testagram_video.videos(id) on delete cascade,
  user_id uuid not null,
  created_at timestamptz not null default now(),
  primary key(video_id, user_id)
);

create table if not exists testagram_video.subscriptions (
  channel_id uuid not null references testagram_video.channels(id) on delete cascade,
  user_id uuid not null,
  created_at timestamptz not null default now(),
  primary key(channel_id, user_id)
);

create table if not exists testagram_video.comments (
  id uuid primary key default gen_random_uuid(),
  video_id uuid not null references testagram_video.videos(id) on delete cascade,
  user_id uuid not null,
  body text not null check (char_length(body) between 1 and 4000),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists videos_public_idx on testagram_video.videos(visibility, status, published_at desc);
create index if not exists videos_channel_idx on testagram_video.videos(channel_id, created_at desc);
create index if not exists jobs_ready_idx on testagram_video.video_jobs(status, available_at, created_at);
create index if not exists comments_video_idx on testagram_video.comments(video_id, created_at desc);

alter table testagram_video.channels enable row level security;
alter table testagram_video.videos enable row level security;
alter table testagram_video.renditions enable row level security;
alter table testagram_video.video_jobs enable row level security;
alter table testagram_video.video_views enable row level security;
alter table testagram_video.video_likes enable row level security;
alter table testagram_video.subscriptions enable row level security;
alter table testagram_video.comments enable row level security;

-- The application service owns writes. Client access is intentionally not granted
-- directly to this schema; the video API applies authorization and moderation policy.


create index if not exists video_views_session_idx on testagram_video.video_views(session_hash, created_at desc);
create index if not exists subscriptions_user_idx on testagram_video.subscriptions(user_id, created_at desc);
