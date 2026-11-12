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
alter table media_objects add column if not exists reconciled_at timestamptz;
alter table media_objects add column if not exists reconciliation_claimed_at timestamptz;

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
 operation text not null check(operation in ('verify','replicate','delete')),
 status text not null default 'pending' check(status in ('pending','processing','retry','done','dead','cancelled')),
 attempts integer not null default 0,
 available_at timestamptz not null default now(),
 locked_until timestamptz,
 last_error text,
 created_at timestamptz not null default now(),
 updated_at timestamptz not null default now(),
 unique(owner_id,namespace,object_key,operation)
);
create index if not exists media_outbox_claim_idx on media_outbox(status,available_at,locked_until,id);

create table if not exists media_operations (
 id bigserial primary key,
 operation_id text not null,
 owner_id text not null,
 namespace text not null,
 object_key text not null,
 operation text not null check(operation in ('upload_grant','verify','replicate','delete')),
 outcome text not null check(outcome in ('issued','verified','queued','succeeded','retry','dead','cancelled')),
 attempt integer not null default 0 check(attempt>=0),
 detail text,
 created_at timestamptz not null default now()
);
create index if not exists media_operations_object_idx on media_operations(owner_id,namespace,object_key,created_at desc,id desc);
create index if not exists media_operations_id_idx on media_operations(operation_id,created_at desc);

alter table media_outbox drop constraint if exists media_outbox_status_check;
alter table media_outbox add constraint media_outbox_status_check check(status in ('pending','processing','retry','done','dead','cancelled'));
alter table media_operations drop constraint if exists media_operations_outcome_check;
alter table media_operations add constraint media_operations_outcome_check check(outcome in ('issued','verified','queued','succeeded','retry','dead','cancelled'));

create or replace function media_record_outbox_operation() returns trigger language plpgsql as $$
begin
 if new.status in ('done','retry','dead','cancelled') and new.status is distinct from old.status then
  insert into media_operations(operation_id,owner_id,namespace,object_key,operation,outcome,attempt,detail)
  values ('outbox:'||new.id::text||':'||new.attempts::text,new.owner_id,new.namespace,new.object_key,new.operation,
          case new.status when 'done' then 'succeeded' when 'dead' then 'dead' when 'cancelled' then 'cancelled' else 'retry' end,
          new.attempts,new.last_error);
 end if;
 return new;
end;
$$;
drop trigger if exists media_outbox_operation_journal on media_outbox;
create trigger media_outbox_operation_journal after update of status on media_outbox
for each row execute function media_record_outbox_operation();

create unique index if not exists media_operations_operation_id_uidx on media_operations(operation_id);
