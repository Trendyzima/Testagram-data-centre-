# Testagram Data Centre deployment

This repository deploys **Data Centre-owned infrastructure services**. It does not deploy a second public API or a second HTTPS edge.

## Service ownership

- **Backend repository** owns the canonical Go API, PostgreSQL product/auth schema, public Caddy edge, staff console and API authentication.
- **Data Centre repository** owns the private media lifecycle service, optional ranking service, ActivityPub federation service, node control plane and node agent.
- **CDN repository** owns media delivery/cache infrastructure.

The old Appwrite-backed Fresh API was a parallel API implementation. It is not part of this deployment. Do not restore its Appwrite credentials, container or Caddy listener. The Backend Caddy edge already routes API, video, media, federation and staff-console traffic.

## Start the Data Centre node services

1. Deploy the Backend self-hosted stack first. It creates the private Docker networks testagram-platform and testagram-ranker.
2. Copy vps/data-centre.env.example to a root-owned runtime file, for example /opt/testagram/vps/data-centre.env, and set permissions to 0600.
3. Set DATA_CENTRE_DATABASE_URL, VPS_INTERNAL_TOKEN and VPS_NODE_BOOTSTRAP_TOKEN with independently generated secrets.
4. Run vps/install.sh. The installer starts only the nodes profile and registers the outbound-only node agent.

The installer deliberately does not require media, federation or ranking credentials when those profiles are disabled.

## Optional profiles

- **media:** starts the private media lifecycle service and applies both 001_media_lifecycle.sql and 002_primary_origin_restore.sql. It requires a dedicated MEDIA_DATABASE_URL, a shared FRESH_DATA_CENTRE_INTERNAL_TOKEN, and complete B2/R2 credentials. The media service has no published host port and joins the Backend-owned testagram-platform network.
- **federation:** starts the ActivityPub service with its own PostgreSQL database. FEDERATION_BACKEND_INTERNAL_TOKEN is the private Federation-to-Backend read credential. FEDERATION_INGRESS_TOKEN is a different Backend-to-Federation credential. Never reuse either token.
- **ranking:** starts the private ranking service. It is optional and must not become an authentication or login dependency.

Example commands:

```sh
docker compose --env-file vps/data-centre.env -f vps/docker-compose.yml --profile media up -d media-migrate media
docker compose --env-file vps/data-centre.env -f vps/docker-compose.yml --profile federation up -d federation-db federation-api
docker compose --env-file vps/data-centre.env -f vps/docker-compose.yml --profile ranking up -d algorithm
```

## Safety and release gates

- Keep retention disabled and dry-run enabled until explicit restore/delete acceptance has passed.
- Keep media disabled until B2 and R2 write/read/hash/replica/restore drills pass with real credentials.
- Keep Federation workers disabled until inbox application, outbox polling, outbound delivery and follower fan-out have passed end-to-end tests.
- Never publish the private media, federation ingress, database or control-plane ports directly to the public internet.
- Do not delete or rewrite historical identity migrations or account records as part of provider removal. Reconcile and back up existing identities first.
