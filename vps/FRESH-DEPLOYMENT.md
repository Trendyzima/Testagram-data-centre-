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
- edge: Caddy terminates TLS and forwards only api.testagram.site to Backend.
  The Federation hostname is deliberately absent from the public Caddy config.
- federation-api: a private ActivityPub preview service on the internal
  services/federation networks, backed by its dedicated PostgreSQL database.
  It resolves opted-in actors through Backend and verifies/persists signed inbox
  deliveries, but it does not yet process inbox activities or execute outbound
  delivery/follower-fanout workers. Its canonical actor origin is
  https://federation.testagram.site; that hostname must not receive public DNS
  or Caddy routing until the remaining federation release gates pass.

## Before deployment

1. Publish the fresh Backend image using Backend container-release.yml and pin
   the resulting GHCR digest in TESTAGRAM_FRESH_BACKEND_IMAGE.
2. Publish Algorithm and pin its digest in TESTAGRAM_ALGORITHM_IMAGE.
3. Publish the private Federation preview image and pin its digest in TESTAGRAM_FEDERATION_IMAGE.
4. Pin Caddy to a reviewed immutable image digest.
5. Provision a new Appwrite project and database. Create all documented
   collections, attributes and indexes; wait for asynchronous provisioning
   before starting the API.
6. Create a least-privilege Appwrite server key. Keep it only in the runtime
   secret store. Never put it in XClone build variables.
7. Configure exact frontend HTTPS origins, Appwrite endpoint/project/database,
   independent Algorithm token and a separate TLS PostgreSQL database for the
   Data Centre control plane.
8. Run docker compose --env-file vps/fresh.env -f vps/docker-compose.fresh.yml config
   and inspect the rendered configuration for secrets and unexpected services.
9. Keep FRESH_MEDIA_ENABLED=false until B2/R2 credentials, Appwrite storage
   schema, upload read-back verification and independent archive are staged.
10. Keep retention disabled and dry-run true. Do not enable destructive jobs
   during initial deployment.
11. Run staging auth, feed authorization, ranker outage, media, backup/restore,
    load and security tests before production DNS cutover.

This compose file is a deployment foundation, not proof of production readiness.
The private Backend-to-Federation actor and readiness contracts exist, but inbox
activity application, Backend outbox polling, outbound delivery, follower fan-out
and cross-repository end-to-end tests remain unfinished. Backend-to-Data-Centre
media placement, verified B2/R2 replica recovery and operational restore drills
also remain release gates. Do not add public Federation routing until those
contracts and tests are complete.
