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

# Data Centre-owned media lifecycle API and migration.
grep -q '008_media_storage_control_plane.sql' vps/docker-compose.fresh.yml
grep -q 'MEDIA_STORAGE_ENABLED' vps/docker-compose.fresh.yml
grep -q 'FRESH_DATA_CENTRE_INTERNAL_TOKEN' vps/docker-compose.fresh.yml
grep -q '/internal/v1/media/upload-url' control-plane/main.go
grep -q '/internal/v1/media/verify' control-plane/main.go
grep -q 'media_replication_jobs' migrations/008_media_storage_control_plane.sql
grep -q 'DownloadURLAuthorized' control-plane/media.go
echo "Fresh Appwrite runtime, B2/R2 storage contract and retired-provider exit checks: PASS"
