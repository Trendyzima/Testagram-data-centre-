# Dedicated Data Centre Media Service

Private Backend-to-Data-Centre object lifecycle service. This service is deliberately separate from the platform feature-flag service and node control-plane.

## Runtime configuration

Required: `MEDIA_INTERNAL_TOKEN` (32+ chars), `MEDIA_DATABASE_URL` (TLS PostgreSQL in production), B2 S3 endpoint/region/bucket/access key/secret, and Cloudflare R2 endpoint/region/bucket/access key/secret. Provider credentials belong only in this service's secret environment.

## API

Implements the five routes in `docs/platform/openapi/media-lifecycle-v1.yaml`. Every route requires both `Authorization: Bearer` and `X-Testagram-API-Version: 1`.

## Safety

- Backend remains the product authorization authority; requester_id is recorded as an authenticated service assertion, not independently interpreted as social permission.
- Upload URLs are short-lived, content-type-bound and byte-limited at the Backend gateway. Object keys are canonical `users/{owner_id}/{32-character-lowercase-hex}` values; the service rejects arbitrary paths and unsupported MIME types.
- Verification requests are durably queued and return promptly; a worker downloads the full B2 object and computes SHA-256 before enqueuing replication.
- The media schema lives only in `backend/services/media/migrations/` and is applied to the dedicated `MEDIA_DATABASE_URL`, never the Backend-owned migration database.
- PostgreSQL transactionally records upload-grant consumption, verification jobs and subsequent replication jobs.
- Worker uses leased `FOR UPDATE SKIP LOCKED` jobs, bounded exponential retries and a dead state.
- R2 writes are independently read back and hashed before state becomes `protected`.
- Deletion is idempotently queued and removes both B2 and R2 copies.
- A separate reconciliation loop re-reads protected objects from both origins every 24 hours, compares exact byte counts and SHA-256, requeues a damaged replica when the primary remains valid, and fails closed if the primary no longer matches.
- Reconciliation leases expire after 15 minutes so a crashed worker does not suppress future checks for a full day.
- No public port is exposed; mount only on a private service network.

This initial adapter targets S3-compatible B2 and R2. **Instabox is not an object-storage provider**: its documented API manages cloud Linux machines, machine lifecycle, commands, files and snapshots. Do not treat an Instabox API key as an S3 credential or add an Instabox media-storage adapter. If adopted, integrate it separately as a compute/deployment provider for VPS workloads. See https://instabox.sh/docs/api.md and https://instabox.sh/docs/machines.md. Do not set `FRESH_MEDIA_ENABLED=true` until live provider integration tests, staging and CDN/Flutter journeys pass.
