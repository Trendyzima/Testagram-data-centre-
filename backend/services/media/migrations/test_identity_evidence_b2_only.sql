DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM media_objects
    WHERE namespace='identity-evidence-private' AND replica_verified_at IS NOT NULL
  ) THEN
    RAISE EXCEPTION 'B2-only identity migration left a verified replica timestamp';
  END IF;
  IF EXISTS (
    SELECT 1 FROM media_objects
    WHERE namespace='identity-evidence-private' AND status IN ('replication_pending','restore_pending')
  ) THEN
    RAISE EXCEPTION 'B2-only identity migration left a replica/restore lifecycle state';
  END IF;
  IF EXISTS (
    SELECT 1 FROM media_objects
    WHERE namespace='identity-evidence-private' AND status='uploaded' AND primary_verified_at IS NOT NULL
  ) THEN
    RAISE EXCEPTION 'B2-only identity migration left stale primary verification on a recheck-required object';
  END IF;
  IF EXISTS (
    SELECT 1 FROM media_outbox
    WHERE namespace='identity-evidence-private'
      AND operation IN ('replicate','restore_primary')
      AND status IN ('pending','processing','retry')
  ) THEN
    RAISE EXCEPTION 'B2-only identity migration left a legacy replica restore queued';
  END IF;
END $$;
