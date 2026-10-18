ALTER TABLE testagram_video.videos
  ADD COLUMN IF NOT EXISTS original_size_bytes bigint NOT NULL DEFAULT 0
  CHECK (original_size_bytes >= 0);

CREATE TABLE IF NOT EXISTS testagram_video.video_upload_reservations (
  reservation_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  video_id uuid UNIQUE REFERENCES testagram_video.videos(id) ON DELETE CASCADE,
  owner_id uuid NOT NULL,
  reserved_bytes bigint NOT NULL CHECK (reserved_bytes > 0),
  status text NOT NULL DEFAULT 'reserved'
    CHECK (status IN ('reserved', 'completed', 'released')),
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS video_upload_reservations_owner_active_idx
  ON testagram_video.video_upload_reservations(owner_id, expires_at)
  WHERE status = 'reserved';

ALTER TABLE testagram_video.video_upload_reservations ENABLE ROW LEVEL SECURITY;
