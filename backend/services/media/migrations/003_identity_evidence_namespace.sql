-- Add the Backend-owned native identity evidence namespace without
-- widening any public media namespace or changing existing object records.
ALTER TABLE media_objects
  DROP CONSTRAINT IF EXISTS media_objects_namespace_check;

ALTER TABLE media_objects
  ADD CONSTRAINT media_objects_namespace_check
  CHECK (namespace IN ('media-public', 'media-private', 'identity-evidence-private'));
