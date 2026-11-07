# Fresh Appwrite deployment (default login runtime with opt-in auxiliary profiles)

The default profile starts the Appwrite-backed Backend API and Caddy edge. Ranking,
Federation and the Data Centre node control-plane are independent opt-in profiles;
none is allowed to gate Appwrite login. This stack does not attach retired provider
networks, start the legacy cmd/api, or configure retired database credentials.

## Services and trust boundaries

- **api (default):** Backend's dedicated fresh Docker image. Verifies Appwrite
  JWTs, serves the public /v1 API, owns authorization and calls private services.
  Its /readyz health gate checks the Appwrite profile store required by login;
  Neon, Algorithm and Federation are not login prerequisites.
- **edge (default):** Caddy terminates TLS and forwards api.testagram.site to
  Backend. It depends only on the healthy API. The Federation hostname is
  deliberately absent from the public Caddy configuration.
- **algorithm (ranking profile):** private stateless ranker on an internal-only
  Docker network. Only Backend can reach it; its bearer token is not exposed to
  XClone. When disabled or unavailable, Backend uses safe chronological ordering.
- **federation-api (federation profile):** private ActivityPub preview service
  backed by its dedicated PostgreSQL database and private Backend identity API.
  It is not routed publicly and does not gate the public edge. Inbox activity
  application, Backend outbox polling, outbound delivery and follower fan-out
  remain release gates.
- **migrate and control-plane (nodes profile):** Data Centre infrastructure
  migrations and Android/Linux node management. They use a separate TLS database
  and independent service/bootstrap tokens. They are not required for login.
- **federation-db (local-federation profile):** local-development-only PostgreSQL for
  Federation protocol state. Production Federation uses its own dedicated Neon
  database and least-privilege role.

## Before deployment

1. Publish the fresh Backend image and pin its GHCR digest in
   TESTAGRAM_FRESH_BACKEND_IMAGE. Pin Caddy to a reviewed immutable digest.
2. Provision a new Appwrite project and database. Create all documented
   collections, attributes and indexes; wait for asynchronous provisioning
   before starting the API.
3. Create a least-privilege Appwrite server key. Keep it only in the runtime
   secret store. Never put it in XClone build variables.
4. Configure exact frontend HTTPS origins, Appwrite endpoint/project/database
   and server API key. Configure XClone with the matching Appwrite endpoint,
   project ID and VITE_TESTAGRAM_FRESH_API_URL=https://api.testagram.site.
5. Neon is not required to start the login service. Set NEON_DATABASE_URL to a
   dedicated pooled TLS URL when enabling the canonical social data plane.
   /readyz/platform remains unavailable until schema migration, repository
   integration and reconciliation are complete.
6. Keep TESTAGRAM_ENABLE_RANKING=false for the minimal login deployment. To
   enable ranking, set it true and provide a pinned Algorithm image, internal
   endpoint and a 32+ character ALGORITHM_INTERNAL_TOKEN. Backend keeps a
   chronological fallback if ranking is disabled or temporarily unavailable.
7. Keep TESTAGRAM_ENABLE_FEDERATION=false until the separate Neon Federation
   database is provisioned and reconciled. To enable it, provide the pinned
   Federation image, FEDERATION_DATABASE_URL, FEDERATION_BACKEND_INTERNAL_URL
   and the same strong private token to Backend and Federation. Do not publish
   federation.testagram.site or add public Caddy routing until interoperability
   and durable delivery gates pass.
8. Keep TESTAGRAM_ENABLE_NODES=false until the separate Data Centre database,
   pinned PostgreSQL client image, VPS_INTERNAL_TOKEN and
   VPS_NODE_BOOTSTRAP_TOKEN are provisioned. Enabling it activates the nodes
   Compose profile and its migrations/control-plane. The installer also
   activates the nodes.testagram.site Caddy site only for this profile.
9. Provision Backblaze B2 as the durable primary object store and Cloudflare R2
   as the delivery/replica store. Backend implements B2 signed uploads, B2
   SHA-256 verification, synchronous R2 copy/read-back verification, private
   download URLs and repair logic. Keep FRESH_MEDIA_ENABLED=false until real
   provider credentials, Appwrite metadata schema, replica reconciliation and
   independent restore tests are staged.
10. Keep retention disabled and dry-run true. Do not enable destructive jobs
    during initial deployment.
11. Run staging authentication/session restoration, CORS preflight, feed
    authorization, ranking outage, media, backup/restore, load and security
    tests before production DNS cutover.

This compose file is a deployment foundation, not proof of production readiness.
The private Backend-to-Federation actor and readiness contracts exist, but inbox
activity application, Backend outbox polling, outbound delivery, follower fan-out
and cross-repository end-to-end tests remain unfinished. Backend-to-Data-Centre
node media placement, signed node object transfer, durable replication jobs,
provider reconciliation scheduling and operational restore drills also remain
release gates. Real B2/R2 credentials and live provider E2E tests have not yet
been exercised by this repository's CI. Do not add public Federation routing until those
contracts and tests are complete.
