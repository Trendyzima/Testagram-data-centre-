# Testagram durable media architecture: Backblaze B2 primary + Cloudflare R2 replica

## Canonical storage contract

Testagram's durable media path has two independently credentialed S3-compatible object stores:

- **Backblaze B2 is the primary object store and authoritative upload origin.**
- **Cloudflare R2 is the verified replica and recovery origin.**
- The Testagram Go CDN is the public delivery/cache layer. CDN cache is not a durable replica.
- Cloudflare DNS, Pages and edge routing remain separate from media durability. A Cloudflare Worker is not the canonical media API or the source of truth for object bytes.
- Local MinIO is a development/legacy self-hosted object service. It is not counted as a third durable copy in this B2/R2 contract.

The public application and authorization boundary remains the canonical Testagram Go Backend. The Backend verifies native Testagram credentials and live session state; Backend-owned PostgreSQL remains authoritative for local accounts, credentials metadata, posts, visibility, permissions, quotas and product-level media associations. Appwrite-compatible legacy identity data is migration-only and must never authenticate native Testagram user or staff requests. The Data Centre media service is a private provider adapter and lifecycle worker: its dedicated `MEDIA_DATABASE_URL` stores upload grants, provider verification state, operations and replication outbox records. That operational ledger does not replace Backend product data or grant access to a post.

## Ownership and provider roles

| Role | Owner / provider | Responsibility |
|---|---|---|
| Identity and session verification | Testagram Backend native authentication | Verify native credentials and live session state; no external identity-provider JWT is accepted as a native bearer token |
| Product authority | Go Backend + Backend-owned PostgreSQL | Canonical users, posts, permissions, visibility, quotas and media-to-post association |
| Primary durable bytes | Backblaze B2 | Authoritative upload origin and first verified object copy |
| Durable replica | Cloudflare R2 | Independently verified copy; used for recovery if B2 is missing or corrupt |
| Provider lifecycle | Data Centre media service | Private presigning, size/type/SHA-256 verification, replication, reconciliation, repair and deletion execution |
| Operational media ledger | Data Centre PostgreSQL via `MEDIA_DATABASE_URL` | Upload grants, object lifecycle state, primary/replica verification timestamps and durable outbox jobs |
| Public delivery | Testagram Go CDN | Viewer authorization, cache, range/HLS delivery and B2-first/R2-fallback origin reads |

Keep B2 and R2 endpoints, regions, buckets and access keys independent. Use private buckets and least-privilege credentials. Provider credentials stay server-side; never place them in Flutter, browser configuration, node agents or public logs.

The current media service configures one bucket per provider using `B2_S3_BUCKET` and `CLOUDFLARE_R2_BUCKET`. Do not assume that the service is using separate public/private/identity buckets merely because other deployment variables exist. Namespace, owner, object key and Backend authorization must be enforced consistently across upload, verification, download and delete.

## Upload and replication lifecycle

1. Flutter requests an upload reservation from the canonical Go Backend.
2. Backend verifies the native Testagram access token and live session state, resolves the canonical local account, authorizes the intended owner/visibility, enforces quotas and records the product-level media intent in Backend-owned PostgreSQL.
3. Backend calls the private Data Centre media API with a service credential. The Data Centre service creates a bounded upload grant in its operational ledger and returns a short-lived B2 upload URL. The client never receives a provider access key.
4. Flutter uploads bytes directly to B2 using that object-scoped URL.
5. Backend asks the Data Centre media service to verify the upload. The service reads the actual B2 object, checks content type and byte length, computes SHA-256, and persists the verification state.
6. A durable PostgreSQL outbox job copies the verified B2 object to R2. The service reads the R2 copy back and verifies byte length and SHA-256 independently.
7. Only after both primary and replica are verified may the media service mark the object `protected`. Backend must not expose an object as ready before it receives that verified state.
8. The Go CDN serves authorized media. On a cache miss, B2 is the preferred origin and R2 is the recovery fallback. A CDN cache hit never counts as a verified durable copy.
9. If B2 is missing or corrupt, the service first verifies the R2 source completely, restores B2, then performs a fresh B2 read-back before returning the object to `protected`.
10. Deletion is an explicit Backend-authorized lifecycle operation. The Data Centre service records a tombstone/outbox job and propagates deletion to both providers. Failed deletion remains visible for retry/reconciliation; it must not be silently marked complete.

A failed replica write, read-back or checksum check leaves the object degraded/replication-failed and unavailable for normal publication. Never report two-copy durability from a successful B2 upload alone.

## Security and integrity

