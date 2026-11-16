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
runtime environment. Keep Federation workers disabled until the separate
protocol release gates and cross-instance tests pass. The video service joins the shared
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
