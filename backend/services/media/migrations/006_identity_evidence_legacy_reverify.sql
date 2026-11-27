-- Re-verify legacy identity evidence against its dedicated B2 primary after
-- migration 004 retired R2 replication and restore jobs. This migration is
-- deliberately idempotent: provisioning may re-apply the SQL on each deploy.
BEGIN;

-- A legacy identity object can have a successful primary timestamp from the
-- former replication path while still being marked replication_failed. Clear
-- that trust signal so the B2 verifier actually re-reads and hashes the object.
UPDATE media_objects
SET primary_verified_at = NULL,
    replica_verified_at = NULL,
    updated_at = now()
WHERE namespace = 'identity-evidence-private'
  AND status = 'replication_failed'
  AND primary_verified_at IS NOT NULL;

WITH candidates AS (
  SELECT m.owner_id, m.namespace, m.object_key
  FROM media_objects AS m
  WHERE m.namespace = 'identity-evidence-private'
    AND m.status IN ('uploaded', 'replication_failed')
    AND m.primary_verified_at IS NULL
    AND m.sha256 IS NOT NULL
    AND m.size_bytes > 0
    AND NOT EXISTS (
      SELECT 1 FROM media_operations AS audit
      WHERE audit.owner_id = m.owner_id
        AND audit.namespace = m.namespace
        AND audit.object_key = m.object_key
        AND audit.operation = 'verify'
        AND audit.outcome = 'queued'
        AND audit.detail = 'queued B2-only verification after legacy identity replica retirement'
    )
),
queued AS (
  INSERT INTO media_outbox(
    owner_id, namespace, object_key, operation, status, attempts,
    available_at, locked_until, last_error
  )
  SELECT owner_id, namespace, object_key, 'verify', 'pending', 0,
         now(), NULL,
         'reverify dedicated B2 identity object after legacy replica retirement'
  FROM candidates
  ON CONFLICT(owner_id, namespace, object_key, operation) DO UPDATE
  SET status = CASE
        WHEN media_outbox.status IN ('done', 'dead', 'cancelled') THEN 'pending'
        ELSE media_outbox.status
      END,
      attempts = CASE
        WHEN media_outbox.status IN ('done', 'dead', 'cancelled') THEN 0
        ELSE media_outbox.attempts
      END,
      available_at = CASE
        WHEN media_outbox.status IN ('done', 'dead', 'cancelled') THEN now()
        ELSE media_outbox.available_at
      END,
      locked_until = CASE
        WHEN media_outbox.status IN ('done', 'dead', 'cancelled') THEN NULL
        ELSE media_outbox.locked_until
      END,
      last_error = CASE
        WHEN media_outbox.status IN ('done', 'dead', 'cancelled')
          THEN 'reverify dedicated B2 identity object after legacy replica retirement'
        ELSE media_outbox.last_error
      END,
      updated_at = now()
  RETURNING owner_id, namespace, object_key
)
INSERT INTO media_operations(
  operation_id, owner_id, namespace, object_key, operation, outcome, detail
)
SELECT gen_random_uuid()::text, owner_id, namespace, object_key, 'verify', 'queued',
       'queued B2-only verification after legacy identity replica retirement'
FROM queued;

COMMIT;
