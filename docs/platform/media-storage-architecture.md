# Testagram object storage: Instabox VPS + Cloudflare R2 + Backblaze B2

## Storage is not just CDN

Cloudflare is a storage provider in Testagram, not merely DNS, CDN, Worker hosting or a cache. **Cloudflare R2 stores the actual media object bytes** in a private R2 bucket. Backblaze B2 and Cloudflare R2 are two independently credentialed S3-compatible object stores holding verified copies of the same canonical object.

The Cloudflare Worker is only an authorization-aware delivery interface to R2. It does not replace R2, and CDN cache is not counted as storage capacity.

## Ownership

The Data Centre repository owns deployment, node registration/capacity, node-level replica placement and independent backup/restore orchestration. The Go Backend remains the only public API and authorization boundary and owns media authorization, metadata, quotas, checksum policy and the provider-replica ledger. Data Centre operates the Instabox VPS object-store service, replication/repair workers and backup/restore operations. Provider operations across all three stores must be coordinated through this contract. XClone and node agents never receive long-lived provider credentials.

| Role | Provider | Responsibility |
|---|---|---|
| Durable object store A | Instabox VPS private object storage | Local S3-compatible MinIO/object service on persistent attached storage; one verified full copy |
| Durable object store B | Cloudflare R2 | Full verified copy in a private bucket; private origin for Testagram CDN |
| Durable object store C | Backblaze B2 | Independent full verified copy and off-site recovery origin |
| Public delivery | Testagram Go CDN | Own edge/shield cache, viewer authorization, range/HLS/DASH delivery and health-aware origin failover; not a durable replica |
| Optional node replicas | Registered Android/Linux nodes | Additional verified copies or explicitly disposable caches; never the only copy |
| Media metadata and quotas | Backend PostgreSQL (`testagram_media`) | Upload reservations, ownership, quotas, lifecycle and per-provider replica state; Appwrite is used for identity/session verification, not as the media ledger |

B2 and R2 are independent object stores. Keep endpoints, regions, credentials and buckets separately configured; never reuse access keys. Use least-privilege credentials scoped to the required buckets and operations. Keep both buckets private.

## Upload and replication contract

1. Flutter requests an upload reservation from the Backend. Backend verifies the Appwrite session, resolves the linked local account, authorizes owner and visibility, enforces quotas, and records the pending object plus quota reservation in its PostgreSQL `testagram_media` schema.
2. Backend selects the configured upload origin (B2 or R2) and persists that choice in the PostgreSQL reservation. It issues a short-lived, object-scoped upload authorization for that provider; provider credentials remain server-side.
3. The client uploads bytes directly to the selected provider. Backend verifies object existence, content type, byte length and the actual SHA-256 of the stored bytes.
4. Backend/Data Centre copies the complete object to the other two providers using bounded streaming transfers. It independently reads each replica back and verifies size, content type and SHA-256.
5. Under the target durable-media policy, only after all three stores are verified does Backend commit the protected media as ready and record all three replicas. A missing copy leaves the object replicating/degraded and triggers repair; never report three-copy durability from one or two successful uploads.
6. Testagram CDN serves the object from its own cache/shield. On a miss it fetches from healthy private origins in the configured order (Instabox VPS, R2, B2). If an origin is unavailable or fails integrity checks, CDN falls back to another verified replica and raises a repair signal. Cloudflare is not the public media CDN.
7. Repair can copy in either direction from a surviving verified provider. It must never overwrite a good copy with an unverified source.
8. Registered Android/Linux node replicas are optional additional copies beyond the three required stores. A node must prove mounted storage identity, available quota, object checksum and heartbeat health. Reported disk capacity alone is not replica evidence.

This is a dual-object-storage design: B2 and R2 both retain the media bytes. R2 is not only a cache, and the application must not count a CDN cache hit as a verified replica.

## Object identity and security

