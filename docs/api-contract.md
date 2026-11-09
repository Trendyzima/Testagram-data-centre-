# Testagram Data Centre API contract

Data Centre owns infrastructure APIs only: runtime operations, private platform cache/rate-limit/queue adapters, node enrollment/health, storage capacity and physical replica operations. It is not the product API and never owns local user identity, posts, social graph, media ownership or user-facing authorization.

## Current private platform API
Private platform listener (default `:8095`), internal network only:
- `GET /healthz`, `GET /readyz`, `GET /metrics`
- `GET /v1/cache/get?key=...`
- `POST /v1/cache/set`, `DELETE /v1/cache/delete?key=...`
- `POST /v1/rate-limit/check`
- `POST /v1/queue/enqueue`, `POST /v1/queue/claim`, `POST /v1/queue/ack`, `POST /v1/queue/reclaim`
- `POST /v1/config/set`, `GET /v1/config/{key}`
- `POST /v1/events`, `POST /v1/audit`

Every private operation requires the Data Centre platform bearer token. Do not publish the listener to the public Internet. Backend uses its typed `internal/platformclient`; browser clients never receive this credential.

## Media placement boundary
Backend remains the authority for owner, visibility, quota, retention and lifecycle. Data Centre provides infrastructure placement/replica evidence only. A media placement is not durable until size and SHA-256 have been independently read back and verified. Include object ID, provider/node, object key, bytes, checksum, replica state, request ID and verification timestamp.

The full authenticated Backend <-> Data Centre media placement/replica API is not yet implemented. Do not advertise planned reserve/replicate/repair routes as live. Until implemented and tested, Backend's configured storage adapter remains the active media path. Android Node B remains removable-SD-only and cannot be a sole durable copy.

## Security and operations
- Private network plus per-service high-entropy credentials; rotate independently.
- Bound payload sizes, timeouts, concurrency, queue claims and retry attempts.
- Idempotent placement, verification and repair operations.
- Never interpret missing heartbeat as proof of deletion.
- Never delete media or reclaim quota without Backend-authorized lifecycle command and independent replica checks.
- Keep node credentials, provider keys and storage secrets out of XClone and logs.


## Fresh API private-service wiring

The deployed Fresh Backend image is built from `Dockerfile.fresh` and runs `cmd/fresh-api`. Its service contract is different from the legacy `cmd/api` binary:

- Algorithm ranking: `ALGORITHM_ENDPOINT=http://algorithm:8092` plus `ALGORITHM_INTERNAL_TOKEN`. The Algorithm service is optional under the `ranking` Compose profile; Fresh API must retain chronological fallback when ranking is unavailable.
- Federation ingress/outbox: Data Centre passes the same high-entropy `FRESH_FEDERATION_INTERNAL_TOKEN` to Fresh API and Federation's `BACKEND_INTERNAL_TOKEN`. Fresh API exposes only private `/internal/v1/federation/*` routes with this token; Federation polls the Backend outbox and resolves actors through these routes. There is no public browser-to-Federation API.
- Data Centre platform cache/rate-limit/queue API is a separate infrastructure service. It is not currently a Fresh API dependency and its private token must never be exposed to XClone.

These are service-to-service settings, not browser API URLs. XClone calls only the public Fresh Backend API; it must never call Algorithm, Federation or Data Centre private platform endpoints directly.
