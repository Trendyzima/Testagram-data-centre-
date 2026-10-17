#!/usr/bin/env bash
set -euo pipefail
ROOT="${TESTAGRAM_PLATFORM_ROOT:-${SUPABASE_DATA_ROOT:-/var/lib/testagram/platform}}"
PROJECT="${TESTAGRAM_PLATFORM_PROJECT_ROOT:-${SUPABASE_PROJECT_ROOT:-$ROOT/project}}"
test -f "$PROJECT/docker-compose.yml" || { echo "Testagram platform runtime is not prepared: $PROJECT"; exit 1; }
cd "$PROJECT"
docker compose config >/dev/null
docker compose ps
