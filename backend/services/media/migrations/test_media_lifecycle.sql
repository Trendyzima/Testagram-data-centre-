-- Exercise the durable outbox journal across a dead/retry-style lifecycle.
-- Run only in the isolated CI media database; this transaction rolls back.
begin;

insert into media_objects(owner_id,namespace,object_key,content_type,size_bytes,status)
values ('ci-user','media-public','users/ci-user/0123456789abcdef0123456789abcdef','image/jpeg',1,'uploaded')
on conflict(owner_id,namespace,object_key) do update set status='uploaded';

insert into media_outbox(owner_id,namespace,object_key,operation,status,attempts)
values ('ci-user','media-public','users/ci-user/0123456789abcdef0123456789abcdef','replicate','pending',0)
on conflict(owner_id,namespace,object_key,operation) do update
set status='pending',attempts=0,available_at=now(),locked_until=null,last_error=null;

update media_outbox set status='processing',attempts=1
where owner_id='ci-user' and namespace='media-public'
  and object_key='users/ci-user/0123456789abcdef0123456789abcdef' and operation='replicate';
update media_outbox set status='done'
where owner_id='ci-user' and namespace='media-public'
  and object_key='users/ci-user/0123456789abcdef0123456789abcdef' and operation='replicate';

update media_outbox set status='pending',attempts=0,available_at=now(),locked_until=null
where owner_id='ci-user' and namespace='media-public'
  and object_key='users/ci-user/0123456789abcdef0123456789abcdef' and operation='replicate';
update media_outbox set status='processing',attempts=1
where owner_id='ci-user' and namespace='media-public'
  and object_key='users/ci-user/0123456789abcdef0123456789abcdef' and operation='replicate';
update media_outbox set status='done'
where owner_id='ci-user' and namespace='media-public'
  and object_key='users/ci-user/0123456789abcdef0123456789abcdef' and operation='replicate';

-- The new recovery state is quarantined until the restore worker verifies B2.
update media_objects set status='restore_pending'
where owner_id='ci-user' and namespace='media-public'
  and object_key='users/ci-user/0123456789abcdef0123456789abcdef';
insert into media_outbox(owner_id,namespace,object_key,operation,status,attempts)
values ('ci-user','media-public','users/ci-user/0123456789abcdef0123456789abcdef','restore_primary','pending',0)
on conflict(owner_id,namespace,object_key,operation) do update
set status='pending',attempts=0,available_at=now(),locked_until=null,last_error=null;
update media_outbox set status='processing',attempts=1
where owner_id='ci-user' and namespace='media-public'
  and object_key='users/ci-user/0123456789abcdef0123456789abcdef' and operation='restore_primary';
update media_outbox set status='done'
where owner_id='ci-user' and namespace='media-public'
  and object_key='users/ci-user/0123456789abcdef0123456789abcdef' and operation='restore_primary';

do $
declare event_count integer;
begin
  select count(*) into event_count from media_operations
  where owner_id='ci-user' and object_key='users/ci-user/0123456789abcdef0123456789abcdef'
    and operation='replicate' and outcome='succeeded' and operation_id like 'outbox:%';
  if event_count <> 2 then
    raise exception 'expected two distinct durable journal events across outbox reuse; got %', event_count;
  end if;
end $$;

rollback;
