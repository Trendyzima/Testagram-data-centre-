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

- The Backend issues Testagram-native short-lived access tokens. Video and notification APIs verify the shared HS256 contract and live PostgreSQL session state; no external identity provider is used.
- PostgreSQL stores Data Centre platform configuration, audit/telemetry,
  storage metadata and infrastructure control-plane state.
- Object storage and node agents own byte placement, checksums, replicas and
  capacity reporting.
- Video API/worker own upload validation, FFmpeg processing and playback.
- Notification API owns device registrations, preferences and delivery state.
- The Backend remains the authority for product accounts, posts, privacy and
  media-to-post authorization.

Rate limits use PostgreSQL counters; durable asynchronous work uses
service-owned PostgreSQL outboxes and idempotent workers.

## Deployment status

- `backend/docker-compose.yml` is the Data Centre Compose overlay. It defines
  the Algorithm ranker, private Federation API, platform API, video API/worker,
  notification API and maintenance worker on the stable private networks. A
  Compose definition is not evidence that these services are deployed or healthy.
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

- `vps/docker-compose.yml` is the supported VPS deployment path. It uses
  the canonical Testagram Backend image and Testagram-native PostgreSQL
  identity/session authority; no external identity-provider runtime or
  authentication fallback is part of the supported deployment. Use the Backend base stack
  plus this repository's `backend/docker-compose.yml` overlay for the
  PostgreSQL/MinIO deployment. End-to-end Compose, native-auth and media
  restore tests remain release gates.

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
Backend product database, all media objects or Federation state. A full restore
drill must include those systems before production recovery is declared ready.
No external identity-provider configuration is required by the native-auth
deployment or restore path.

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
- [Media storage architecture](docs/platform/media-storage-architecture.md)
- Media-byte durability target: Backblaze B2 primary plus Cloudflare R2 replica,
  each verified by independent read-back and SHA-256. Instabox MinIO stores
  metadata sidecars; it is not a third media-byte replica. Testagram CDN owns
  public delivery and cache policy.
- [Fresh deployment preview](vps/FRESH-DEPLOYMENT.md)



## Media lifecycle evidence and acceptance

The media service is deliberately opt-in under the Compose `media` profile and
Backend feature flags remain fail-closed by default. Its configured storage
roles are:

- Backblaze B2: primary media-byte origin.
- Cloudflare R2: independently verified media-byte replica and primary-restore
  source.
- Instabox-hosted MinIO: metadata-sidecar origin only; not a third media-byte
  copy.
- Cloudflare R2 metadata bucket: metadata-sidecar replica.

The code includes durable PostgreSQL media objects/outbox state, provider
read-back verification, reconciliation, restore and two-origin deletion
coverage. Unit/CI tests and an isolated PostgreSQL restore drill do not prove
that external B2/R2 buckets, a deployed Data Centre service, or a physical
storage node have passed a live acceptance run.

Before enabling the media profile or changing production flags, capture one
staging run tied to exact Backend and Data Centre image digests. The evidence
must include: authenticated upload grant; object upload; independent B2 and R2
read-back with matching byte count and SHA-256; durable replication state;
controlled primary-loss simulation; verified R2-to-B2 restoration; authenticated
delete; confirmed absence from both origins; metadata-sidecar cleanup; and
redacted correlation IDs. Never publish credentials, signed URLs, private
object keys, or user evidence in the artifact.

The repository CI's isolated restore drill uses disposable local PostgreSQL
fixtures. It validates the restore tooling and fixture lifecycle only; it is not
a restore of production data or an external object-provider replication test.

## Seven-repository Testagram integration

This repository is one component of a single Testagram product. The shared ownership map, runtime request flow, security boundaries, hostname ownership and release gates are maintained in the [Testagram API integration control plane](https://github.com/Trendyzima/testagram-api). That repository is build-time contract governance only; it is not a runtime gateway.

| Repository | Responsibility |
|---|---|
| [Flutter Client](https://github.com/Trendyzima/testagram-flutter-client-) | Native Android/web UI and public Backend transport |
| [Backend](https://github.com/Trendyzima/Testagram-backend-) | Canonical identity, sessions, authorization, PostgreSQL and public APIs |
| [Data Centre](https://github.com/Trendyzima/Testagram-data-centre-) | Private infrastructure, storage, processing, backups and restore |
| [CDN](https://github.com/Trendyzima/cdn-) | Authorized media byte delivery, edge cache and streaming transport |
| [Federated Content](https://github.com/Trendyzima/federated-content) | ActivityPub/WebFinger protocol state and remote delivery |
| [The Algorithm](https://github.com/Trendyzima/the-algorithm) | Private ranking of Backend-authorized candidates |
| [Testagram API](https://github.com/Trendyzima/testagram-api) | OpenAPI, private contracts, SDKs, immutable source pins and compatibility CI |

**This repository's boundary:** Private infrastructure, media processing, storage durability, replicas, notifications, backups and restore.

**Contract entry points:** [Data Centre media lifecycle contract](https://github.com/Trendyzima/testagram-api/blob/main/contracts/upstream/datacentre/media-lifecycle-v1.openapi.yaml) · [Service registry](https://github.com/Trendyzima/testagram-api/blob/main/contracts/registry/services.json)

**Shared integration references:** [Seven-repository ownership and request flow](https://github.com/Trendyzima/testagram-api/blob/main/docs/architecture/service-ownership.md) · [Locked end-to-end acceptance gates](https://github.com/Trendyzima/testagram-api/blob/main/docs/integration/locked-seven-repository-acceptance.md) · [Machine-readable service registry](https://github.com/Trendyzima/testagram-api/blob/main/contracts/registry/services.json)

Keep the ownership boundaries intact: Flutter calls only the public Backend API and approved media URLs; Backend remains the authority for user identity and product authorization; Data Centre owns storage mechanics; CDN transports bytes but does not authorize access; Federation owns protocol state but not local accounts or canonical posts; Algorithm ranks but does not authorize or hydrate product records. Never place private service credentials, database credentials or storage-provider keys in client code.
