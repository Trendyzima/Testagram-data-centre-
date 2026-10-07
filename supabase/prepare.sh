#!/usr/bin/env bash
set -euo pipefail
ROOT="${SUPABASE_DATA_ROOT:-/var/lib/testagram/supabase}"
UPSTREAM="${SUPABASE_UPSTREAM_ROOT:-$ROOT/upstream/supabase}"
PROJECT="${SUPABASE_PROJECT_ROOT:-$ROOT/project}"
REF="${SUPABASE_SELF_HOSTED_REF:-self-hosted/v0.8.2}"
REPO="https://github.com/supabase/supabase.git"
command -v git >/dev/null 2>&1 || { echo "git is required"; exit 1; }
command -v cp >/dev/null 2>&1 || { echo "cp is required"; exit 1; }
mkdir -p "$ROOT" "$PROJECT"
TESTAGRAM_STORAGE_ROOT="${TESTAGRAM_STORAGE_ROOT:-/var/lib/testagram/storage}"
export TESTAGRAM_STORAGE_ROOT
"$(dirname "$0")/storage.sh"
if [ -d "$UPSTREAM/.git" ]; then
  git -C "$UPSTREAM" remote set-url origin "$REPO"
  git -C "$UPSTREAM" fetch --depth=1 origin "$REF"
  git -C "$UPSTREAM" checkout -B "$REF" "origin/$REF"
  git -C "$UPSTREAM" reset --hard "origin/$REF"
else
  rm -rf "$UPSTREAM"
  git clone --depth=1 --branch "$REF" "$REPO" "$UPSTREAM"
fi
test "$(git -C "$UPSTREAM" remote get-url origin)" = "$REPO"
test "$(git -C "$UPSTREAM" branch --show-current)" = "$REF"
rm -rf "$PROJECT"
mkdir -p "$PROJECT"
cp -a "$UPSTREAM/docker/." "$PROJECT/"
cp "$(dirname "$0")/docker-compose.vps.yml" "$PROJECT/docker-compose.testagram.yml"
if [ -f "$PROJECT/.env.example" ] && [ ! -f "$PROJECT/.env" ]; then cp "$PROJECT/.env.example" "$PROJECT/.env"; fi
if [ -f "$PROJECT/.env" ]; then
  if grep -q "^TESTAGRAM_STORAGE_ROOT=" "$PROJECT/.env"; then
    sed -i "s#^TESTAGRAM_STORAGE_ROOT=.*#TESTAGRAM_STORAGE_ROOT=$TESTAGRAM_STORAGE_ROOT#" "$PROJECT/.env"
  else
    printf "\nTESTAGRAM_STORAGE_ROOT=$TESTAGRAM_STORAGE_ROOT\n" >> "$PROJECT/.env"
  fi
fi
if [ -f "$PROJECT/.env" ]; then
  if grep -q '^COMPOSE_FILE=' "$PROJECT/.env"; then
    sed -i 's#^COMPOSE_FILE=.*#COMPOSE_FILE=docker-compose.yml:docker-compose.testagram.yml#' "$PROJECT/.env"
  else
    printf '\nCOMPOSE_FILE=docker-compose.yml:docker-compose.testagram.yml\n' >> "$PROJECT/.env"
  fi
fi
# Copy deployment-owned runtime settings into the VPS-only Supabase .env.
set_env() {
  key="$1"; value="$2"
  if grep -q "^$key=" "$PROJECT/.env"; then
    sed -i "s#^$key=.*#$key=$value#" "$PROJECT/.env"
  else
    printf "\n%s=%s\n" "$key" "$value" >> "$PROJECT/.env"
  fi
}
set_env "POSTGRES_PASSWORD" "${POSTGRES_PASSWORD:?POSTGRES_PASSWORD is required}"
set_env "JWT_SECRET" "${JWT_SECRET:?JWT_SECRET is required}"
set_env "ANON_KEY" "${ANON_KEY:?ANON_KEY is required}"
set_env "SERVICE_ROLE_KEY" "${SERVICE_ROLE_KEY:?SERVICE_ROLE_KEY is required}"
set_env "DASHBOARD_USERNAME" "${DASHBOARD_USERNAME:-admin}"
set_env "DASHBOARD_PASSWORD" "${DASHBOARD_PASSWORD:?DASHBOARD_PASSWORD is required}"
set_env "SUPABASE_PUBLIC_URL" "${SUPABASE_PUBLIC_URL:?SUPABASE_PUBLIC_URL is required}"
set_env "API_EXTERNAL_URL" "${SUPABASE_API_EXTERNAL_URL:-${SUPABASE_PUBLIC_URL}/auth/v1}"
set_env "API_GW_HTTP_PORT" "127.0.0.1:8000"
set_env "POSTGRES_HOST" "db"
set_env "POSTGRES_PORT" "5432"
set_env "POSTGRES_DB" "postgres"
printf 'Official Supabase release prepared at %s\n' "$PROJECT"
printf 'Source ref: %s\n' "$REF"
printf 'Source SHA: %s\n' "$(git -C "$UPSTREAM" rev-parse HEAD)"
