-- Add a quarantined lifecycle state and durable outbox operation for rebuilding
-- the B2 primary from an independently verified R2 replica.
alter table media_objects drop constraint if exists media_objects_status_check;
alter table media_objects add constraint media_objects_status_check
  check(status in ('upload_pending','uploaded','replication_pending','protected','replication_failed','restore_pending','delete_pending','deleted'));

alter table media_outbox drop constraint if exists media_outbox_operation_check;
alter table media_outbox add constraint media_outbox_operation_check
  check(operation in ('verify','replicate','restore_primary','delete'));

alter table media_operations drop constraint if exists media_operations_operation_check;
alter table media_operations add constraint media_operations_operation_check
  check(operation in ('upload_grant','verify','replicate','restore_primary','delete'));