- Use opaque, non-user-controlled object keys; do not put email addresses or other personal data in bucket paths.
- Store SHA-256, byte length, media type, owner, visibility, created time and per-provider verification state in the Backend PostgreSQL media metadata ledger.
- Validate MIME type from file content, enforce upload size limits and reject path traversal or caller-supplied bucket/key overrides.
- Keep buckets private. Use short-lived signed requests or Backend-proxied authorization; never expose S3 access keys to browsers, Android nodes or desktop nodes.
- Encrypt transport with TLS. Keep provider secrets in the deployment secret store and rotate them independently.
- Treat ETags as provider-specific metadata, not as a portable SHA-256 digest. Multipart uploads must be completed and verified with a real SHA-256 checksum.
- Do not let a node self-assign object ownership or mark its own replica verified. Backend/Data Centre must verify transfer receipts and checksum evidence.
- Private R2 delivery must use the Worker signing secret; never expose a public R2 bucket or bypass the Backend authorization decision.

## Retention, deletion and recovery

Normal posts are retained forever; stories expire after 24 hours. Expiry and deletion must be enforced by a Backend-owned, auditable lifecycle job and propagated to every provider and replica only after policy and legal-hold checks pass.

Retention remains disabled and dry-run in the fresh deployment. No destructive provider lifecycle rule, bucket-wide cleanup, node wipe or quota reclamation may be activated as part of this storage cutover. A deleted object is eligible for physical reclamation only after the canonical tombstone is committed, replica deletion is acknowledged, retry/dead-letter handling is complete and the configured recovery window has elapsed.

Backups must be independent of the active B2/R2 pair. A restore drill must recover metadata and object bytes, verify SHA-256 for restored objects, and exercise private-media authorization and playback. A database-only restore or successful bucket listing is not a complete media recovery test.

## Runtime configuration

The target Backend/Data Centre media runtime requires independent server-side configuration for all three stores. Instabox VPS uses its private MinIO/S3-compatible endpoint and dedicated credentials; the current self-hosted stack already deploys local MinIO but the fresh dual-provider media path has not yet integrated it as a verified third copy.

The external provider configuration includes:

- Instabox VPS: private MinIO endpoint, bucket and dedicated access/secret key (configuration contract still to be implemented in the fresh media runtime)
- B2: `B2_S3_ENDPOINT`, `B2_S3_REGION`, `B2_S3_BUCKET`, `B2_S3_ACCESS_KEY_ID`, `B2_S3_SECRET_ACCESS_KEY`
- R2: `CLOUDFLARE_R2_ENDPOINT`, `CLOUDFLARE_R2_REGION`, `CLOUDFLARE_R2_BUCKET`, `CLOUDFLARE_R2_ACCESS_KEY_ID`, `CLOUDFLARE_R2_SECRET_ACCESS_KEY`
- Worker delivery: `CLOUDFLARE_MEDIA_BASE_URL`, `CLOUDFLARE_MEDIA_SIGNING_SECRET`

The credentials are injected into Backend, not the node control plane. The Data Centre node agent only receives node-scoped credentials or one-time transfer capabilities when verified node-level object transfer is implemented.

## Delivery and DNS boundary

Cloudflare DNS is authoritative for Testagram's zone. The media hostname points DNS-only to Testagram CDN edges. Testagram CDN owns public delivery and cache/shield behavior. Cloudflare R2 stays private and is fetched as an origin by Testagram CDN using a reviewed private-origin adapter. Do not expose R2 directly to browsers or use Cloudflare's proxy/Worker as a second media CDN. Cloudflare DNS/Web Analytics provide aggregated domain and site-performance telemetry; product analytics remains in Instabox PostgreSQL and CDN operational telemetry flows to Data Centre monitoring.

## Release gates — not yet satisfied

- Real B2 and R2 bucket access, upload, read-back, checksum and delete tests using isolated staging buckets (current provider unit tests are not live-account proof).
- Crash/retry testing of the current synchronous B2-to-R2 completion path; durable outbox-driven replication, bounded retries and dead-letter recovery remain required before production scale.
- Provider outage tests proving no unverified object is published and the surviving verified copy remains usable.
- Reconciliation of PostgreSQL media/replica metadata with both provider buckets.
- Android removable-SD and Linux node transfer tests with signed receipts; node-to-provider object transfer is not yet implemented.
- Replica corruption, missing-object repair and restore-from-each-verified-copy tests.
- Private-media authorization, CDN cache isolation and URL-expiry tests.
- End-to-end upload, feed rendering, download and playback tests.
- Independent encrypted backup and measured restore RPO/RTO.

Until those gates pass, keep `FRESH_MEDIA_ENABLED=false` and retention disabled/dry-run. Do not claim production media readiness.
