# Legacy identity-evidence replica retirement

## Target boundary

The live application path for identity-verification photos/documents is exclusively the dedicated private Backblaze B2 identity-evidence bucket. No new identity object or metadata sidecar may be written to Cloudflare R2, InstaBox MinIO, general-media buckets or the CDN.

The B2-only source change prevents new R2/MinIO writes and prevents application-level R2 replica reads/restores. Migration `004_identity_evidence_b2_only.sql` clears stale replica verification timestamps and dead-letters queued legacy R2-to-B2 restore jobs so they cannot silently execute.

## Important: historical bytes are a separate cleanup gate

Changing source code or clearing `replica_verified_at` does not delete object bytes that may already exist in a legacy R2 or InstaBox identity bucket. Do not claim the storage migration is fully accepted until this inventory and retirement procedure has evidence.

## Read-only inventory first

1. Identify legacy identity bucket names from the previous protected deployment configuration and Cloudflare account inventory. Do not guess bucket names or reuse the current general-media R2 bucket.
2. Inventory object count, total bytes, object keys, content types, sizes and provider checksums from each legacy identity bucket. Keep object keys and applicant identifiers out of CI logs and public artifacts.
3. Query `media_objects` for `identity-evidence-private` rows and build a private, access-controlled mapping from each object key to its canonical size and SHA-256.
4. For every legacy R2/MinIO object, confirm the corresponding dedicated B2 identity object exists and matches exact size and SHA-256. Independently verify B2 read-back; a database timestamp alone is not proof.
5. Stop if any legacy object is missing from B2, has a mismatched checksum/size/content type, has no canonical database row, or cannot be mapped unambiguously. Keep it quarantined and resolve through an audited operator recovery that writes only to the dedicated B2 bucket. Never restore it through an R2 fallback in the application.

## Destructive retirement

Only after the read-only inventory proves every legacy object has an independently verified B2 copy:

1. Obtain explicit approval from the data owner and a second independent security reviewer for the exact legacy bucket(s), object count, byte count and signed inventory-manifest digest.
2. Take and verify the approved encrypted recovery snapshot required by the retention policy. The snapshot must remain access-controlled and must not become an application serving replica.
3. In a separately authorized maintenance operation, delete only the inventoried legacy identity objects from the legacy R2/MinIO identity bucket(s). Do not touch general-media buckets, R2 metadata sidecars or ordinary user media.
4. Re-list the exact legacy buckets and verify zero remaining identity objects; verify the dedicated B2 objects still pass read-back SHA-256 checks.
5. Retain the signed before/after manifests, operator approvals, deletion counts and audit event IDs in the restricted operations evidence store. Remove legacy bucket credentials and deployment variables after the purge is independently verified. Delete an empty legacy bucket only under the same approval.

## Acceptance evidence

- [ ] Data Centre migration 004 applied to the target media database.
- [ ] No identity evidence writes or serving fallbacks to R2, InstaBox MinIO, general-media buckets or CDN in deployed source/configuration.
- [ ] Read-only inventory of every legacy identity bucket completed with object/byte counts.
- [ ] Every retained evidence object independently verified in the dedicated B2 identity bucket.
- [ ] Approved legacy R2/MinIO identity objects purged and absence re-verified, or authoritative evidence that the legacy buckets never contained identity objects.
- [ ] Dedicated B2 upload, verify, status, GET/HEAD and delete staging acceptance passed.
- [ ] Backend reviewer flow and Flutter identity journey pass authenticated staging acceptance.

No production bucket, object or database changes are performed by this runbook automatically. Production purge remains a separately approved manual operation.
