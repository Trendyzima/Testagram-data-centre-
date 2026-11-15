# InstaBox three-copy media operations: VPS MinIO, Cloudflare R2 and Backblaze B2

**Status:** operational implementation checklist. No production secrets, buckets, DNS records or services are changed by this document.

## Provider roles

- **InstaBox VPS MinIO:** local private serving origin and first durable copy.
- **Cloudflare R2:** private cloud object replica; Cloudflare DNS and analytics remain separate from object storage.
- **Backblaze B2:** independent private cloud replica and disaster-recovery source.
- **Testagram Go CDN:** public media byte-delivery plane; cache is disposable and never a durable copy.
- **Backend PostgreSQL:** canonical asset metadata, owner UUID, visibility, checksum, state, quota, tombstone and replica ledger.

The existing dedicated media service currently configures B2 as its primary and R2 as its replica, with its own PostgreSQL lifecycle/job database. This two-cloud-copy service is not by itself proof that the VPS MinIO copy participates in the canonical three-copy readiness gate. The integration must reconcile the dedicated service with Backend's VPS/MinIO replica ledger before declaring an asset ready.

## Secret ownership

Keep these secrets only in the private Data Centre media service environment / VPS secret manager:

- MEDIA_INTERNAL_TOKEN
- MEDIA_DATABASE_URL (dedicated operational media database, not a replacement for Backend canonical PostgreSQL)
- B2_S3_ENDPOINT, B2_S3_REGION, B2_S3_BUCKET, B2_S3_ACCESS_KEY_ID, B2_S3_SECRET_ACCESS_KEY
- CLOUDFLARE_R2_ENDPOINT, CLOUDFLARE_R2_REGION, CLOUDFLARE_R2_BUCKET, CLOUDFLARE_R2_ACCESS_KEY_ID, CLOUDFLARE_R2_SECRET_ACCESS_KEY
- VPS MinIO endpoint, bucket and dedicated access/secret key, scoped to the required namespaces.

Never place provider master keys in Flutter, Cloudflare Pages variables, public Worker variables, CDN public config, Backend user responses, Git or logs. Use separate keys per provider and environment; least-privilege bucket scopes; rotate through a staged overlap window.

## Required service integration

1. Backend authenticates the user and authorizes upload intent. Data Centre accepts only Backend service-authenticated calls.
2. Canonical object identity is owner UUID + allow-listed namespace + normalized key. Caller-supplied owner IDs are never trusted without a Backend service assertion.
3. Ingest bytes to the configured staging provider, then enqueue durable replication jobs for the other two providers.
4. Stream each destination back and calculate SHA-256 independently. Compare SHA-256, byte length and content type with canonical metadata.
5. Write a provider-specific durable receipt to the replica ledger only after successful read-back verification.
6. Backend marks the asset ready only when VPS MinIO, R2 and B2 receipts all match. Otherwise expose replicating or degraded to authorized clients; never return a ready public URL.
7. Use leased claims, bounded retry with jitter, expired-lease recovery, dead-letter state, graceful shutdown and idempotency keys.
8. Periodically reconcile all protected objects by full read-back SHA-256. A missing/mismatched replica is repaired from a surviving replica that matches the canonical digest. If no replica matches, quarantine the object and alert; never overwrite the last known good copy.
9. Deletion uses a PostgreSQL tombstone/outbox, deletes all three provider copies idempotently and records receipts. Do not equate CDN purge with object deletion.

## Operational health and metrics

Expose private authenticated health/metrics for provider reachability, replication queue depth, oldest job age, lease expiry, retry/dead-letter counts, bytes pending per provider, replication throughput, verification latency, verified replica count, degraded assets, checksum mismatches, restore/repair results, capacity, rate limits, deletion tombstone age and outstanding provider receipts.

Readiness must fail if required database or provider configuration is invalid. Liveness must not perform expensive full bucket scans. Do not report a provider as healthy solely because DNS resolves or credentials parse.

## Staging acceptance runbook

1. Provision isolated private buckets for VPS MinIO, R2 and B2; enable encryption and block public access.
2. Validate each endpoint with a unique synthetic object and a provider-specific least-privilege key.
3. Upload, HEAD, stream GET, compute SHA-256, compare byte length/content type, and delete the synthetic object on each provider.
4. Run the full three-copy flow through Backend's authenticated upload/complete API; verify three receipts in PostgreSQL.
5. Disable one provider and confirm asset remains non-ready, retries are bounded, and alerting works.
6. Restore provider access and confirm worker recovers expired leases and completes the missing replica.
7. Corrupt or remove one staging replica and confirm checksum detection and repair from a verified good copy.
8. Exercise public/private CDN delivery, viewer-bound assertions, cache bypass, Range/HEAD and HLS/DASH.
9. Test delete tombstones, retry after provider outage, and verify all three copies are absent before final deletion state.
10. Perform a restore drill from B2 and R2 into a fresh VPS MinIO namespace; verify checksum and canonical ownership before cutover.

## Guardrails

- Do not add a Cloudflare Worker route to media.testagram.site; the Go CDN owns that hostname.
- Do not let the Go CDN speak directly to S3 providers until an explicit SigV4/private-origin adapter is implemented and tested.
- Do not expose B2/R2 presigned URLs as stable public media URLs.
- Do not enable fresh-media feature flags or change the active media hostname until staging acceptance and explicit operator approval.
- Do not use Render.


## Private metadata sidecars

The dedicated Data Centre media path keeps Backblaze B2 as its media primary and Cloudflare R2 as its media replica. Once both media copies pass size/content-type/SHA-256 verification, it writes the same deterministic JSON sidecar into the dedicated private InstaBox VPS MinIO metadata bucket and Cloudflare R2 metadata bucket. Each sidecar is independently read back and SHA-256 checked before the media object becomes `protected`. Deletion removes the two sidecars alongside the B2/R2 media objects; reconciliation repairs missing sidecars for already protected media.

PostgreSQL remains authoritative for owner, lifecycle, checksum, quota and authorization. The sidecar is a recoverable metadata mirror, not an alternate authorization database. The VPS metadata key must be restricted to the single metadata bucket; do not reuse the MinIO root key.
