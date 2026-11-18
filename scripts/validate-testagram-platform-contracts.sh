#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILE="vps/docker-compose.yml"

for script in vps/backup.sh vps/restore-drill.sh vps/install.sh scripts/install-local-docker.sh; do
  printf "checking shell syntax: %s\n" "$script"
  bash -n "$script"
done

test -f "$COMPOSE_FILE"

# The canonical Backend API is the sole product API. Ranking is optional and
# must not become an API startup dependency.
api_block="$(sed -n '/^  api:/,/^  media-migrate:/p' "$COMPOSE_FILE")"
algorithm_block="$(sed -n '/^  algorithm:/,/^  api:/p' "$COMPOSE_FILE")"
grep -Fq 'image: ${TESTAGRAM_BACKEND_IMAGE:?Set TESTAGRAM_BACKEND_IMAGE' <<< "$api_block"
grep -Fq 'LISTEN_ADDR: :8080' <<< "$api_block"
grep -Fq 'INTERNAL_LISTEN_ADDR: :8082' <<< "$api_block"
grep -Fq 'TESTAGRAM_AUTH_JWT_SECRET:' <<< "$api_block"
grep -Fq 'ALGORITHM_URL: ${ALGORITHM_URL:-http://algorithm:8092}' <<< "$api_block"
grep -Fq 'ALGORITHM_INTERNAL_TOKEN:' <<< "$api_block"
grep -Fq 'profiles: ["ranking"]' <<< "$algorithm_block"
grep -Fq 'networks: [ranker]' <<< "$algorithm_block"
grep -Fq 'INTERNAL_TOKEN: ${ALGORITHM_INTERNAL_TOKEN:-}' <<< "$algorithm_block"
if grep -Fq 'depends_on:' <<< "$api_block"; then
  echo "The canonical Backend API must not require optional ranking at startup." >&2
  exit 1
fi

# Backend and optional ranker communicate on the isolated ranker network.
# The API also joins the private platform network for Federation integration.
grep -Fq 'ranker: {}' <<< "$api_block"
grep -Fq 'aliases: [testagram-backend-api]' <<< "$api_block"
if grep -Fq 'platform' <<< "$algorithm_block"; then
  echo "Algorithm must remain isolated from the shared platform network." >&2
  exit 1
fi

federation_block="$(sed -n '/^  federation-api:/,/^  migrate:/p' "$COMPOSE_FILE")"
grep -Fq 'profiles: ["federation"]' <<< "$federation_block"
grep -Fq 'BACKEND_INTERNAL_URL: http://testagram-backend-api:8082' <<< "$federation_block"
grep -Fq 'BACKEND_INTERNAL_TOKEN: ${FEDERATION_BACKEND_INTERNAL_TOKEN:-}' <<< "$federation_block"
grep -Fq 'FEDERATION_INGRESS_TOKEN: ${FEDERATION_INGRESS_TOKEN:-}' <<< "$federation_block"
grep -Fq 'networks: [services, federation, platform]' <<< "$federation_block"

media_block="$(sed -n '/^  media:/,/^  federation-db:/p' "$COMPOSE_FILE")"
grep -Fq 'profiles: ["media"]' <<< "$media_block"
grep -Fq 'networks: [services, platform]' <<< "$media_block"
grep -Fq 'B2_S3_ENDPOINT:' <<< "$media_block"
grep -Fq 'CLOUDFLARE_R2_ENDPOINT:' <<< "$media_block"
grep -Fq 'VPS_MINIO_METADATA_BUCKET:' <<< "$media_block"
grep -Fq 'CLOUDFLARE_R2_METADATA_BUCKET:' <<< "$media_block"
grep -Fq 'FRESH_MEDIA_ENABLED: ${FRESH_MEDIA_ENABLED:-false}' <<< "$api_block"
grep -Fq 'DATA_CENTRE_MEDIA_API_URL:' <<< "$api_block"
grep -Fq 'FRESH_DATA_CENTRE_INTERNAL_TOKEN:' <<< "$api_block"

for file in vps/docker-compose.yml vps/.env.example vps/fresh.env.example vps/install.sh scripts/install-local-docker.sh vps/backup.sh vps/restore-drill.sh backend/docker-compose.yml; do
  if grep -Eqi 'supabase' "$file"; then
    echo "Retired provider reference remains in active runtime path: $file" >&2
    exit 1
  fi
done

grep -q 'Backblaze B2' docs/platform/media-storage-architecture.md
grep -q 'Cloudflare R2' docs/platform/media-storage-architecture.md
grep -q '/readyz' docs/platform/openapi/internal-platform.yaml
grep -q '/v1/hosting/backups' docs/platform/openapi/hosting-control-plane.yaml

echo "Canonical Backend runtime, optional ranker, Federation, B2/R2 storage and provider-exit checks: PASS"
