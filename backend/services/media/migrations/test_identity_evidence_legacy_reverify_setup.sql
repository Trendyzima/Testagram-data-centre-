-- Deterministic fixtures for the legacy identity-evidence re-verification migration test.
-- Runs only against the disposable CI media database, after migrations 001-003.
BEGIN;

DELETE FROM media_operations WHERE owner_id IN ('legacy-owner-1','legacy-owner-2','legacy-owner-3');
DELETE FROM media_outbox WHERE owner_id IN ('legacy-owner-1','legacy-owner-2','legacy-owner-3');
DELETE FROM media_objects WHERE owner_id IN ('legacy-owner-1','legacy-owner-2','legacy-owner-3');

INSERT INTO media_objects(
  owner_id, namespace, object_key, content_type, size_bytes, sha256, status,
  primary_verified_at, replica_verified_at, reconciliation_claimed_at
) VALUES
  ('legacy-owner-1','identity-evidence-private','identity/legacy-replication','image/jpeg',1024,repeat('a',64),'replication_pending',now(),now(),now()),
  ('legacy-owner-2','identity-evidence-private','identity/legacy-restore','image/jpeg',2048,repeat('b',64),'restore_pending',now(),now(),now()),
  ('legacy-owner-3','identity-evidence-private','identity/legacy-replication-failed','image/jpeg',3072,repeat('c',64),'replication_failed',now(),now(),now());

-- A previously completed verification must be re-queued because the old
-- provider path did not prove a fresh B2 read after the replica retirement.
INSERT INTO media_outbox(owner_id,namespace,object_key,operation,status,attempts)
VALUES ('legacy-owner-1','identity-evidence-private','identity/legacy-replication','verify','done',2);

-- These legacy jobs must be dead-lettered by migration 004, never executed.
INSERT INTO media_outbox(owner_id,namespace,object_key,operation,status,attempts,locked_until)
VALUES
  ('legacy-owner-1','identity-evidence-private','identity/legacy-replication','replicate','pending',0,NULL),
  ('legacy-owner-2','identity-evidence-private','identity/legacy-restore','restore_primary','processing',1,now()+interval '5 minutes'),
  ('legacy-owner-3','identity-evidence-private','identity/legacy-replication-failed','replicate','retry',2,NULL);

COMMIT;
