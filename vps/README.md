# Testagram single-VPS runtime

This repository assembles the self-hosted Testagram Data Centre runtime. It
runs Testagram-owned PostgreSQL, storage/node services, the Appwrite-authenticated
video and notification APIs, PostgreSQL-backed platform configuration/audit,
the control plane, maintenance workers and the Caddy HTTPS edge.

It is not the Supabase Docker distribution. The VPS does not require Supabase,
Redis/Valkey or Upstash.

## Runtime boundaries

- Appwrite issues client sessions. Video and notification services validate
  Appwrite JWTs through the Appwrite Account API and resolve canonical Testagram
  account IDs through the PostgreSQL identity bridge.
- PostgreSQL is the canonical relational store. Configure the shared
  `DATABASE_URL` to the Testagram-owned PostgreSQL cluster, not a Supabase host.
- Object bytes and node volumes live under the configured Testagram storage
  root. Preserve object checksums, metadata and replica inventory.
- The platform service provides PostgreSQL-backed feature configuration,
  telemetry and audit only; it does not provide cache or generic queue APIs.
- Durable asynchronous work is recorded in owning services' PostgreSQL
  outboxes and processed idempotently.
- The node-agent can run as a host systemd service and uses the VPS storage tree
  for node volumes.

## Deployment

Configure the protected VPS environment from `vps/fresh.env.example` and
`backend/.env.example`. Set Appwrite endpoint/project ID, PostgreSQL
credentials, storage paths, domain names, and private service tokens. Do not
put server-only Appwrite API keys in browser/mobile configuration.

Deploy with:

```sh
sudo bash vps/install.sh
```

Never expose PostgreSQL, object-storage administration, control-plane, or
internal service ports directly to the Internet. Publish only the approved
Caddy HTTPS edge and explicitly configured node-agent operations endpoints.

## Five-repository application runtime

The VPS stack includes the separately released Testagram Backend API and the
private Algorithm ranker on the internal Docker network. Caddy serves the
Backend API at `TESTAGRAM_API_DOMAIN` (default `api.testagram.site`). Flutter
is the only product frontend and should set its Backend base URL to that origin.
The ranker has no public port and accepts only the server-side
`ALGORITHM_INTERNAL_TOKEN`.

Set `TESTAGRAM_BACKEND_IMAGE`, `TESTAGRAM_ALGORITHM_IMAGE` and
`ALGORITHM_INTERNAL_TOKEN` in the protected VPS environment before starting
the stack. Image tags must refer to successfully published immutable GHCR
images. If packages are private, authenticate Docker to ghcr.io on the VPS
using a read-only package token; never place that token in the repository.

Federated Content is deployed only after signature verification, durable inbox
and outbox, consent checks, deduplication and dynamic actor resolution pass
their release gates. Do not expose a preview inbox publicly or claim production
federation before those gates pass.

## Recovery

Back up PostgreSQL, object storage, node inventory and deployment secrets to an
off-host destination. Test restoring the exact backup into an isolated host,
including Appwrite identity mapping, private media access and video playback.
There is no Redis/Valkey queue state to restore; outbox and retry state is in
PostgreSQL.
