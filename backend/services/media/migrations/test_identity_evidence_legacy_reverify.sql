DO $$
BEGIN
  IF (
    SELECT count(*) FROM media_outbox
    WHERE namespace = 'identity-evidence-private'
      AND object_key IN ('identity/legacy-replication', 'identity/legacy-restore', 'identity/legacy-replication-failed')
      AND operation = 'verify' AND status = 'pending'
  ) <> 3 THEN
    RAISE EXCEPTION 'legacy identity evidence was not queued for direct B2 verification';
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM media_outbox
    WHERE owner_id = 'legacy-owner-1'
      AND namespace = 'identity-evidence-private'
      AND object_key = 'identity/legacy-replication'
      AND operation = 'verify' AND status = 'pending' AND attempts = 0
      AND last_error = 'reverify dedicated B2 identity object after legacy replica retirement'
  ) THEN
    RAISE EXCEPTION 'previously completed verify job was not safely re-queued for B2 verification';
  END IF;

  IF EXISTS (
    SELECT 1 FROM media_objects
    WHERE owner_id = 'legacy-owner-3'
      AND namespace = 'identity-evidence-private'
      AND object_key = 'identity/legacy-replication-failed'
      AND (primary_verified_at IS NOT NULL OR replica_verified_at IS NOT NULL)
  ) THEN
    RAISE EXCEPTION 'legacy failed identity object retained stale primary or replica verification';
  END IF;

  IF (
    SELECT count(*) FROM media_operations
    WHERE namespace = 'identity-evidence-private'
      AND object_key IN ('identity/legacy-replication', 'identity/legacy-restore', 'identity/legacy-replication-failed')
      AND operation = 'verify' AND outcome = 'queued'
      AND detail = 'queued B2-only verification after legacy identity replica retirement'
  ) <> 3 THEN
    RAISE EXCEPTION 'legacy identity B2 verification audit records are missing or duplicated';
  END IF;

  IF EXISTS (
    SELECT 1 FROM media_outbox
    WHERE namespace = 'identity-evidence-private'
      AND object_key IN ('identity/legacy-replication', 'identity/legacy-restore', 'identity/legacy-replication-failed')
      AND operation IN ('replicate', 'restore_primary')
      AND status IN ('pending', 'processing', 'retry')
  ) THEN
    RAISE EXCEPTION 'legacy identity replica or restore job remains executable';
  END IF;
END $$;

-- Remove deterministic fixtures after all assertions pass.
DELETE FROM media_operations WHERE owner_id IN ('legacy-owner-1','legacy-owner-2','legacy-owner-3');
DELETE FROM media_outbox WHERE owner_id IN ('legacy-owner-1','legacy-owner-2','legacy-owner-3');
DELETE FROM media_objects WHERE owner_id IN ('legacy-owner-1','legacy-owner-2','legacy-owner-3');
