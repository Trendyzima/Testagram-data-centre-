#!/usr/bin/env bash
set -euo pipefail

for script in vps/backup.sh vps/restore-drill.sh vps/install.sh scripts/install-local-docker.sh; do
  printf "checking shell syntax: %s\\n" "$script"
  bash -n "$script"
done


# Appwrite login must not depend on optional ranking, Federation or node services.
api_block="$(sed -n '/^  api:/,/^  federation-db:/p' vps/docker-compose.fresh.yml)"
if grep -q 'algorithm:' <<< "$api_block"; then
  echo "The public Backend API must not have an Algorithm startup dependency." >&2
  exit 1
fi
grep -q 'FEED_RANKING_REQUIRED: "false"' <<< "$api_block"
grep -q 'ALGORITHM_URL: ${ALGORITHM_URL:-http://algorithm:8092}' <<< "$api_block"
grep -q 'FEDERATION_INTERNAL_URL: ${FEDERATION_INTERNAL_URL:-}' <<< "$api_block"
grep -q 'FEDERATION_INTERNAL_TOKEN: ${FRESH_FEDERATION_INTERNAL_TOKEN:-}' <<< "$api_block"
grep -q 'PLATFORM_URL: ${PLATFORM_URL:-}' <<< "$api_block"
grep -q 'PLATFORM_INTERNAL_TOKEN: ${PLATFORM_INTERNAL_TOKEN:-}' <<< "$api_block"
grep -Fq 'profiles: ["ranking"]' vps/docker-compose.fresh.yml
grep -Fq 'profiles: ["federation"]' vps/docker-compose.fresh.yml
grep -Fq 'profiles: ["nodes"]' vps/docker-compose.fresh.yml
for file in vps/docker-compose.yml vps/.env.example vps/install.sh scripts/install-local-docker.sh vps/backup.sh vps/restore-drill.sh backend/docker-compose.yml; do
  if grep -Eqi 'supabase' "$file"; then
    echo "Retired provider reference remains in active runtime path: $file" >&2
    exit 1
  fi
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

echo "Fresh Appwrite runtime, B2/R2 storage contract and retired-provider exit checks: PASS"
