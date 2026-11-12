# Dedicated media service rollout

The media service is isolated from the feature-flag platform and node control-plane. It owns B2/R2 credentials and its own PostgreSQL operational database. Backend remains the authority for user identity, ownership, visibility and product authorization.

## Required environment

Provision these values in the VPS secret environment (not in source control):

- `MEDIA_DATABASE_URL`: dedicated TLS PostgreSQL database for media lifecycle state. Do not reuse `DATA_CENTRE_DATABASE_URL`.
- `FRESH_DATA_CENTRE_INTERNAL_TOKEN`: one independently generated 32+ character random secret, shared only by Backend and the media service.
- `B2_S3_ENDPOINT`, `B2_S3_REGION`, `B2_S3_BUCKET`, `B2_S3_ACCESS_KEY_ID`, `B2_S3_SECRET_ACCESS_KEY`: least-privilege B2 S3 credentials for the primary bucket.
- `CLOUDFLARE_R2_ENDPOINT`, `CLOUDFLARE_R2_REGION=auto`, `CLOUDFLARE_R2_BUCKET`, `CLOUDFLARE_R2_ACCESS_KEY_ID`, `CLOUDFLARE_R2_SECRET_ACCESS_KEY`: least-privilege R2 credentials for the replica bucket.
- `DATA_CENTRE_MEDIA_API_URL=http://media:8096`: private Docker-network address. Do not expose port 8096 publicly.

The B2 and R2 credentials must exist only in the `media` service environment. The Backend API receives only the Data Centre URL and shared service token. The token must never be supplied to Flutter, web clients, node agents or federation peers.

## Staged rollout

1. Keep `FRESH_MEDIA_ENABLED=false`.
2. Provision the dedicated media database and least-privilege provider keys. Verify the B2 and R2 bucket regions/endpoints in the provider consoles.
3. Configure the environment above, then start the isolated service profile:

   ```sh
   docker compose --profile media -f vps/docker-compose.fresh.yml up -d --build media
   ```

   Compose runs `media-migrate` before the service. Confirm `/healthz` and `/readyz` from inside the private service network.
4. Run a staging-only upload using the real Backend API. Confirm the B2 PUT succeeds, Backend's bounded gateway enforces size and content type, Data Centre independently GETs the object and computes SHA-256, R2 receives a full copy, and R2 read-back matches both size and SHA-256.
5. Test provider outages, worker restart during a lease, expired upload grants, repeated verify requests, delete retries, dead-letter behavior and object deletion in both stores.
6. Verify CDN origin authorization and Flutter playback against the protected replica. Verify public cache-miss behavior and private media denial/expiry.
7. Only after all tests pass, set `DATA_CENTRE_MEDIA_API_URL=http://media:8096`, `FRESH_DATA_CENTRE_INTERNAL_TOKEN` in Backend and media, and explicitly approve changing `FRESH_MEDIA_ENABLED=true` in the Backend deployment.

## Current implementation boundary

This service implements the five internal API routes, B2 primary upload grants and object verification, R2 replication/read-back, a PostgreSQL outbox with retry/dead-letter state, and idempotent deletion of both configured origins.

It does **not** yet provide:
- Instabox adapter (provider API, signing and consistency guarantees must first be confirmed).
- Periodic full-inventory replica reconciliation after successful protection.
- CDN authorization/origin integration or Flutter playback migration.
- Production provider smoke tests using the actual account credentials.

Do not treat an object as protected until its R2 read-back verification succeeds. Do not enable production media while any of these rollout gates remain open.
