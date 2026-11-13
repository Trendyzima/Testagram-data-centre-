#!/usr/bin/env bash
set -euo pipefail

for script in vps/backup.sh vps/restore-drill.sh vps/install.sh scripts/install-local-docker.sh; do
  printf "checking shell syntax: %s\\n" "$script"
  bash -n "$script"
done


# Appwrite login must not depend on optional ranking, Federation or node services.
api_block="$(sed -n '/^  api:/,/^  federation-db:/p' vps/docker-compose.fresh.yml)"
if grep -q '^  algorithm:' <<< "$api_block"; then
  echo "The public Backend API must not have an Algorithm startup dependency." >&2
  exit 1
fi
grep -q 'FEED_RANKING_REQUIRED: "false"' <<< "$api_block"
grep -q 'ALGORITHM_ENDPOINT: ${ALGORITHM_ENDPOINT:-http://algorithm:8092}' <<< "$api_block"
grep -q 'FRESH_FEDERATION_INTERNAL_TOKEN: ${FRESH_FEDERATION_INTERNAL_TOKEN:-}' <<< "$api_block"
grep -Fq 'profiles: ["ranking"]' vps/docker-compose.fresh.yml
grep -Fq 'profiles: ["federation"]' vps/docker-compose.fresh.yml
grep -Fq 'profiles: ["nodes"]' vps/docker-compose.fresh.yml

# Keep service-to-service reachability explicit and least-privilege:
# - API and Algorithm share only the isolated ranker network.
# - API, Federation and media share the in-stack services network.
# - Federation and media also attach to platform for their documented
#   cross-stack integration; Algorithm must never join that network.
algorithm_block="$(sed -n '/^  algorithm:/,/^  api:/p' vps/docker-compose.fresh.yml)"
federation_block="$(sed -n '/^  federation-api:/,/^  migrate:/p' vps/docker-compose.fresh.yml)"
grep -Fq 'networks: [services, ranker]' <<< "$api_block"
grep -Fq 'networks: [ranker]' <<< "$algorithm_block"
if grep -Fq 'platform' <<< "$algorithm_block"; then
  echo "Algorithm must remain isolated from the shared platform network." >&2
  exit 1
fi
grep -Fq 'INTERNAL_TOKEN: ${ALGORITHM_INTERNAL_TOKEN:-}' <<< "$algorithm_block"
grep -Fq 'networks: [services, federation, platform]' <<< "$federation_block"
grep -Fq 'BACKEND_INTERNAL_URL: http://api:8081' <<< "$federation_block"
grep -Fq 'BACKEND_INTERNAL_TOKEN: ${FRESH_FEDERATION_INTERNAL_TOKEN:-}' <<< "$federation_block"
media_block="$(sed -n '/^  media:/,/^  federation-db:/p' vps/docker-compose.fresh.yml)"
grep -Fq 'networks: [services, platform]' <<< "$media_block"
grep -Fq 'DATA_CENTRE_MEDIA_API_URL: ${DATA_CENTRE_MEDIA_API_URL:-}' <<< "$api_block"
grep -Fq 'FRESH_DATA_CENTRE_INTERNAL_TOKEN: ${FRESH_DATA_CENTRE_INTERNAL_TOKEN:-}' <<< "$api_block"
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
