# Data Centre service overlay

This Compose file is an overlay for the canonical Backend deployment. It is
not a standalone database or public edge stack.

## Start order

1. Deploy the Backend repository's `deploy/self-hosted/docker-compose.yml`.
   That stack creates PostgreSQL and the stable private networks
   `testagram-platform` and `testagram-egress`.
2. Copy `.env.example` to `.env`. Set `DATABASE_URL` to the same PostgreSQL
   user/password/database as the Backend stack, and configure the same
   `APPWRITE_ENDPOINT` and `APPWRITE_PROJECT_ID`.
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
- `video`: Appwrite JWT validation, canonical identity resolution, upload
  quotas and playback authorization.
- `video-worker`: PostgreSQL-backed FFmpeg processing and HLS publication.
- `notification`: Appwrite JWT validation, device registration and delivery
  state.
- `worker`: Data Centre background tasks.

The Backend Caddy edge remains the only public ingress. The overlay does not
publish ports or run a second reverse proxy. Video, notification and platform
services join the shared private network; only the services that need to call
Appwrite also join the controlled egress network.

## Shared database and storage

The Backend's PostgreSQL database is canonical. Apply Backend migrations before
starting this overlay, including the Appwrite identity bridge and platform
schema. The video API and worker share the host media directory through the
configured storage root. Back up that directory and PostgreSQL together.

There is no Supabase/PostgREST dependency, Redis/Valkey cache or Upstash queue.
