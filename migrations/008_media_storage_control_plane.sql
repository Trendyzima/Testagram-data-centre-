CREATE SCHEMA IF NOT EXISTS testagram_storage;

CREATE TABLE IF NOT EXISTS testagram_storage.media_objects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id TEXT NOT NULL,
    namespace TEXT NOT NULL CHECK (namespace IN ('media-private','media-public')),
    object_key TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    sha256 TEXT,
    replica_state TEXT NOT NULL DEFAULT 'pending' CHECK (replica_state IN ('pending','verified','degraded')),
    status TEXT NOT NULL CHECK (status IN (
        'upload_pending','replication_pending','primary_verified','replication_failed','protected','delete_pending','deleted'
    )),
    primary_verified_at TIMESTAMPTZ,
    replica_verified_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (owner_id, namespace, object_key),
    CHECK (sha256 IS NULL OR sha256 ~ '^[0-9a-f]{64}$')
);

CREATE INDEX IF NOT EXISTS media_objects_owner_status_idx
    ON testagram_storage.media_objects (owner_id, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS testagram_storage.media_replication_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id TEXT NOT NULL,
    namespace TEXT NOT NULL CHECK (namespace IN ('media-private','media-public')),
    object_key TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    sha256 TEXT CHECK (sha256 IS NULL OR sha256 ~ '^[0-9a-f]{64}
    status TEXT NOT NULL CHECK (status IN ('queued','processing','completed','dead')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_until TIMESTAMPTZ,
    lease_token UUID,
    last_error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (owner_id, namespace, object_key),
    FOREIGN KEY (owner_id, namespace, object_key)
      REFERENCES testagram_storage.media_objects(owner_id, namespace, object_key)
      ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS media_replication_jobs_ready_idx
    ON testagram_storage.media_replication_jobs (available_at, created_at)
    WHERE status IN ('queued','processing');
),
    status TEXT NOT NULL CHECK (status IN ('queued','processing','completed','dead')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_until TIMESTAMPTZ,
    lease_token UUID,
    last_error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (owner_id, namespace, object_key),
    FOREIGN KEY (owner_id, namespace, object_key)
      REFERENCES testagram_storage.media_objects(owner_id, namespace, object_key)
      ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS media_replication_jobs_ready_idx
    ON testagram_storage.media_replication_jobs (available_at, created_at)
    WHERE status IN ('queued','processing');
