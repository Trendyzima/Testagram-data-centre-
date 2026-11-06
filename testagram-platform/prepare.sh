#!/usr/bin/env bash
set -euo pipefail
ROOT="${TESTAGRAM_PLATFORM_ROOT:-${SUPABASE_DATA_ROOT:-/var/lib/testagram/supabase}}"
UPSTREAM="${TESTAGRAM_PLATFORM_UPSTREAM_ROOT:-${SUPABASE_UPSTREAM_ROOT:-$ROOT/upstream/supabase}}"
PROJECT="${TESTAGRAM_PLATFORM_PROJECT_ROOT:-${SUPABASE_PROJECT_ROOT:-$ROOT/project}}"
REF="${TESTAGRAM_PLATFORM_RELEASE:-${SUPABASE_SELF_HOSTED_REF:-self-hosted/v0.8.2}}"
REPO="https://github.com/supabase/supabase.git"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
command -v git >/dev/null 2>&1 || { echo "git is required"; exit 1; }
command -v cp >/dev/null 2>&1 || { echo "cp is required"; exit 1; }
mkdir -p "$ROOT" "$(dirname "$PROJECT")"
export TESTAGRAM_STORAGE_ROOT="${TESTAGRAM_STORAGE_ROOT:-/var/lib/testagram/storage}"
"$SCRIPT_DIR/storage.sh"
if [ -d "$UPSTREAM/.git" ]; then
  git -C "$UPSTREAM" remote set-url origin "$REPO"
  git -C "$UPSTREAM" fetch --depth=1 origin "$REF"
  git -C "$UPSTREAM" checkout -B "$REF" "origin/$REF"
  git -C "$UPSTREAM" reset --hard "origin/$REF"
else
  mkdir -p "$(dirname "$UPSTREAM")"
  git clone --depth=1 --branch "$REF" "$REPO" "$UPSTREAM"
fi
test "$(git -C "$UPSTREAM" remote get-url origin)" = "$REPO"
test "$(git -C "$UPSTREAM" branch --show-current)" = "$REF"

# Build the new runtime in a staging directory. Never delete the active project
# or its .env; keep a timestamped rollback copy before switching directories.
PROJECT_PARENT="$(dirname "$PROJECT")"
STAGE="$PROJECT_PARENT/.project.stage.$BASHPID"
BACKUP="$PROJECT_PARENT/$(basename "$PROJECT").previous.$(date -u +%Y%m%dT%H%M%SZ)"
cleanup() { rm -rf "$STAGE"; }
trap cleanup EXIT
mkdir -p "$STAGE"
cp -a "$UPSTREAM/docker/." "$STAGE/"
cp "$SCRIPT_DIR/docker-compose.vps.yml" "$STAGE/docker-compose.testagram.yml"
if [ -f "$PROJECT/.env" ]; then
  cp -p "$PROJECT/.env" "$STAGE/.env"
elif [ -f "$STAGE/.env.example" ]; then
  cp "$STAGE/.env.example" "$STAGE/.env"
fi
chmod 0700 "$STAGE"
if [ -f "$STAGE/.env" ]; then chmod 0600 "$STAGE/.env"; fi

if [ -f "$STAGE/.env" ]; then
  set_env() {
    key="$1"; value="$2"
    if grep -q "^$key=" "$STAGE/.env"; then
      KEY="$key" VALUE="$value" python3 - "$STAGE/.env" <<'PY'
import os, pathlib, sys
p=pathlib.Path(sys.argv[1]); key=os.environ["KEY"]; value=os.environ["VALUE"]
lines=p.read_text().splitlines()
replacement=f"{key}={value}"
found=False; out=[]
for line in lines:
    if line.startswith(key+"="):
        if not found: out.append(replacement); found=True
    else: out.append(line)
if not found: out.append(replacement)
p.write_text("\n".join(out)+"\n")
PY
    else
      printf '\n%s=%s\n' "$key" "$value" >> "$STAGE/.env"
    fi
  }
  set_env "TESTAGRAM_STORAGE_ROOT" "$TESTAGRAM_STORAGE_ROOT"
  set_env "COMPOSE_FILE" "docker-compose.yml:docker-compose.testagram.yml"
  set_env "POSTGRES_PASSWORD" "${POSTGRES_PASSWORD:?POSTGRES_PASSWORD is required}"
  set_env "JWT_SECRET" "${JWT_SECRET:?JWT_SECRET is required}"
  set_env "ANON_KEY" "${ANON_KEY:?ANON_KEY is required}"
  set_env "SERVICE_ROLE_KEY" "${SERVICE_ROLE_KEY:?SERVICE_ROLE_KEY is required}"
  set_env "DASHBOARD_USERNAME" "${DASHBOARD_USERNAME:-admin}"
  set_env "DASHBOARD_PASSWORD" "${DASHBOARD_PASSWORD:?DASHBOARD_PASSWORD is required}"
  PUBLIC_URL="${TESTAGRAM_PUBLIC_URL:-${SUPABASE_PUBLIC_URL:?Set TESTAGRAM_PUBLIC_URL (legacy SUPABASE_PUBLIC_URL is also accepted)}}"
  set_env "SUPABASE_PUBLIC_URL" "$PUBLIC_URL"
  set_env "API_EXTERNAL_URL" "${TESTAGRAM_API_EXTERNAL_URL:-${SUPABASE_API_EXTERNAL_URL:-$PUBLIC_URL/auth/v1}}"
  set_env "SITE_URL" "${SITE_URL:-$PUBLIC_URL}"
  set_env "API_GW_HTTP_PORT" "127.0.0.1:8000"
  set_env "POSTGRES_HOST" "db"
  set_env "POSTGRES_PORT" "5432"
  set_env "POSTGRES_DB" "postgres"
fi

if [ -e "$PROJECT" ]; then
  mv "$PROJECT" "$BACKUP"
  chmod 0700 "$BACKUP"
fi
mv "$STAGE" "$PROJECT"
trap - EXIT
printf 'Testagram platform runtime prepared at %s\n' "$PROJECT"
if [ -d "$BACKUP" ]; then printf 'Previous runtime retained for rollback at %s\n' "$BACKUP"; fi
printf 'Upstream compatibility release: %s\n' "$REF"
printf 'Upstream source SHA: %s\n' "$(git -C "$UPSTREAM" rev-parse HEAD)"
