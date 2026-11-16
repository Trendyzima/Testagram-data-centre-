#!/usr/bin/env bash
set -euo pipefail

for script in vps/backup.sh vps/restore-drill.sh vps/install.sh scripts/install-local-docker.sh; do
  printf "checking shell syntax: %s\\n" "$script"
  bash -n "$script"
done

compose="vps/docker-compose.yml"
test -f "$compose"

# The Backend repository owns the canonical public API and HTTPS edge.
# The Data Centre must not start a parallel API or bind public web ports.
if grep -Eq '^  (api|edge):' "$compose"; then
  echo "Data Centre compose must not duplicate the Backend API or public edge." >&2
  exit 1
fi

# Optional ranking remains isolated from media, federation and Backend services.
algorithm_block="$(sed -n '/^  algorithm:/,/^  media-migrate:/p' "$compose")"
grep -Fq 'profiles: ["ranking"]' <<< "$algorithm_block"
grep -Fq 'networks: [ranker]' <<< "$algorithm_block"
if grep -Fq 'platform' <<< "$algorithm_block" || grep -Fq 'federation' <<< "$algorithm_block"; then
  echo "Ranking must remain isolated from the shared platform and federation networks." >&2
  exit 1
fi

# Data Centre media remains private, independently credentialed and dual-provider.
media_block="$(sed -n '/^  media:/,/^  federation-db:/p' "$compose")"
grep -Fq 'profiles: ["media"]' <<< "$media_block"
grep -Fq 'networks: [services, platform]' <<< "$media_block"
grep -Fq 'MEDIA_INTERNAL_TOKEN: ${FRESH_DATA_CENTRE_INTERNAL_TOKEN:?Set FRESH_DATA_CENTRE_INTERNAL_TOKEN}' <<< "$media_block"
grep -Fq 'MEDIA_DATABASE_URL: ${MEDIA_DATABASE_URL:?Set a dedicated MEDIA_DATABASE_URL}' <<< "$media_block"
grep -Fq 'B2_S3_ENDPOINT: ${B2_S3_ENDPOINT:?Set B2_S3_ENDPOINT}' <<< "$media_block"
grep -Fq 'CLOUDFLARE_R2_ENDPOINT: ${CLOUDFLARE_R2_ENDPOINT:?Set CLOUDFLARE_R2_ENDPOINT}' <<< "$media_block"
if grep -Fq 'ports:' <<< "$media_block"; then
  echo "Media must remain private and must not publish a host port." >&2
  exit 1
fi

# Federation remains private and uses two distinct directional credentials.
federation_block="$(sed -n '/^  federation-api:/,/^  migrate:/p' "$compose")"
grep -Fq 'profiles: ["federation"]' <<< "$federation_block"
grep -Fq 'networks: [services, federation, platform]' <<< "$federation_block"
grep -Fq 'BACKEND_INTERNAL_URL: http://testagram-backend-api:8082' <<< "$federation_block"
grep -Fq 'BACKEND_INTERNAL_TOKEN: ${FEDERATION_BACKEND_INTERNAL_TOKEN:?Set FEDERATION_BACKEND_INTERNAL_TOKEN}' <<< "$federation_block"
grep -Fq 'FEDERATION_INGRESS_TOKEN: ${FEDERATION_INGRESS_TOKEN:?Set FEDERATION_INGRESS_TOKEN}' <<< "$federation_block"
if grep -Fq 'ports:' <<< "$federation_block"; then
  echo "Federation ingress must remain private behind the Backend edge." >&2
  exit 1
fi

for file in "$compose" vps/.env.example vps/fresh.env.example vps/install.sh scripts/install-local-docker.sh vps/backup.sh vps/restore-drill.sh backend/docker-compose.yml backend/.env.example; do
  if grep -Eqi 'supabase' "$file"; then
    echo "Unapproved external database dependency remains in active runtime path: $file" >&2
    exit 1
  fi
done

grep -q 'Backblaze B2' docs/platform/media-storage-architecture.md
grep -q 'Cloudflare R2' docs/platform/media-storage-architecture.md
! grep -Eq '^  (retention|retention-worker):' "$compose"
grep -q '/readyz' docs/platform/openapi/internal-platform.yaml
grep -q '/v1/hosting/backups' docs/platform/openapi/hosting-control-plane.yaml

echo "Data Centre service boundaries, private media/federation wiring and safety checks: PASS"
