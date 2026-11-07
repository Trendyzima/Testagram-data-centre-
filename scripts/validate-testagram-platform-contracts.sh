#!/usr/bin/env bash
set -euo pipefail
for script in vps/backup.sh vps/restore-drill.sh vps/install.sh scripts/install-local-docker.sh scripts/validate-image-digests.sh; do bash -n "$script"; done
cmp -s vps/docker-compose.yml vps/docker-compose.fresh.yml
for file in vps/docker-compose.yml vps/.env.example vps/install.sh scripts/install-local-docker.sh vps/backup.sh vps/restore-drill.sh backend/docker-compose.yml; do
  if grep -Eqi 'supabase' "$file"; then echo "Retired provider reference remains in active runtime path: $file" >&2; exit 1; fi
done
grep -q 'B2_S3_ENDPOINT' vps/docker-compose.fresh.yml
grep -q 'CLOUDFLARE_R2_ENDPOINT' vps/docker-compose.fresh.yml
grep -q 'Backblaze B2' docs/platform/media-storage-architecture.md
grep -q 'Cloudflare R2' docs/platform/media-storage-architecture.md
grep -q 'FRESH_MEDIA_ENABLED: ${FRESH_MEDIA_ENABLED:-false}' vps/docker-compose.fresh.yml
grep -q 'TESTAGRAM_RETENTION_ENABLED: "false"' vps/docker-compose.fresh.yml
grep -q 'TESTAGRAM_RETENTION_DRY_RUN: "true"' vps/docker-compose.fresh.yml
grep -q '/readyz' docs/platform/openapi/internal-platform.yaml
grep -q '/v1/hosting/backups' docs/platform/openapi/hosting-control-plane.yaml
valid_digest="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
TESTAGRAM_FRESH_BACKEND_IMAGE="example/backend@sha256:$valid_digest" TESTAGRAM_ALGORITHM_IMAGE="example/algorithm@sha256:$valid_digest" TESTAGRAM_FEDERATION_IMAGE="example/federation@sha256:$valid_digest" CADDY_IMAGE="example/caddy@sha256:$valid_digest" POSTGRES_CLIENT_IMAGE="example/postgres-client@sha256:$valid_digest" FEDERATION_POSTGRES_IMAGE="example/postgres@sha256:$valid_digest" bash scripts/validate-image-digests.sh >/dev/null
if TESTAGRAM_FRESH_BACKEND_IMAGE="example/backend:latest" TESTAGRAM_ALGORITHM_IMAGE="example/algorithm@sha256:$valid_digest" TESTAGRAM_FEDERATION_IMAGE="example/federation@sha256:$valid_digest" CADDY_IMAGE="example/caddy@sha256:$valid_digest" POSTGRES_CLIENT_IMAGE="example/postgres-client@sha256:$valid_digest" FEDERATION_POSTGRES_IMAGE="example/postgres@sha256:$valid_digest" bash scripts/validate-image-digests.sh >/dev/null 2>&1; then
  echo "Mutable image tag unexpectedly passed digest validation" >&2; exit 1
fi
echo "Fresh Appwrite runtime, immutable image pins, B2/R2 storage contract and retired-provider exit checks: PASS"
