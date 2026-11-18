# Data Centre service overlay

This Compose file is an overlay for the canonical Backend deployment. It is
not a standalone database or public edge stack.

## Start order

1. Deploy the Backend repository's `deploy/self-hosted/docker-compose.yml`.
   That stack creates PostgreSQL and the shared networks `testagram-platform`,
   `testagram-egress` and `testagram-self-hosted_edge`.
2. Copy `.env.example` to `.env`. Set `DATABASE_URL` to the same PostgreSQL
   user/password/database as the Backend stack. Keep `TESTAGRAM_AUTH_PROVIDER`
   aligned with Backend. In native mode, provide the exact same
   `TESTAGRAM_AUTH_JWT_SECRET`; Appwrite endpoint/project are required only in
   Appwrite mode. Set `TESTAGRAM_EDGE_NETWORK` to the Backend Caddy edge network.
3. Generate independent random values for `PLATFORM_INTERNAL_TOKEN`,
   `NOTIFICATION_INTERNAL_TOKEN` and `VIDEO_SIGNING_SECRET`.
4. Ensure the persistent media root exists on the host.
5. Start the overlay from this repository:

```sh
docker compose --env-file backend/.env -f backend/docker-compose.yml config
docker compose --env-file backend/.env -f backend/docker-compose.yml up -d --build
docker compose --env-file backend/.env -f backend/docker-compose.yml ps
```

## Services

- `platform`: PostgreSQL-backed feature configuration, event telemetry and
  audit API. It is private and uses `PLATFORM_INTERNAL_TOKEN`.
- `video`: provider-explicit Appwrite or Testagram-native session validation,
  canonical identity resolution, upload quotas and signed HLS playback.
- `video-worker`: PostgreSQL-backed FFmpeg processing and HLS publication.
- `notification`: Appwrite JWT validation, device registration and delivery
  state.
- `worker`: Data Centre background tasks.

The Backend Caddy edge remains the only public ingress. The overlay does not
publish ports or run a second reverse proxy. Federation joins the existing
`TESTAGRAM_EDGE_NETWORK` as a private upstream; it does not publish port 8081.
The Backend edge routes only `/.well-known/webfinger`, `/users/*` and
`/objects/*` on `testagram.site` to Federation. Ordinary apex web requests
redirect to the existing `www.testagram.site` frontend. Set
`FEDERATION_PUBLIC_BASE_URL=https://testagram.site` in this overlay's protected
runtime environment. Keep Federation's inbound acceptance, inbox worker, outbound delivery worker and Backend outbox poller disabled until the separate protocol release gates and cross-instance tests pass. In particular, keep `FEDERATION_INBOX_ACCEPT_ENABLED=false`; setting it true requires the durable inbox worker, delivery worker, Federation database and private Backend integration. The video service joins the shared
Caddy edge network so Caddy can route `/v1/feed`, `/v1/videos/*` and
`/v1/channels*` over the existing HTTPS API origin. Video playback and its
likes/comments use the same origin and Testagram-native session as the Flutter
client. Browser HLS playback remains a separate web-player capability; native
mobile playback uses the dedicated video service. Only services that need to
call Appwrite join the controlled egress network.

## Shared database and storage

The Backend's PostgreSQL database is canonical. Apply Backend migrations before
starting this overlay, including the Appwrite identity bridge and platform
schema. The video API and worker share the host media directory through the
configured storage root. Back up that directory and PostgreSQL together.

There is no Supabase/PostgREST dependency, Redis/Valkey cache or Upstash queue.


## Optional B2/R2 media lifecycle profile

The `media` and `media-migrate` services are opt-in under the Compose
`media` profile. They use a separate `MEDIA_DATABASE_URL`; never point it at
the Backend product database. The Backend and Data Centre must receive the
same independently generated `FRESH_DATA_CENTRE_INTERNAL_TOKEN`. The short-lived
`media-migrate` job uses Backend's database-admin URL only to create/own the
restricted `MEDIA_DATABASE_USER` and its database; the long-running `media`
container receives only `MEDIA_DATABASE_URL`, never the Backend database-admin
URL or password. Set `VPS_MINIO_METADATA_BUCKET` to the same bucket as Backend
`MINIO_METADATA_BUCKET`, and pass the matching dedicated metadata key created
by Backend's `object-init` job. In the
Backend repository's protected `.env`, set
`FRESH_MEDIA_ENABLED=true` and `DATA_CENTRE_MEDIA_API_URL=http://media:8096`
only after the Data Centre media service is running and the staging checks pass.

Configure the optional variables in this repository's `backend/.env` using
`backend/.env.example` as the template. The service requires B2 and R2 object
credentials, a separate media database, and the restricted MinIO/R2 metadata
credentials. The MinIO metadata key must be the dedicated key provisioned by
the Backend `object-init` job; never use MinIO root credentials.

Start this profile only after the Backend base stack has created the shared
`testagram-platform` network and the Backend product database is ready:

```sh
docker compose --env-file backend/.env -f backend/docker-compose.yml --profile media config
docker compose --env-file backend/.env -f backend/docker-compose.yml --profile media up -d --build media
docker compose --env-file backend/.env -f backend/docker-compose.yml --profile media ps media media-migrate
```

This service's media-byte path is **B2 primary + R2 replica**. MinIO and R2
metadata sidecars are not a third copy of the media bytes. The Backend's
separate virtual-storage path is the one configured for three independently
verified media-byte copies (InstaBox MinIO + R2 + B2). Passing one lifecycle
does not pass the other. Keep both production feature paths disabled until
their own upload, read-back SHA-256, restore, repair, delete and CDN delivery
evidence is recorded.


## Cross-stack service credentials

The Backend and Data Centre Compose projects read separate protected `.env`
files. Copy only the matching value across each boundary; do not reuse a token
for another service:

- `ALGORITHM_INTERNAL_TOKEN`: same value in both projects, Backend ↔ Algorithm only.
- Backend `FEDERATION_INGRESS_TOKEN` must equal Data Centre `FEDERATION_INTERNAL_TOKEN`: Backend → Federation only.
- `FEDERATION_BACKEND_INTERNAL_TOKEN`: same value in both projects, Federation → Backend only.
- `FRESH_DATA_CENTRE_INTERNAL_TOKEN`: same value in both projects, Backend → Data Centre media only.

The Data Centre `media` profile has its own B2/R2 provider keys and restricted
MinIO metadata identity. Never pass those MinIO metadata keys to Backend or
Flutter. The Backend's separate virtual-storage provider credentials remain
in the Backend's own protected environment. These service credentials are
independent of the end-user JWT signing secret.

Federation protocol state also has a separate `FEDERATION_DATABASE_NAME` and
`FEDERATION_DATABASE_USER`. The one-shot Data Centre `migrate` service uses
Backend's database-admin URL to create/own that non-superuser role and database;
the long-running `federation-api` receives only the scoped
`FEDERATION_DATABASE_URL`. Do not put Backend's `DATABASE_URL` or the
Federation role bootstrap password in the Federation API container.
