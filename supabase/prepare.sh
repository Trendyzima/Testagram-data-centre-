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
  if grep -q '^COMPOSE_FILE=' "$PROJECT/.env"; then
    sed -i 's#^COMPOSE_FILE=.*#COMPOSE_FILE=docker-compose.yml:docker-compose.testagram.yml#' "$PROJECT/.env"
  else
    printf '\nCOMPOSE_FILE=docker-compose.yml:docker-compose.testagram.yml\n' >> "$PROJECT/.env"
  fi
fi
printf 'Official Supabase release prepared at %s\n' "$PROJECT"
printf 'Source ref: %s\n' "$REF"
printf 'Source SHA: %s\n' "$(git -C "$UPSTREAM" rev-parse HEAD)"
