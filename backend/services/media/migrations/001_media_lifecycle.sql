create table if not exists media_objects (
 owner_id text not null,
 namespace text not null check(namespace in ('media-public','media-private')),
 object_key text not null,
 content_type text not null,
 size_bytes bigint not null check(size_bytes>0),
 sha256 char(64),
 status text not null check(status in ('upload_pending','uploaded','replication_pending','protected','replication_failed','delete_pending','deleted')),
 primary_verified_at timestamptz,
 replica_verified_at timestamptz,
 created_at timestamptz not null default now(),
 updated_at timestamptz not null default now(),
 primary key(owner_id,namespace,object_key)
);
create table if not exists media_upload_grants (
 id text primary key,
 owner_id text not null,
 namespace text not null,
 object_key text not null,
 max_bytes bigint not null check(max_bytes>0),
 expires_at timestamptz not null,
 consumed_at timestamptz,
 created_at timestamptz not null default now()
);
create index if not exists media_upload_grants_lookup_idx on media_upload_grants(owner_id,namespace,object_key,expires_at desc);
create table if not exists media_outbox (
 id bigserial primary key,
 owner_id text not null,
 namespace text not null,
 object_key text not null,
 operation text not null check(operation in ('replicate','delete')),
 status text not null default 'pending' check(status in ('pending','processing','retry','done','dead')),
 attempts integer not null default 0,
 available_at timestamptz not null default now(),
 locked_until timestamptz,
 last_error text,
 created_at timestamptz not null default now(),
 updated_at timestamptz not null default now(),
 unique(owner_id,namespace,object_key,operation)
);
create index if not exists media_outbox_claim_idx on media_outbox(status,available_at,locked_until,id);
