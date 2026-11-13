# Fresh Appwrite deployment (separate from the legacy VPS stack)

The default profile starts the Appwrite Backend API and Caddy edge. Ranking, Federation and the Data Centre node control-plane are independent opt-in profiles; none is allowed to gate Appwrite login. This stack does not attach retired provider networks, start the legacy cmd/api, or configure retired database credentials.

## Services and trust boundaries

- Enable the private Algorithm ranker only with `docker compose --profile ranking ...`; login remains available when ranking is stopped.
- Enable ActivityPub delivery only after provisioning Federation's dedicated Neon database and service token, using `--profile federation`.
- Enable Android/Linux node control-plane only after provisioning its separate infrastructure database and tokens, using `--profile nodes`.

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
- media: dedicated private B2-primary/R2-replica object lifecycle service,
  enabled only with the `media` profile and backed by its own MEDIA_DATABASE_URL.
  It is attached to both the local services network and the Backend-owned
  `testagram-platform` network; it is not a public API.
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
5. Provision a dedicated Neon PostgreSQL database and least-privilege Federation role. Set FEDERATION_DATABASE_URL to its pooled TLS URL (sslmode=require or stronger). Import and reconcile existing Federation actors, keys, inbox/outbox activities, remote followers and delivery jobs before production cutover. The local federation-db service is only available with the Compose local profile.
6. Provision a new Appwrite project and database. Create all documented
   collections, attributes and indexes; wait for asynchronous provisioning
   before starting the API.
7. Create a least-privilege Appwrite server key. Keep it only in the runtime
   secret store. Never put it in XClone build variables.
8. Configure exact frontend HTTPS origins, Appwrite endpoint/project/database,
   independent Algorithm token and a separate TLS PostgreSQL database for the
   Data Centre control plane.
9. Start the Backend self-hosted stack first so it creates the internal
   `testagram-platform` Docker network. The Data Centre Compose file joins that
   existing network for the dedicated media service; do not publish port 8096.
   Configure Backend with DATA_CENTRE_MEDIA_API_URL=http://media:8096 and the
   same FRESH_DATA_CENTRE_INTERNAL_TOKEN used as MEDIA_INTERNAL_TOKEN by the
   Data Centre media container. Keep FRESH_MEDIA_ENABLED=false during staging.
10. Run docker compose --env-file vps/fresh.env -f vps/docker-compose.fresh.yml config
    and inspect the rendered configuration for secrets and unexpected services.
11. Provision Backblaze B2 as the durable primary and Cloudflare R2 as the
    independent replica. Store B2/R2 credentials only in the Data Centre media
    service environment; never pass provider credentials into Backend, Flutter,
    the Algorithm, Federation or CDN. Use separate least-privilege credentials.
12. Apply the dedicated media migration and start only the private media profile:
    `docker compose --env-file vps/fresh.env -f vps/docker-compose.fresh.yml --profile media up -d media-migrate media`.
    Verify `/healthz` and `/readyz` from the private network; do not expose 8096.
13. Keep FRESH_MEDIA_ENABLED=false until real B2 PUT, independent SHA-256
    read-back, R2 replica write/read-back, retry/reconciliation, delete and
    restore tests pass. Also prove Backend -> Data Centre authentication and
    CDN public/private cache isolation in staging.
12. Keep retention disabled and dry-run true. Do not enable destructive jobs
   during initial deployment.
13. Run staging auth, feed authorization, ranker outage, media, backup/restore,
    load and security tests before production DNS cutover.

This compose file is a deployment foundation, not proof of production readiness.
The dedicated media service now implements the five Backend-facing lifecycle
operations, durable verification/deletion outbox, retry workers and replica
reconciliation. CI exercises service logic and isolated database restore, but
real B2/R2 credentials, provider-backed write/read/hash/delete/restore drills and
Backend-to-CDN-to-Flutter end-to-end delivery are still mandatory release gates.
The Go CDN also needs its canonical deployment controller and external post-deploy
smoke. The private Backend-to-Federation actor/readiness contracts exist, but
inbox activity application, Backend outbox polling, outbound delivery, follower
fan-out and cross-repository end-to-end tests remain unfinished. Do not enable
fresh media or add public Federation routing until those contracts and tests
are complete.
