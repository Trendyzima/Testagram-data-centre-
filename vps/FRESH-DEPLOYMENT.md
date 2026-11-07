# Fresh Appwrite deployment (separate from the legacy VPS stack)

This stack is the clean Appwrite runtime. It does not attach the legacy Supabase
network, start the legacy cmd/api, or configure Supabase credentials.

## Services and trust boundaries

- api: Backend's dedicated Dockerfile.fresh image. Verifies Appwrite JWTs,
  serves the public /v1 API, owns authorization and calls private services.
- algorithm: private stateless ranker on an internal-only Docker network. Only
  Backend can reach it; its bearer token is not exposed to XClone. Backend has
  a second egress-capable network for Appwrite and object-provider access.
- migrate: applies only `001_vps.sql` and `006_hosting_control_plane.sql` to
  the dedicated Data Centre PostgreSQL database before control-plane starts.
  It deliberately does not apply product/video, notification, platform-event,
  or Supabase Storage migrations to this infrastructure database.
- control-plane: Data Centre infrastructure state only. Its PostgreSQL URL
  must point to a dedicated TLS database, not the legacy Supabase project.
- edge: Caddy terminates TLS and forwards api.testagram.site to Backend.
- Federation is intentionally not started or exposed. Keep
  `FRESH_FEDERATION_INTERNAL_TOKEN` blank until a trusted consumer exists; the
  private Backend outbox route is otherwise not mounted. Signature verification
  and durable Federation inbox/outbox delivery are not ready.

## Before deployment

1. Publish the fresh Backend image using Backend container-release.yml and pin
   the resulting GHCR digest in TESTAGRAM_FRESH_BACKEND_IMAGE.
2. Publish Algorithm and pin its digest in TESTAGRAM_ALGORITHM_IMAGE.
3. Pin Caddy to a reviewed immutable image digest.
4. Provision a new Appwrite project and database. Create all documented
   collections, attributes and indexes; wait for asynchronous provisioning
   before starting the API.
5. Create a least-privilege Appwrite server key. Keep it only in the runtime
   secret store. Never put it in XClone build variables.
6. Configure exact frontend HTTPS origins, Appwrite endpoint/project/database,
   independent Algorithm token and a separate TLS PostgreSQL database for the
   Data Centre control plane.
7. Run docker compose --env-file vps/fresh.env -f vps/docker-compose.fresh.yml config
   and inspect the rendered configuration for secrets and unexpected services.
8. Keep FRESH_MEDIA_ENABLED=false until B2/R2 credentials, Appwrite storage
   schema, upload read-back verification and independent archive are staged.
9. Keep retention disabled and dry-run true. Do not enable destructive jobs
   during initial deployment.
10. Run staging auth, feed authorization, ranker outage, media, backup/restore,
    load and security tests before production DNS cutover.

This compose file is a deployment foundation, not proof of production readiness.
It does not implement the missing Backend-to-Federation or Backend-to-Data-Centre
media placement/replica APIs. Do not expose or enable those paths until their
contracts and integration tests are complete.