- Accept only Backend-authorized owner, namespace, object key, content type and size constraints. Reject caller-controlled bucket overrides and path traversal.
- Keep buckets private. Public/private visibility is enforced by Backend and the Go CDN; do not make an entire provider bucket public.
- Use short-lived, object-scoped signed requests. Provider keys never reach a client.
- Verify stored bytes using SHA-256 and exact byte length. ETags are provider-specific metadata, not portable SHA-256 proofs.
- Verify the R2 replica with a fresh GET after PUT; verify a recovery source before overwriting a missing/corrupt primary.
- Bound request sizes, timeouts, transfer concurrency and retry attempts. Persist work before acknowledging it.
- Keep private-media authorization separate from CDN cache keys. Never cache authenticated or private media as public content.
- Retention, expiry and physical deletion remain disabled/dry-run until tombstone, retry, legal-hold and restore-window policies are approved and tested.

## Backups and disaster recovery — current gap

**B2 plus R2 is replication, not a complete independent backup.** A mistaken deletion or compromised credential can affect both copies if deletion is propagated. The CDN cache is not a backup.

The currently inspected backup scripts are not a complete recovery path for this architecture:

- Data Centre `vps/backup.sh` creates database-only archives for its configured Data Centre/Neon databases. It does not export the Data Centre media ledger or B2/R2 object bytes.
- Backend self-hosted `backup/backup.sh` archives Backend PostgreSQL and the legacy local MinIO buckets. It does not prove recovery of the B2/R2 media service ledger or B2/R2 objects.
- Any retained legacy Appwrite identity documents/configuration are outside those database/object archives; they are migration evidence only, not native Testagram authentication authority.
- The existing database restore drill therefore cannot be described as a complete Testagram media disaster-recovery drill.

Before production media sign-off, add an independently encrypted off-host backup for the media operational ledger and canonical object bytes, verify per-object SHA-256 and counts, and restore them into an isolated environment. Define RPO/RTO, retention, encryption-key escrow and recovery ownership. Do not silently treat R2 replication as a backup policy.

## Runtime configuration and deployment gate

The private Data Centre media service requires:

- B2 primary: `B2_S3_ENDPOINT`, `B2_S3_REGION`, `B2_S3_BUCKET`, `B2_S3_ACCESS_KEY_ID`, `B2_S3_SECRET_ACCESS_KEY`
- R2 replica: `CLOUDFLARE_R2_ENDPOINT`, `CLOUDFLARE_R2_REGION`, `CLOUDFLARE_R2_BUCKET`, `CLOUDFLARE_R2_ACCESS_KEY_ID`, `CLOUDFLARE_R2_SECRET_ACCESS_KEY`
- Operational ledger: `MEDIA_DATABASE_URL`
- Backend-to-Data-Centre private API: `DATA_CENTRE_MEDIA_API_URL` and a distinct internal service token

The Backend deployment script can enable this service with `DATA_CENTRE_MEDIA_ENABLED=true`; it synchronizes the Backend `FRESH_MEDIA_ENABLED` gate and validates the required provider configuration before starting the media profile. Keep that gate false until isolated live-provider acceptance tests and the full authorization/upload/playback flow pass.

The repository currently has opt-in Go live-provider tests (`TESTAGRAM_LIVE_MEDIA_SMOKE=true`), but the GitHub live-provider workflow is intentionally absent. No live B2/R2 write is triggered by ordinary CI. These tests require an operator-controlled run against dedicated non-production buckets and an isolated media database; they perform real PUT, GET, HEAD, checksum, replication, restore and DELETE operations. Unit tests and successful container publication do not replace this live proof. Do not run the provider tests against production buckets. Reintroduce an automated workflow only after its staging-only guard, explicit confirmation, redacted evidence artifact and cleanup behavior have been independently reviewed.

## Release gates — not yet satisfied

- [ ] Dedicated staging B2 primary and R2 replica credentials/buckets are configured with least privilege.
- [ ] Authorized live staging upload, GET, HEAD, SHA-256 read-back and delete tests pass on both providers.
- [ ] B2 outage, R2 outage, checksum corruption, missing-object repair and failed-delete retry tests pass.
- [ ] The media operational ledger and provider buckets reconcile without unexplained objects or rows.
- [ ] Independent encrypted off-host backup includes Backend product data, media operational state and B2 object bytes; restore checksums pass.
- [ ] Private-media authorization, expired URL, cache isolation and cross-account access-denial tests pass.
- [ ] Flutter upload → Backend authorization → B2 primary → R2 replica → Go CDN playback succeeds end to end.
- [ ] The public media hostname serves the canonical Go CDN release and no longer exposes the legacy R2 Worker as the canonical media service.
- [ ] Measured restore RPO/RTO and an operator rollback/recovery runbook are approved.

Until these gates pass, keep `FRESH_MEDIA_ENABLED=false` and retention disabled/dry-run. Do not claim production media readiness or full disaster-recovery readiness.
