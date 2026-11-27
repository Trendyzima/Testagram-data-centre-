# Canonical Backend VPS deployment

This is the single VPS deployment contract for the Testagram canonical Go
Backend. The API image is published by
`Trendyzima/Testagram-backend-/.github/workflows/container-release.yml` as
`ghcr.io/trendyzima/testagram-backend`. Pin the exact published image digest
in `TESTAGRAM_BACKEND_IMAGE`; do not use the retired Fresh API image.

## Runtime contract

- `api`: canonical `cmd/api` runtime. Public HTTP listens on `:8080`;
  private readiness and internal service routes listen on `:8082`.
  The private listener is exposed only on Docker networks and is never routed
  through Caddy or published on the host.
- `edge`: Caddy terminates HTTPS and proxies `api.testagram.site` to
  `api:8080`; it proxies the node-control hostname to the loopback-bound
  control-plane service.
- `algorithm`: optional private ranking profile. It is not a login or API
  startup dependency; leave its URL/token unset until its image and credential
  are provisioned.
- `federation-api`: optional private Federation profile. Its calls into the
  Backend use `FEDERATION_BACKEND_INTERNAL_TOKEN`; Backend calls to the
  Federation ingress use the separate `FEDERATION_INTERNAL_TOKEN`.
- `media`: optional private Data Centre media service. Ordinary-media B2
  credentials, the separate bucket-scoped identity-evidence B2 credentials, and
  R2 credentials stay in this service and are not passed to the Backend API
  container. The identity-evidence B2 key must be restricted to its dedicated
  private bucket and must not be interchangeable with the ordinary-media key.
- `control-plane`: Data Centre-owned infrastructure state only, using
  `DATA_CENTRE_DATABASE_URL`, separate from the canonical product
  `DATABASE_URL`.

The canonical API requires `DATABASE_URL`, `TESTAGRAM_AUTH_JWT_SECRET`
(at least 32 bytes), and explicit `ALLOWED_ORIGINS`. Native authentication
is implemented by the canonical Backend; no separate Fresh API or external
identity-provider endpoint, database, server key or fallback environment is
part of this runtime.

## TV health monitor contract

The TV monitor endpoints are implemented in the canonical Backend:

- `GET /internal/tv/catalog`
- `POST /internal/tv/health`
- `GET /api/tv/health`

The catalogue read and health write endpoints validate a signed GitHub Actions
OIDC token in `X-GitHub-OIDC-Token`. The verifier is restricted to the
Testagram Flutter repository's `Testagram TV Health Monitor` workflow, the
`main` ref, the `testagram-tv-health` audience, and the permitted
`schedule`, `workflow_dispatch`, and `push` events. Do not replace this
with a static bearer token or expose the private `:8082` listener publicly.

The Backend readiness probe is `GET http://127.0.0.1:8082/readyz`; it checks
PostgreSQL connectivity. Public liveness is `GET /health` on port 8080.

## Provisioning and staging

1. Publish the canonical Backend image and resolve its immutable GHCR digest.
2. Provision the canonical product PostgreSQL database and a separate
   Data Centre infrastructure PostgreSQL database. Set them respectively as
   `DATABASE_URL` and `DATA_CENTRE_DATABASE_URL`.
3. Provision a stable random native-auth signing secret of at least 32 bytes.
   Keep it stable across deploys and include it in the protected backup/recovery
   process.
4. Set exact Flutter/web HTTPS origins in `ALLOWED_ORIGINS`, and set
   `AUTH_PUBLIC_BASE_URL` to the canonical public auth origin.
5. Pin Backend, Caddy and Postgres client images to immutable SHA-256 digests.
   Provision control-plane and node bootstrap tokens (at least 32 characters).
6. Copy `vps/fresh.env.example` to the VPS-local `vps/fresh.env`, set file
   permissions to 0600, and populate every required value. Never commit the
   runtime file or print secrets in CI logs.
7. Start the Backend self-hosted stack first. It owns the shared
   `testagram-platform` and `testagram-ranker` networks. Then run
   `sudo bash vps/install.sh`; the installer validates Compose rendering
   before starting the node profile and node agent.
8. Verify `/health` externally, `:8082/readyz` only from a private network,
   and verify the actual TV monitor workflow can read the catalogue and
   persist a bounded health batch. Confirm unauthenticated and wrong-workflow
   OIDC requests are rejected.
9. Verify Backend-to-Federation and Backend-to-media authentication only when
   those optional profiles are configured. Keep media disabled until B2/R2
   provider-backed write, hash/read-back, replication, delete and isolated
   restore tests pass.
10. Run database backup and isolated restore, rollback, auth lifecycle,
    frontend/API compatibility, TV monitor, security and load acceptance
    before any production cutover.

## Safety

The installer deploys the reviewed Compose file but does not authorize a
production cutover. Do not change DNS, rotate production credentials, migrate
production data, enable destructive retention, or activate media replication
as part of this configuration migration. Keep the previous deployment
configuration and a verified restore point until the new runtime has passed
staging acceptance.
