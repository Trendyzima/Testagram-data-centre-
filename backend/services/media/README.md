# Dedicated Data Centre Media Service

Private Backend-to-Data-Centre object lifecycle service. This service is deliberately separate from the platform feature-flag service and node control-plane.

## Runtime configuration

Required: `MEDIA_INTERNAL_TOKEN` (32+ chars), `MEDIA_DATABASE_URL` (TLS PostgreSQL in production), B2 S3 endpoint/region/bucket/access key/secret, and Cloudflare R2 endpoint/region/bucket/access key/secret. Provider credentials belong only in this service's secret environment.

## API

Implements the five routes in `docs/platform/openapi/media-lifecycle-v1.yaml`. Every route requires both `Authorization: Bearer` and `X-Testagram-API-Version: 1`.

## Safety

- Backend remains the product authorization authority; requester_id is recorded as an authenticated service assertion, not independently interpreted as social permission.
- Upload URLs are short-lived, content-type-bound and byte-limited at the Backend gateway.
- Verification downloads the full B2 object and computes SHA-256 before enqueuing replication.
- PostgreSQL transactionally records verification and the replication outbox event.
- Worker uses leased `FOR UPDATE SKIP LOCKED` jobs, bounded exponential retries and a dead state.
- R2 writes are independently read back and hashed before state becomes `protected`.
- Deletion is idempotently queued and removes both B2 and R2 copies.
- No public port is exposed; mount only on a private service network.

This initial adapter targets S3-compatible B2 and R2. Instabox is intentionally not enabled until its supported API, authentication model and object consistency guarantees are verified. Do not set `FRESH_MEDIA_ENABLED=true` until live provider integration tests, staging and CDN/Flutter journeys pass.
