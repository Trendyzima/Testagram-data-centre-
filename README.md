# Testagram Data Centre

The Data Centre owns Testagram's storage nodes, media processing, notification
delivery, VPS/control-plane operations, backups and infrastructure services.

## Android storage node

GitHub Actions builds the Android node on relevant pushes and pull requests.
Download the latest successful `testagram-android-node` artifact from:

https://github.com/Trendyzima/Testagram-data-centre-/actions/workflows/vps-ci.yml

The artifact contains debug/release APKs when signing secrets are configured,
SHA-256 checksums and an optional ADB installer. A stable release signing key
must be retained for updates; Play Protect behavior cannot be guaranteed.

The Android node is storage-only. It uses a user-selected removable SD card,
reports capacity/health, and does not transcode video on-device. FFmpeg runs on
a separate Linux worker.

## Runtime ownership

- Appwrite issues client sessions. Video and notification APIs validate
  Appwrite JWTs with the Appwrite Account API and resolve canonical Testagram
  UUIDs through the shared PostgreSQL identity bridge.
- PostgreSQL stores Data Centre platform configuration, audit/telemetry,
  storage metadata and infrastructure control-plane state.
- Object storage and node agents own byte placement, checksums, replicas and
  capacity reporting.
- Video API/worker own upload validation, FFmpeg processing and playback.
- Notification API owns device registrations, preferences and delivery state.
- The Backend remains the authority for product accounts, posts, privacy and
  media-to-post authorization.

No Supabase, Redis/Valkey or Upstash runtime is required. Rate limits use
PostgreSQL counters; durable asynchronous work uses service-owned PostgreSQL
outboxes and idempotent workers.

## Deployment status

- `backend/docker-compose.yml` is the Data Centre production overlay. It deploys
  the Algorithm ranker, private Federation API, platform API, video API/worker,
  notification API and maintenance worker on the stable private networks.
  Algorithm and Federation stay private; public federation routing and delivery
  workers remain disabled until their end-to-end production gates pass.
- Start the Backend repository's self-hosted Compose stack first. It owns local
  PostgreSQL, MinIO, the public Caddy edge and creates the shared networks.
  The Data Centre overlay connects to that same PostgreSQL database; it does
  not start a second database or reverse proxy.
- Before starting Data Centre services, run its checksummed, forward-only
  migration runner against the Backend-owned database. It records each applied
  SQL file and refuses to silently reuse a migration whose contents changed.
- From the Data Centre repository root, with `backend/.env` configured:

```sh
docker compose --env-file backend/.env -f backend/docker-compose.yml config
docker compose --env-file backend/.env -f backend/docker-compose.yml run --rm migrate
docker compose --env-file backend/.env -f backend/docker-compose.yml up -d --build
docker compose --env-file backend/.env -f backend/docker-compose.yml ps
```

- `vps/docker-compose.fresh.yml` remains a separate Appwrite/Neon/B2/R2
  preview and is not the portable deployment path. Use the Backend base stack
  plus this repository's `backend/docker-compose.yml` overlay for the
  PostgreSQL/MinIO deployment. End-to-end Compose, Appwrite and media restore
  tests remain release gates.

## Federation service credentials

The Federation container receives two independent service credentials from
`backend/.env`:

- `FEDERATION_INTERNAL_TOKEN` authenticates Backend-to-Federation ingress and is injected as
  `FEDERATION_INGRESS_TOKEN`.
- `FEDERATION_BACKEND_INTERNAL_TOKEN` authenticates Federation-to-Backend actor/readiness requests and is injected as
  `BACKEND_INTERNAL_TOKEN`.

Generate both values independently (at least 32 random characters each). The
Compose file requires both values and does not permit a shared legacy-token
fallback. Missing either secret must prevent the corresponding private
integration from authenticating. Keep public federation and delivery workers
disabled until both directions are verified in staging.

## Backups and restore

The VPS backup scripts currently cover the Data Centre infrastructure database
and optional node-storage archive. They do not by themselves back up the
Backend product database, all media objects, Federation state or Appwrite
configuration. A full restore drill must include those systems before
production recovery is declared ready.

## Forensic end-to-end gate

The required physical path remains:

**real Android storage → real MP4 upload → signed object transfer → real FFmpeg HLS → reachable Testagram API/frontend → actual playback**

Do not mark the path complete until a physical device supplies evidence for
every hop.

## Architecture references

- [Service catalogue](docs/platform/service-catalog.md)
- [Self-sustaining operations and recovery plan](docs/platform/self-sustaining-operations.md)
- [Internal platform OpenAPI contract](docs/platform/openapi/internal-platform.yaml)
- [Hosting control-plane OpenAPI contract](docs/platform/openapi/hosting-control-plane.yaml)
- [Media storage architecture](docs/platform/media-storage-architecture.md)\n- Instabox VPS + Cloudflare R2 + Backblaze B2 target: three verified media copies; Testagram CDN owns public delivery.
- [Fresh deployment preview](vps/FRESH-DEPLOYMENT.md)
