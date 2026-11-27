# Data Centre media provider staging acceptance

This manual workflow exercises the Data Centre media service against dedicated non-production Backblaze B2 and Cloudflare R2 buckets plus an ephemeral PostgreSQL lifecycle database. It does not deploy the service or change the VPS. The workflow and sidecar assertions are introduced by PR #133; merge that PR before dispatching the workflow from the default branch.

## GitHub Actions environment

Create or reuse the protected `media-staging` environment. Use dedicated S3-compatible keys restricted to the six staging buckets listed below. Never copy production credentials into this environment.

Environment variables:
- `MEDIA_STAGING_B2_ENDPOINT`
- `MEDIA_STAGING_B2_REGION`
- `MEDIA_STAGING_B2_PRIVATE_BUCKET`
- `MEDIA_STAGING_B2_IDENTITY_BUCKET`
- `MEDIA_STAGING_R2_ENDPOINT`
- `MEDIA_STAGING_R2_REGION`
- `MEDIA_STAGING_R2_PRIVATE_BUCKET`
- `MEDIA_STAGING_R2_PUBLIC_BUCKET`
- `MEDIA_STAGING_R2_IDENTITY_BUCKET`
- `MEDIA_STAGING_R2_METADATA_BUCKET`

Environment secrets:
- `MEDIA_STAGING_B2_ACCESS_KEY_ID`
- `MEDIA_STAGING_B2_SECRET_ACCESS_KEY`
- `MEDIA_STAGING_R2_ACCESS_KEY_ID`
- `MEDIA_STAGING_R2_SECRET_ACCESS_KEY`
- `MEDIA_STAGING_INTERNAL_TOKEN`
- `MEDIA_STAGING_IDENTITY_EVIDENCE_INTERNAL_TOKEN`

The media and identity-evidence service tokens must be different and at least 32 characters. Each endpoint must use HTTPS. Each bucket name must explicitly contain `ci`, `staging` or `smoke`, must not contain `prod` or `production`, and the identity-evidence bucket must be distinct from the ordinary media bucket for that provider, and the R2 metadata bucket must be distinct from R2 private, public and identity-evidence buckets.

## Run

1. Confirm the six cloud buckets are dedicated staging resources and contain only disposable test data.
2. Open **Actions → Data Centre media provider staging acceptance → Run workflow**.
3. Set `confirm_staging_provider_writes=true` only after verifying the selected environment and bucket names.
4. Inspect the run summary and confirm all three tests passed:
   - provider round-trip: PUT, GET, HEAD, exact content type/length/SHA-256 and deletion;
   - private MinIO and R2 metadata sidecar write/read-back SHA-256 verification and deletion;
   - durable B2-to-R2 replication, simulated B2 primary loss and verified restoration from R2, then two-provider tombstone deletion;
   - separately authenticated identity-evidence lifecycle, token-family separation, cross-application denial, read-back integrity, primary restoration and deletion.

The workflow creates a disposable PostgreSQL database and applies the Data Centre media migrations. Test-generated objects and database rows are cleaned by the test harness. A failed cleanup must be treated as an outstanding staging incident and investigated before rerunning.

## Scope boundary

This proves the Data Centre media service's B2/R2 provider and PostgreSQL lifecycle code on the workflow's exact source SHA. It does not prove the Backend's deployed DATA_CENTRE_MEDIA_API_URL, the production-like private network, MinIO metadata sidecars, the video worker, Go CDN authorization/cache behavior, or Flutter playback. Those remain separate same-deployment acceptance gates.

The Data Centre media service stores media bytes in B2 and R2; its VPS MinIO and R2 metadata sidecars are metadata mirrors, not a third media-byte copy. The staging workflow now verifies the metadata sidecar path with an ephemeral MinIO bucket and the dedicated R2 metadata bucket. Do not count these tests as proof of a three-byte-copy media topology.
