# Dedicated Data Centre Media Service

Private Backend-to-Data-Centre object lifecycle service. This service is deliberately separate from the platform feature-flag service and node control-plane.

## Runtime configuration

Required for ordinary media: `MEDIA_INTERNAL_TOKEN` (32+ chars), `MEDIA_DATABASE_URL` (TLS PostgreSQL in production), B2 S3 endpoint/region/media bucket/access key/secret, and Cloudflare R2 endpoint/region/media bucket/access key/secret. Identity-evidence storage is an independent, opt-in feature and is not a media-service or ordinary-production-deployment requirement. When `IDENTITY_EVIDENCE_INTERNAL_TOKEN` is absent, the service does not register identity-evidence routes, does not enqueue legacy identity purges, excludes identity-evidence jobs from the outbox worker, and excludes identity-evidence objects from reconciliation. Stale identity bucket/key variables are ignored. Provider credentials belong only in this service's secret environment.

## Private deployment wiring

1. Provision a **separate PostgreSQL database** for media lifecycle state. Set `MEDIA_DATABASE_URL` to that database's TLS connection string; do not point it at the Backend metadata database, platform configuration database, or node control-plane database.
2. Store the ordinary-media B2 and R2 credentials as private VPS deployment secrets. Do not create, provision or configure an identity-evidence bucket or identity-evidence application key for ordinary media deployment. If the separate identity-evidence capability is deliberately enabled in a future, separately approved change, configure its dedicated service token, bucket and bucket-scoped credentials together; its key must not access the ordinary-media bucket, and the ordinary-media key must not access the identity bucket. Never configure an R2 identity-evidence bucket or copy provider keys into Backend, Flutter, CDN public variables, or Git.
3. Generate one high-entropy service token (at least 32 random bytes). Set it as `FRESH_DATA_CENTRE_INTERNAL_TOKEN` for the Backend API and map the same value to `MEDIA_INTERNAL_TOKEN` for this service. The Compose file maps the same source secret to both.
4. On the shared private Compose network, set `DATA_CENTRE_MEDIA_API_URL=http://media:8096`. The `media` container is not published to the public host; only the Backend API should call it.
5. Start the service with `docker compose --profile media -f vps/docker-compose.fresh.yml up -d --build media`. Compose runs `media-migrate` first and waits for it to finish successfully.
6. Keep `FRESH_MEDIA_ENABLED=false` until provider read/write/delete/read-back tests, Backend upload-to-completion tests, CDN authorization tests and Flutter playback tests pass against a staging bucket pair. CI uses fake `.invalid` provider endpoints and is not evidence of real object writes.

## API

Implements the five routes in `docs/platform/openapi/media-lifecycle-v1.yaml`. Every route requires both `Authorization: Bearer` and `X-Testagram-API-Version: 1`.

## Safety

- Backend remains the product authorization authority; requester_id is recorded as an authenticated service assertion, not independently interpreted as social permission.
- Upload URLs are short-lived, content-type-bound and byte-limited at the Backend gateway. Object keys are canonical `users/{canonical-Testagram-owner-id}/{32-character-lowercase-hex}; owner IDs may contain letters, digits, dots, underscores and hyphens` values; the service rejects arbitrary paths and unsupported MIME types.
- Verification requests are durably queued and return promptly; a worker downloads the full B2 object and computes SHA-256 before enqueuing replication.
- The media schema lives only in `backend/services/media/migrations/` and is applied to the dedicated `MEDIA_DATABASE_URL`, never the Backend-owned migration database.
- PostgreSQL transactionally records upload-grant consumption, verification jobs and subsequent replication jobs.
- Worker uses leased `FOR UPDATE SKIP LOCKED` jobs, bounded exponential retries and a dead state.
- R2 writes are independently read back and hashed before state becomes `protected`.
- Ordinary-media deletion is idempotently queued and removes its configured B2 and R2 copies.
- Identity-evidence upload grants expire after at most five minutes. Deletion waits until every issued presigned PUT URL has expired plus a five-minute in-flight grace; consumed database grants do not revoke an already-issued B2 URL.
- Identity-evidence deletion lists every B2 object version and delete marker, permanently deletes each exact version ID for both the evidence object and its metadata sidecar, then lists again and refuses to mark the record deleted unless no versions remain.
- The identity-evidence purge marker is set only after the physical version purge is verified. This lifecycle runs only when the separately enabled identity-evidence feature is active.
- The identity-only B2 key is bucket-scoped and requires `readFiles`, `writeFiles`, `listFiles` and `deleteFiles`; the last two capabilities are required to enumerate and permanently delete versions.
- A separate reconciliation loop re-reads protected objects from both origins every 24 hours, compares exact byte counts and SHA-256, requeues a damaged replica when the primary remains valid, and fails closed if the primary no longer matches.
- Reconciliation leases expire after 15 minutes so a crashed worker does not suppress future checks for a full day.
- No public port is exposed; mount only on a private service network.

This initial adapter targets S3-compatible B2 and R2. **Instabox is not an object-storage provider**: its documented API manages cloud Linux machines, machine lifecycle, commands, files and snapshots. Do not treat an Instabox API key as an S3 credential or add an Instabox media-storage adapter. If adopted, integrate it separately as a compute/deployment provider for VPS workloads. See https://instabox.sh/docs/api.md and https://instabox.sh/docs/machines.md. Do not set `FRESH_MEDIA_ENABLED=true` until live provider integration tests, staging and CDN/Flutter journeys pass.
