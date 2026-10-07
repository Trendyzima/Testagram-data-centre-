#!/usr/bin/env bash
set -euo pipefail
ROOT="${TESTAGRAM_ROOT:-/opt/testagram}"
SUPABASE_ROOT="${SUPABASE_DATA_ROOT:-/var/lib/testagram/supabase}"
STORAGE_ROOT="${TESTAGRAM_STORAGE_ROOT:-/var/lib/testagram/storage}"
ENV_FILE="${TESTAGRAM_ENV_FILE:-$ROOT/.env}"
REPO_URL="${TESTAGRAM_REPO_URL:-https://github.com/Trendyzima/Testagram-data-centre-.git}"
command -v git >/dev/null || { echo "git is required"; exit 1; }
command -v docker >/dev/null || { echo "Docker Engine is required"; exit 1; }
mkdir -p "$ROOT" "$SUPABASE_ROOT" "$STORAGE_ROOT"
if [ ! -d "$ROOT/.git" ]; then git clone "$REPO_URL" "$ROOT"; else git -C "$ROOT" fetch origin main && git -C "$ROOT" reset --hard origin/main; fi
if [ ! -f "$ENV_FILE" ]; then cp "$ROOT/vps/.env.example" "$ENV_FILE"; chmod 600 "$ENV_FILE"; echo "Created $ENV_FILE; set production values and rerun."; exit 1; fi
export TESTAGRAM_STORAGE_ROOT SUPABASE_DATA_ROOT
"$ROOT/supabase/prepare.sh"
cd "$ROOT/supabase/project"
docker compose pull
docker compose up -d --wait
cd "$ROOT"
docker compose --env-file "$ENV_FILE" -f vps/docker-compose.yml up -d --build --wait
for f in migrations/*.sql; do docker exec supabase-db psql -U postgres -d postgres -v ON_ERROR_STOP=1 < "$f"; done
docker compose --env-file "$ENV_FILE" -f vps/docker-compose.yml ps
cd "$ROOT/supabase/project"
docker compose ps
