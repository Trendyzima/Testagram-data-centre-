# Testagram media storage: Backblaze B2 + Cloudflare R2

## Ownership

The Data Centre repository owns deployment, node registration/capacity,
node-level replica placement, B2/R2 provider operations, signed storage URLs,
checksum verification, physical deletion and replica recovery. The Go Backend
remains the only public product API and authorization boundary; it owns user
permissions, upload quotas and canonical media metadata, and calls the private
Data Centre media control plane. XClone and node agents never receive long-lived
provider credentials.

The object-storage layout is:

| Role | Provider | Responsibility |
|---|---|---|
| Durable primary | Backblaze B2 | Canonical original uploads and durable source objects |
| Delivery/replica | Cloudflare R2 | Verified replica for Cloudflare-side delivery and reduced origin egress |
| Optional node replicas | Registered Android/Linux nodes | Additional verified copies or explicitly disposable caches; never the only copy |
| Media metadata and quotas | Appwrite via Backend | Current fresh runtime stores upload reservations, media ownership, quotas and provider replica state in private Appwrite collections; Neon is the target social data plane, not yet the media ledger |

B2 and R2 are both S3-compatible, but their endpoints, regions, credentials,
buckets and operational semantics must remain independently configured. Never
reuse access keys between providers. Use least-privilege keys scoped to the
required buckets and operations.

## Upload and replication contract

1. XClone requests an upload intent from Backend. Backend authenticates the
   Appwrite session, authorizes the owner and visibility, enforces quotas, and
   creates a pending object record and quota reservation in Appwrite.
2. Backend requests a short-lived, tenant-scoped B2 upload URL from the private
   Data Centre API. The signed URL remains server-side; XClone uploads through
   Backend's bounded gateway.
3. Backend submits the expected size, content type and SHA-256 to Data Centre.
   Data Centre reads back the B2 object, queues B2-to-R2 replication, verifies
   both checksums and reports `protected` only after the replica is confirmed.
4. Backend currently copies the verified B2 object to R2 synchronously in the
   upload-completion request, reads the R2 object back and verifies size, type
   and SHA-256 before committing the protected media/replica records in Appwrite.
5. Only after both cloud copies are verified is the media marked protected.
   Delivery prefers R2, with B2 as the verified fallback. Never publish a URL
   for an unverified object.
6. Node replicas are optional additional copies. A node must prove its mounted
   storage identity, available quota, object checksum and heartbeat health.
   Reported disk capacity alone is not replica evidence.
7. Backend's current `RepairObject` path compares provider objects against
   the stored SHA-256 and repairs a missing/corrupt cloud replica from the
   surviving verified copy. A durable scheduled reconciliation queue and
   dead-letter workflow remain required before production scale.

## Object identity and security

- Use opaque, non-user-controlled object keys; do not put email addresses or
  other personal data in bucket paths.
- Store SHA-256, byte length, media type, owner, visibility, created time and
  per-provider verification state in the current Appwrite media metadata ledger.
- Validate MIME type from file content, enforce upload size limits and reject
  path traversal or caller-supplied bucket/key overrides.
- Keep buckets private. Use short-lived signed requests or Backend-proxied
  authorization; never expose S3 access keys to browsers, Android nodes or
  desktop nodes.
- Encrypt transport with TLS. Keep provider secrets in the deployment secret
  store and rotate them independently.
- Treat ETags as provider-specific metadata, not as a portable SHA-256 digest.
  Multipart uploads must be completed and verified with a real SHA-256 checksum.
- Do not let a node self-assign object ownership or mark its own replica verified.
  Backend/Data Centre must verify transfer receipts and checksum evidence.

## Retention, deletion and recovery

The currently specified product policy is: normal posts are retained forever;
stories expire after 24 hours. Expiry and deletion must be enforced by a
Backend-owned, auditable lifecycle job and propagated to every provider and
replica only after the policy and legal-hold checks pass.

Retention remains disabled and dry-run in the fresh deployment. No destructive
provider lifecycle rule, bucket-wide cleanup, node wipe or quota reclamation
may be activated as part of this storage cutover. A deleted object is eligible
for physical reclamation only after the canonical tombstone is committed,
replica deletion is acknowledged, retry/dead-letter handling is complete and
the configured recovery window has elapsed.

Backups must be independent of the active B2/R2 pair. A restore drill must
recover metadata and object bytes, verify SHA-256 for restored objects, and
exercise private-media authorization and playback. A database-only restore or
a successful bucket listing is not a complete media recovery test.

## Runtime configuration

The fresh Data Centre deployment passes the following server-side settings to
the Backend media runtime:

- `B2_S3_ENDPOINT`, `B2_S3_REGION`, `B2_S3_BUCKET`,
  `B2_S3_ACCESS_KEY_ID`, `B2_S3_SECRET_ACCESS_KEY`
- `CLOUDFLARE_R2_ENDPOINT`, `CLOUDFLARE_R2_REGION`,
  `CLOUDFLARE_R2_BUCKET`, `CLOUDFLARE_R2_ACCESS_KEY_ID`,
  `CLOUDFLARE_R2_SECRET_ACCESS_KEY`

The credentials are intentionally injected into the Backend media service,
not the node control plane: the Backend currently performs provider uploads,
replication, integrity checks and private URL signing. The control plane manages
node enrollment and capacity, not public upload authorization. Data Centre's
node agent must only receive node-scoped credentials or one-time transfer
capabilities when verified node-level object transfer is implemented.

## Release gates — not yet satisfied

The presence of environment variables is configuration, not proof of storage
integration. Keep media disabled until all of these pass in staging:

- Real B2 and R2 bucket access, upload, read-back, checksum and delete tests using isolated staging buckets (the Backend currently has mocked provider tests).
- Crash/retry testing of the current synchronous B2-to-R2 completion path; durable outbox-driven replication, bounded retries and dead-letter recovery remain future work.
- Provider outage tests proving no unverified object is published.
- Reconciliation of Appwrite media/replica metadata with both provider buckets; migrate this ledger to Neon only as part of an explicit tested data-plane cutover.
- Android removable-SD and Linux node transfer tests with signed receipts; node-to-provider object transfer is not yet implemented.
- Replica corruption, missing-object repair and restore-from-each-verified-copy tests.
- Private-media authorization, CDN cache isolation and URL-expiry tests.
- End-to-end upload, feed rendering, download and playback tests.
- Independent encrypted backup and measured restore RPO/RTO.

Until those gates pass, `FRESH_MEDIA_ENABLED=false` and retention remains
disabled with dry-run enabled. Do not claim production media readiness.


## Data Centre media control plane

The implementation branch adds a private control-plane API and durable PostgreSQL
ledger. Upload URLs target the B2 primary; completion queues an asynchronous job
that reads back and hashes the B2 object, copies it to R2, then verifies the R2
checksum before marking the object protected. Backend polls the status endpoint
and exposes media only after the protected state is reached. Failed transfers use
leased retries and a dead-letter state; deletion is retried until both replicas
are removed.

The service is disabled unless `MEDIA_STORAGE_ENABLED=true` and all four
private/public B2/R2 buckets plus independent credentials are configured.
