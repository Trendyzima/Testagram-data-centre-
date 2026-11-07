# Testagram media storage: Backblaze B2 + Cloudflare R2

## Ownership

The Data Centre repository owns media durability orchestration, replica placement,
node registration/capacity, checksum reconciliation, backup and restore drills.
The Go Backend remains the only public API and authorization boundary. XClone
never receives provider credentials.

The object-storage layout is:

| Role | Provider | Responsibility |
|---|---|---|
| Durable primary | Backblaze B2 | Canonical original uploads and durable source objects |
| Delivery/replica | Cloudflare R2 | Verified replica for Cloudflare-side delivery and reduced origin egress |
| Optional node replicas | Registered Android/Linux nodes | Additional verified copies or explicitly disposable caches; never the only copy |
| Metadata and ownership | Neon via Backend | Object key, owner, visibility, checksum, size, provider state and replica ledger |
| Identity and storage metadata collections | Appwrite | Identity integration and documented metadata/indexes only; not the binary media store |

B2 and R2 are both S3-compatible, but their endpoints, regions, credentials,
buckets and operational semantics must remain independently configured. Never
reuse access keys between providers. Use least-privilege keys scoped to the
required buckets and operations.

## Upload and replication contract

1. XClone requests an upload intent from Backend. Backend authenticates the
   Appwrite session, authorizes the owner and visibility, enforces quotas, and
   creates a pending object record in Neon.
2. Backend issues a short-lived, object-scoped upload authorization for the
   B2 primary. Provider credentials remain server-side.
3. The client uploads the object to B2. Backend/Data Centre verifies the
   object exists and matches the expected byte length and SHA-256 checksum
   before marking the primary copy verified.
4. A durable outbox event schedules replication to R2. The worker claims the
   event transactionally, copies the exact object, then reads back or otherwise
   cryptographically verifies the R2 copy before acknowledging the event.
5. Only after primary verification is the media considered stored. Delivery
   prefers the verified R2 copy when policy permits; otherwise it may use the
   verified B2 primary. Never publish a URL for an unverified object.
6. Node replicas are optional additional copies. A node must prove its mounted
   storage identity, available quota, object checksum and heartbeat health.
   Reported disk capacity alone is not replica evidence.
7. Periodic reconciliation compares Neon replica-ledger state with provider
   object metadata and node attestations. Missing/corrupt replicas are retried
   and repaired from a verified copy; permanent failures enter a dead-letter
   queue and alert operators.

## Object identity and security

- Use opaque, non-user-controlled object keys; do not put email addresses or
  other personal data in bucket paths.
- Store SHA-256, byte length, media type, owner, visibility, created time and
  per-provider verification state in the canonical metadata ledger.
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
not the node control plane: the control plane manages node enrollment and
capacity, not public upload authorization. Data Centre's node agent must only
receive node-scoped credentials or one-time transfer capabilities when the
verified replica-transfer worker is implemented.

## Release gates — not yet satisfied

The presence of environment variables is configuration, not proof of storage
integration. Keep media disabled until all of these pass in staging:

- B2 bucket access, upload, read-back, checksum and delete tests.
- R2 bucket access, upload, read-back, checksum and delete tests.
- B2-to-R2 replication with idempotency, retry, backoff and dead-letter tests.
- Provider outage tests proving no unverified object is published.
- Neon ledger/outbox transaction tests and crash recovery between copy and ack.
- Android removable-SD and Linux node transfer tests with signed receipts.
- Replica corruption, missing-object repair and restore-from-each-verified-copy tests.
- Private-media authorization, CDN cache isolation and URL-expiry tests.
- End-to-end upload, feed rendering, download and playback tests.
- Independent encrypted backup and measured restore RPO/RTO.

Until those gates pass, `FRESH_MEDIA_ENABLED=false` and retention remains
disabled with dry-run enabled. Do not claim production media readiness.
