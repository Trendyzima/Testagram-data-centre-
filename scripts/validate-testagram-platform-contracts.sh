#!/usr/bin/env bash
set -euo pipefail

for script in vps/backup.sh vps/restore-drill.sh vps/install.sh scripts/install-local-docker.sh; do
  bash -n "$script"
done

cmp -s vps/docker-compose.yml vps/docker-compose.fresh.yml
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

# Backup and restore must cover the Data Centre infrastructure database only.
grep -q 'DATA_CENTRE_DATABASE_URL' vps/backup.sh
grep -q 'pg_dump --dbname=' vps/backup.sh
grep -q 'testagram_hosting.backup_manifests' vps/restore-drill.sh
if grep -Eq 'SUPABASE|supabase|docker compose.*stop|supabase_services' vps/backup.sh vps/restore-drill.sh; then
  echo "Backup/restore scripts must not depend on the retired provider or stop product services." >&2
  exit 1
fi

echo "Fresh Appwrite runtime, B2/R2 storage contract and retired-provider exit checks: PASS"
