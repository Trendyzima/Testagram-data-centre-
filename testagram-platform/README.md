# Testagram Platform Runtime

This is Testagram's deployment-owned entrypoint for its self-hosted platform services. The application-facing deployment, local storage paths, health checks, and runtime project are branded **Testagram**.

## Important implementation boundary

The authentication, Postgres API, realtime, and object-storage containers are currently built from the upstream **Supabase self-hosted distribution** at a pinned release. This repository wraps and configures those upstream components; it does not yet replace their internals with Testagram-written equivalents. Keeping that provenance explicit is important for security updates, licensing, migrations, and incident response.

## Paths and configuration

- Canonical deployment scripts: `testagram-platform/`
- Runtime root: `TESTAGRAM_PLATFORM_ROOT` (default `/var/lib/testagram/supabase` for compatibility)
- Local media and object storage root: `TESTAGRAM_STORAGE_ROOT` (default `/var/lib/testagram/storage`)
- Testagram API URL: `TESTAGRAM_PLATFORM_URL` / `TESTAGRAM_PUBLIC_URL`
- Legacy `SUPABASE_DATA_ROOT`, `SUPABASE_PROJECT_ROOT`, and `SUPABASE_PUBLIC_URL` settings remain accepted during migration.
- Existing object data remains at `$TESTAGRAM_STORAGE_ROOT/supabase` for compatibility. Do not rename or move this directory until a verified copy, object-count comparison, and restore test have passed.

## Safe preparation

`prepare.sh` stages the new runtime before switching it into place. If a runtime already exists, its `.env` is carried forward and the old project directory is retained as a timestamped rollback copy. It does not delete the active project directory. Keep the runtime root private because rollback copies contain secrets.

The upstream release checkout is under `$TESTAGRAM_PLATFORM_ROOT/upstream/supabase`. Updating the upstream release is a controlled deployment operation; do not point production at an unreviewed branch.

## Operations

Run the read-only host/configuration preflight before a deployment. It checks Docker Compose, required secret presence, restrictive env-file permissions, RAM/disk recommendations, and Compose rendering. It never prints secret values or starts/stops containers:

```bash
TESTAGRAM_ENV_FILE=/opt/testagram/.env bash testagram-platform/preflight.sh
```

On small hosts, resource thresholds warn by default. To make RAM/disk baselines hard requirements, set `TESTAGRAM_PREFLIGHT_STRICT_RESOURCES=1` and adjust `TESTAGRAM_PREFLIGHT_MIN_RAM_GB` / `TESTAGRAM_PREFLIGHT_MIN_FREE_GB` deliberately.

```bash
TESTAGRAM_PLATFORM_ROOT=/var/lib/testagram/platform \
TESTAGRAM_STORAGE_ROOT=/var/lib/testagram/storage \
bash testagram-platform/prepare.sh

TESTAGRAM_PLATFORM_URL=https://api.testagram.site \
bash testagram-platform/healthcheck.sh

bash testagram-platform/status.sh
```

Health checks exercise the auth, REST, storage, realtime, and gateway endpoints. A successful HTTP response is only a surface-level check; it does not prove sign-in, row-level security, media authorization, background jobs, backups, or migration correctness.

## Security and migration guardrails

- Secrets belong in a root-owned environment file with restrictive permissions, never Git.
- Keep Postgres and internal service ports private; expose only the intended reverse proxy/API routes.
- Back up the database and object directory before changing releases or schema.
- Test a restore on a separate instance before any production cutover.
- Do not switch DNS or production traffic as part of this rebrand alone.
