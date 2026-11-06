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

# Optional: validate config without changing containers
TESTAGRAM_ENV_FILE=/opt/testagram/.env TESTAGRAM_DEPLOY_DRY_RUN=1 bash testagram-platform/deploy.sh

# Explicit deployment (builds and starts services; does not change DNS or migrate schemas)
TESTAGRAM_ENV_FILE=/opt/testagram/.env bash testagram-platform/deploy.sh
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


## Production release candidate gates

The VPS preflight is intentionally stricter than local development. Before it permits deployment, the operator must supply immutable SHA-256 image references for the Backend and Algorithm, explicitly select one authentication provider, configure the legacy Supabase compatibility credentials still used by unmigrated media/domain operations, and provide HTTPS S3-compatible media endpoint and credentials. If Appwrite is selected, its HTTPS endpoint and project ID must also be present. Set **TESTAGRAM_REQUIRE_APPWRITE=1** to prevent a production release candidate from silently falling back to legacy Supabase authentication.

Example image references must use the registry's actual manifest digest, not a guessed digest or a mutable tag:

- **TESTAGRAM_BACKEND_IMAGE=ghcr.io/trendyzima/testagram-backend@sha256:<64-hex-digest>**
- **TESTAGRAM_ALGORITHM_IMAGE=ghcr.io/trendyzima/testagram-feed@sha256:<64-hex-digest>**

Keep the federation service on its disabled preview profile. Do not set TESTAGRAM_FEDERATION_IMAGE or enable federation-preview until the service has verified inbound signatures, durable inbox/outbox, SSRF-safe delivery and retry/dead-letter behavior. The disabled placeholder image is not a release artifact.

The S3 endpoint and credentials passing preflight prove configuration presence only. They do not prove bucket policy, object authorization, upload completion, private download denial, CDN cache separation, deletion, or restore. Keep VIRTUAL_STORAGE_ENABLED=false until those live staging tests pass.


### Appwrite cutover is deliberately blocked until the client migration is complete

The current XClone application still has Supabase-backed session listeners and legacy product features that call Supabase directly. Its Appwrite SDK and Go API token adapter do not yet constitute a complete global Appwrite session migration. Therefore the example VPS environment retains the explicit transitional provider value **TESTAGRAM_AUTH_PROVIDER=supabase** while setting **TESTAGRAM_REQUIRE_APPWRITE=1**. This intentionally makes the production preflight fail rather than deploying an Appwrite-only backend against a Supabase-session frontend.

Do not set **TESTAGRAM_REQUIRE_APPWRITE=0** for a production release. That override is only for an explicitly approved transitional staging environment. The Appwrite production gate may be cleared only after XClone's global auth provider, session restoration/listeners, account signup/link/provisioning, password recovery, and all remaining Supabase-backed capability adapters have passed end-to-end staging with the same canonical PostgreSQL user IDs.
