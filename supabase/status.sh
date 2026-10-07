#!/usr/bin/env bash
set -euo pipefail
ROOT="${SUPABASE_DATA_ROOT:-/var/lib/testagram/supabase}"
PROJECT="${SUPABASE_PROJECT_ROOT:-$ROOT/project}"
test -f "$PROJECT/docker-compose.yml" || { echo "Supabase project is not prepared: $PROJECT"; exit 1; }
cd "$PROJECT"
docker compose config >/dev/null
docker compose ps
