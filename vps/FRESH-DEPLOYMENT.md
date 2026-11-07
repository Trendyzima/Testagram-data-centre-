# Fresh Appwrite deployment (default VPS stack)

This stack is the clean Appwrite runtime. It does not attach retired provider networks,
start the legacy cmd/api, or configure retired database credentials.

## Services and trust boundaries

- api: Backend's dedicated Dockerfile.fresh image. Verifies Appwrite JWTs,
  serves the public /v1 API, owns authorization and calls private services.
- algorithm: private stateless ranker on an internal-only Docker network. Only
  Backend can reach it; its bearer token is not exposed to XClone. Backend has
  a second egress-capable network for Appwrite and object-provider access.
- migrate: applies only `001_vps.sql` and `006_hosting_control_plane.sql` to
  the dedicated Data Centre PostgreSQL database before control-plane starts.
  It deliberately does not apply product/video, notification, platform-event,
  or retired object-storage migrations to this infrastructure database.
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
5. Provision a dedicated Neon PostgreSQL database and least-privilege Federation role. Set FEDERATION_DATABASE_URL to its pooled TLS URL (sslmode=require or stronger). Import and reconcile existing Federation actors, keys, inbox/outbox activities, remote followers and delivery jobs before production cutover. The local federation-db service is only available with the Compose local profile.
6. Provision a new Appwrite project and database. Create all documented
   collections, attributes and indexes; wait for asynchronous provisioning
   before starting the API.
7. Create a least-privilege Appwrite server key. Keep it only in the runtime
   secret store. Never put it in XClone build variables.
8. Configure exact frontend HTTPS origins, Appwrite endpoint/project/database,
   independent Algorithm token and a separate TLS PostgreSQL database for the
   Data Centre control plane.
9. Run docker compose --env-file vps/fresh.env -f vps/docker-compose.fresh.yml config
   and inspect the rendered configuration for secrets and unexpected services.
10. Provision Backblaze B2 as the durable primary object store and Cloudflare R2
   as the delivery/replica store. Backend currently implements B2 signed uploads,
   B2 SHA-256 verification, synchronous R2 copy/read-back verification, private
   download URLs and repair logic. Set separate least-privilege S3-compatible
   credentials and endpoints; never reuse keys between providers. Configure the
   optional Cloudflare private-media Worker URL/signing secret as a pair.
11. Keep FRESH_MEDIA_ENABLED=false until B2/R2 credentials, Appwrite metadata
   schema, upload read-back verification, checksum-matched replica reconciliation
   and an independent archive are staged.
12. Keep retention disabled and dry-run true. Do not enable destructive jobs
   during initial deployment.
13. Run staging auth, feed authorization, ranker outage, media, backup/restore,
    load and security tests before production DNS cutover.

This compose file is a deployment foundation, not proof of production readiness.
The private Backend-to-Federation actor and readiness contracts exist, but inbox
activity application, Backend outbox polling, outbound delivery, follower fan-out
and cross-repository end-to-end tests remain unfinished. Backend-to-Data-Centre
node media placement, signed node object transfer, durable replication jobs,
provider reconciliation scheduling and operational restore drills also remain
release gates. Real B2/R2 credentials and live provider E2E tests have not yet
been exercised by this repository's CI. Do not add public Federation routing until those
contracts and tests are complete.
