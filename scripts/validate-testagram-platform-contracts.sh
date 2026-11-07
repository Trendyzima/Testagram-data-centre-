#!/usr/bin/env bash
set -euo pipefail

for script in vps/backup.sh vps/restore-drill.sh vps/install.sh scripts/install-local-docker.sh; do
  bash -n "$script"
done

cmp -s vps/docker-compose.yml vps/docker-compose.fresh.yml

# Login must not be blocked by optional ranking or federation services.
api_block="$(sed -n '/^  api:/,/^  federation-db:/p' vps/docker-compose.fresh.yml)"
grep -q 'algorithm:' <<< "$api_block"
grep -q 'condition: service_started' <<< "$api_block"
grep -q 'FEED_RANKING_REQUIRED: "false"' <<< "$api_block"
edge_block="$(sed -n '/^  edge:/,/^networks:/p' vps/docker-compose.fresh.yml)"
grep -q 'api:' <<< "$edge_block"
if grep -q 'federation-api' <<< "$edge_block"; then
  echo "Optional Federation health must not gate the public API edge." >&2
  exit 1
fi
if grep -Eq 'ep-example|TESTAGRAM_ROLE|FEDERATION_ROLE' vps/fresh.env.example; then
  echo "Fresh environment template must not present example Neon hostnames or role names as deployable values." >&2
  exit 1
fi
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

echo "Fresh Appwrite login path, optional-service isolation, B2/R2 storage contract and retired-provider exit checks: PASS"
