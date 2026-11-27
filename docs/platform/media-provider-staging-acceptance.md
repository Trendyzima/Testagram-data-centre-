# Data Centre media provider staging acceptance

This manual workflow exercises the Data Centre media service against dedicated non-production Backblaze B2 and Cloudflare R2 buckets plus an ephemeral PostgreSQL lifecycle database. It does not deploy the service or change the VPS. The workflow and sidecar assertions are introduced by PR #133; merge that PR before dispatching the workflow from the default branch.

## GitHub Actions environment

Create or reuse the protected `media-staging` environment. Use dedicated S3-compatible keys restricted to the general-media and identity staging buckets listed below. Never copy production credentials into this environment.

Environment variables:
- `MEDIA_STAGING_B2_ENDPOINT`
- `MEDIA_STAGING_B2_REGION`
- `MEDIA_STAGING_B2_PRIVATE_BUCKET`
- `MEDIA_STAGING_B2_IDENTITY_BUCKET`
- `MEDIA_STAGING_R2_ENDPOINT`
- `MEDIA_STAGING_R2_REGION`
- `MEDIA_STAGING_R2_PRIVATE_BUCKET`
- `MEDIA_STAGING_R2_PUBLIC_BUCKET`
- `MEDIA_STAGING_R2_METADATA_BUCKET`

Environment secrets:
- `MEDIA_STAGING_B2_ACCESS_KEY_ID`
- `MEDIA_STAGING_B2_SECRET_ACCESS_KEY`
- `MEDIA_STAGING_R2_ACCESS_KEY_ID`
- `MEDIA_STAGING_R2_SECRET_ACCESS_KEY`
- `MEDIA_STAGING_INTERNAL_TOKEN`
- `MEDIA_STAGING_IDENTITY_EVIDENCE_INTERNAL_TOKEN`

The media and identity-evidence service tokens must be different and at least 32 characters. Each endpoint must use HTTPS. B2 and R2 must also resolve to distinct hostnames; a single provider endpoint cannot satisfy both sides of the general-media replication test. Each configured bucket name must explicitly contain `ci`, `staging` or `smoke` and must not contain `prod` or `production`. The dedicated B2 identity-evidence bucket must be distinct from the B2 general-media bucket. R2 has no identity-evidence bucket; its metadata bucket must be distinct from its private and public general-media buckets.

## Run

1. Confirm the five cloud buckets are dedicated staging resources and contain only disposable test data: B2 general media, B2 identity evidence, R2 private media, R2 public media and R2 metadata.
2. Open **Actions → Data Centre media provider staging acceptance → Run workflow**.
3. Set `confirm_staging_provider_writes=true` only after verifying the selected environment and bucket names.
4. Inspect the run summary and confirm all three tests passed:
   - provider round-trip: PUT, GET, HEAD, exact content type/length/SHA-256 and deletion;
   - private MinIO and R2 metadata sidecar write/read-back SHA-256 verification and deletion;
   - durable B2-to-R2 replication, simulated B2 primary loss and verified restoration from R2, then two-provider tombstone deletion;
   - separately authenticated identity-evidence lifecycle exclusively in the dedicated B2 identity bucket, token-family separation, cross-application denial, read-back integrity, B2-only download and deletion. No identity evidence is copied to R2 or InstaBox.

The workflow creates a disposable PostgreSQL database and applies the Data Centre media migrations. Test-generated objects and database rows are cleaned by the test harness. A failed cleanup must be treated as an outstanding staging incident and investigated before rerunning.

## Scope boundary

This proves the Data Centre media service's B2/R2 provider and PostgreSQL lifecycle code on the workflow's exact source SHA. It does not prove the Backend's deployed DATA_CENTRE_MEDIA_API_URL, the production-like private network, MinIO metadata sidecars, the video worker, Go CDN authorization/cache behavior, or Flutter playback. Those remain separate same-deployment acceptance gates.

The Data Centre media service stores general-media bytes in B2 and R2; identity-evidence bytes are stored only in the dedicated B2 identity bucket. Its VPS MinIO and R2 metadata sidecars are metadata mirrors, not a third general-media-byte copy. The staging workflow now verifies the metadata sidecar path with an ephemeral MinIO bucket and the dedicated R2 metadata bucket. Do not count these tests as proof of a three-byte-copy media topology.
