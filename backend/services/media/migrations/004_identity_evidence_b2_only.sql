-- Retire legacy identity-evidence replica state. Identity evidence is now
-- verified and served only from the dedicated private Backblaze B2 bucket.
-- Historical media_operations rows are preserved as an immutable audit trail.
BEGIN;

UPDATE media_objects
SET replica_verified_at = NULL,
    reconciliation_claimed_at = NULL,
    status = CASE
      WHEN status = 'replication_pending' THEN 'uploaded'
      WHEN status = 'restore_pending' THEN 'replication_failed'
      ELSE status
    END,
    updated_at = now()
WHERE namespace = 'identity-evidence-private'
  AND (replica_verified_at IS NOT NULL
       OR status IN ('replication_pending', 'restore_pending')
       OR reconciliation_claimed_at IS NOT NULL);

-- A queued restore from the retired R2 replica must not silently fall back to
-- R2. Keep it visible as a dead letter for an audited B2 recovery decision.
UPDATE media_outbox
SET status = 'dead',
    locked_until = NULL,
    last_error = 'identity evidence is B2-only; legacy replica restore requires an audited operator recovery',
    updated_at = now()
WHERE namespace = 'identity-evidence-private'
  AND operation = 'restore_primary'
  AND status IN ('pending', 'processing', 'retry');

COMMIT;
