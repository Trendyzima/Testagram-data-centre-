# Testagram Data Centre API contract

Data Centre owns private infrastructure APIs, storage nodes, media processing, notification delivery, VPS/control-plane operations, backups and operations. It does not own product identity, posts, social graph, media ownership, privacy or user-facing authorization.

## Implemented private platform API

Private listener defaults to `:8095` and is attached to the private platform network.

| Route | Method | Authentication | Implemented behavior |
|---|---|---|---|
| `/healthz` | GET | Private network | Process liveness |
| `/readyz` | GET | Private network | PostgreSQL readiness |
| `/metrics` | GET | Private network | Platform counters |
| `/v1/config/set` | POST | Platform bearer token | Write a versioned feature/config flag |
| `/v1/config/{key}` | GET | Public/private visibility is enforced by stored flag | Read an explicitly public config flag |
| `/v1/events` | POST | Platform bearer token | Store bounded platform event metadata |
| `/v1/audit` | POST | Platform bearer token | Store audit metadata |

The current platform server does **not** register the previously documented cache, rate-limit, or queue routes. Do not build clients against `/v1/cache/*`, `/v1/rate-limit/*`, or `/v1/queue/*` until handlers, schemas, authentication, persistence and tests are implemented. The current Backend mainline also does not contain a verified typed Data Centre platform client.

## Media placement boundary

Backend remains authoritative for owner, visibility, quota, retention and lifecycle. Data Centre may provide infrastructure placement and replica evidence only. A placement is not durable until byte count and SHA-256 have been independently read back and verified. Evidence should include object ID, provider/node, object key, bytes, checksum, replica state, request ID and verification timestamp.

The authenticated Backend <-> Data Centre media placement/replica API is not yet implemented. Do not advertise planned reserve/replicate/repair routes as live. Until that contract is implemented and tested, Backend's configured S3-compatible storage adapter remains the active media path. Android Node B is removable-SD storage and must not be treated as a sole durable copy.

## Backend, Algorithm and Federation deployment wiring

The current Backend self-hosted production Compose file builds `Dockerfile`, which runs `cmd/api`. That runtime expects:

- `ALGORITHM_URL=http://algorithm:8092` and `ALGORITHM_INTERNAL_TOKEN`, matching Algorithm's `INTERNAL_TOKEN`.
- `FEDERATION_INTERNAL_URL=http://federation-api:8081` and `FEDERATION_INTERNAL_TOKEN`, matching Federation's dedicated `FEDERATION_INGRESS_TOKEN`.
- `FEDERATION_BACKEND_INTERNAL_TOKEN`, matching Federation's `BACKEND_INTERNAL_TOKEN`, for Federation -> Backend actor/readiness calls.

These are two separate service credentials for the two directions. Do not reuse Appwrite user JWTs or expose any of these values to Flutter/browser builds.

The separate `Dockerfile.fresh` / `cmd/fresh-api` runtime uses a different set of environment names and a different service wiring contract. Do not combine `ALGORITHM_ENDPOINT` / `FRESH_FEDERATION_INTERNAL_TOKEN` with the `cmd/api` deployment. Select one backend binary and configure its matching environment contract.

The Data Centre overlay deploys Algorithm and Federation on the shared private platform network. Backend must be attached to that same network, and its private URLs must resolve there. Federation delivery, inbox and outbox-poller workers remain disabled by default; the Backend transactional outbox dispatcher is a separate controlled path and does not authorize enabling remote federation delivery.

## Security and operations

- Keep the platform listener on the private network; never publish its port to the public Internet.
- Use distinct high-entropy credentials per service direction and rotate them independently.
- Bound payload sizes, timeouts, concurrency, queue claims and retry attempts.
- Make placement, verification and repair idempotent.
- Never interpret a missing heartbeat as proof of deletion.
- Never delete media or reclaim quota without a Backend-authorized lifecycle command and independent replica checks.
- Keep node credentials, provider keys and storage secrets out of client builds and logs.
