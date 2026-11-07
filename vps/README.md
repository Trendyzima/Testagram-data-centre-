# Testagram VPS runtime

The supported VPS deployment is the fresh five-repository runtime. The legacy
single-repository provider stack is not an installation or upgrade target.

## Runtime ownership

- **XClone** is the web/mobile/TV/desktop client.
- **Testagram Backend** is the public API, identity/session verifier, canonical
  product authorization and orchestration layer.
- **Federated Content** is a private ActivityPub service. Public Federation
  routing remains disabled until signed inbox application, Backend outbox
  polling, delivery workers and interoperability gates pass.
- **Algorithm** is a private feed-ranking service reachable only by Backend.
- **Data Centre** owns VPS deployment, Android storage nodes, Linux compute
  nodes, infrastructure state, backup and recovery operations.

The supported Compose definition is `vps/docker-compose.yml`, kept byte-for-byte
aligned with `vps/docker-compose.fresh.yml`. It runs Appwrite-backed identity,
the Backend API, private Algorithm and Federation services, and the Data Centre
control plane. Product data uses the separately configured Neon database.
Backblaze B2 is the intended durable media primary and Cloudflare R2 is the
verified delivery/replica target. Keep media disabled until provider credentials,
metadata, read-back verification and recovery tests pass.

## Deploy

On a Linux host with Docker Engine, Docker Compose v2 and Git:

```bash
bash scripts/install-local-docker.sh
```

The installer creates `vps/fresh.env` from `vps/fresh.env.example` on first
run and stops. Set real Appwrite, Neon, B2/R2, private-service secrets and
immutable image digests before running it again. The compatibility entry point
`sudo bash vps/install.sh` delegates to the same installer; it does not start
a second stack.

Inspect the rendered stack:

```bash
docker compose --env-file /opt/testagram/vps/fresh.env -f /opt/testagram/vps/docker-compose.yml config
docker compose --env-file /opt/testagram/vps/fresh.env -f /opt/testagram/vps/docker-compose.yml ps
```

Never expose PostgreSQL, the Algorithm ranker, Federation API, Valkey, or
control-plane internal ports directly to the Internet. The ranker and
Federation databases are isolated on private networks. Do not add public
Federation DNS/Caddy routing before the release gates pass.

## Android storage and Linux compute nodes

Android devices are storage-only nodes. They must not receive executable
compute workloads. Linux compute nodes may run digest-pinned, allowlisted
workloads. Register nodes through the authenticated Data Centre control plane
and verify heartbeats, removable-card identity, available capacity and
checksummed transfer results before placing media.

## Encrypted backup and restore drill

The backup scripts now operate on the dedicated
`DATA_CENTRE_DATABASE_URL` infrastructure database, not product social data.
They can optionally include `TESTAGRAM_STORAGE_ROOT`; coordinate a quiet period
or use a filesystem snapshot while node writers are active.

Required operator tools: `pg_dump`, Docker Engine, `age`, `rsync`, `tar`,
`flock`, `sha256sum`, SSH key authentication and a verified host key.

Set `TESTAGRAM_FRESH_ENV_FILE`, `TESTAGRAM_BACKUP_REMOTE` and
`TESTAGRAM_BACKUP_AGE_RECIPIENT`, then run:

```bash
bash vps/backup.sh
```

For an isolated restore drill, use the separately held age identity and an
approved immutable PostgreSQL image digest:

```bash
TESTAGRAM_BACKUP_AGE_IDENTITY_FILE=/secure/off-host-age-identity.txt \
TESTAGRAM_RESTORE_POSTGRES_IMAGE='postgres:17-alpine@sha256:REPLACE_WITH_APPROVED_DIGEST' \
  bash vps/restore-drill.sh /secure/backup/testagram-data-centre-YYYYMMDDTHHMMSSZ.tar.gz.age
```

The restore drill verifies the encrypted archive checksum, decrypts the dump,
restores it into a network-isolated temporary PostgreSQL container and checks
Data Centre node/hosting/backup tables. It does not prove product login,
media upload/playback, B2/R2 object recovery or a measured production RPO/RTO.
Record separate application-level recovery evidence before declaring DR ready.
