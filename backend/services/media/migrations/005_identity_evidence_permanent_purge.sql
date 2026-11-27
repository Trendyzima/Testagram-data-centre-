-- Distinguish identity evidence whose B2 versions and delete markers have
-- been physically purged from rows that were only hidden by an S3 delete marker.
BEGIN;

ALTER TABLE media_objects
  ADD COLUMN IF NOT EXISTS identity_evidence_purged_at timestamptz;

COMMENT ON COLUMN media_objects.identity_evidence_purged_at IS
  'Set only after every Backblaze B2 object version and delete marker for the identity evidence object and metadata sidecar has been permanently deleted and verified absent.';

COMMIT;
