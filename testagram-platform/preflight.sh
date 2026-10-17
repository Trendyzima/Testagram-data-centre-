#!/usr/bin/env bash
# Read-only deployment preflight. Does not start containers, change DNS, or touch data.
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="${TESTAGRAM_ENV_FILE:-$REPO_ROOT/.env}"
STORAGE_ROOT="${TESTAGRAM_STORAGE_ROOT:-/var/lib/testagram/storage}"
STRICT="${TESTAGRAM_PREFLIGHT_STRICT_RESOURCES:-0}"
MIN_FREE_GB="${TESTAGRAM_PREFLIGHT_MIN_FREE_GB:-20}"
MIN_RAM_GB="${TESTAGRAM_PREFLIGHT_MIN_RAM_GB:-4}"
errors=0
warnings=0

fail() { printf 'FAIL  %s\n' "$1" >&2; errors=$((errors + 1)); }
warn() { printf 'WARN  %s\n' "$1" >&2; warnings=$((warnings + 1)); }
ok() { printf 'OK    %s\n' "$1"; }

for cmd in docker awk df grep stat; do
  if command -v "$cmd" >/dev/null 2>&1; then ok "command $cmd"; else fail "required command missing: $cmd"; fi
done

if ! command -v docker >/dev/null 2>&1; then
  printf 'Preflight cannot continue without Docker.\n' >&2
  exit 1
fi
if docker compose version >/dev/null 2>&1; then
  ok "Docker Compose v2 available"
else
  fail "Docker Compose v2 unavailable"
fi

if [ ! -f "$ENV_FILE" ]; then
  fail "environment file missing (set TESTAGRAM_ENV_FILE or create $ENV_FILE)"
else
  ok "environment file exists"
  mode="$(stat -c '%a' "$ENV_FILE" 2>/dev/null || stat -f '%Lp' "$ENV_FILE" 2>/dev/null || printf 'unknown')"
  case "$mode" in
    600|400|700|500) ok "environment file permissions are restrictive ($mode)" ;;
    *) fail "environment file permissions are not restrictive ($mode); use chmod 600 on the env file" ;;
  esac

  # Parse as plain KEY=VALUE data; never source/evaluate a secrets file.
  required_keys=(
    POSTGRES_PASSWORD DATABASE_URL VPS_INTERNAL_TOKEN VPS_NODE_BOOTSTRAP_TOKEN
    PLATFORM_INTERNAL_TOKEN VIDEO_JWT_SECRET VIDEO_SIGNING_SECRET
    NOTIFICATION_JWT_SECRET NOTIFICATION_INTERNAL_TOKEN
  )
  for key in "${required_keys[@]}"; do
    value="$(awk -v key="$key" 'index($0, key "=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
    if [ -z "$value" ]; then
      fail "required environment key missing or empty: $key"
    elif printf '%s' "$value" | grep -Eq '^(REPLACE_WITH_|CHANGE_ME|CHANGEME|TODO|example$)'; then
      fail "placeholder value detected for required key: $key"
    else
      ok "required environment key present: $key"
    fi
  done
fi

if docker info >/dev/null 2>&1; then ok "Docker daemon reachable"; else fail "Docker daemon is not reachable"; fi

if [ -f /proc/meminfo ]; then
  mem_kb="$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)"
  min_mem_kb=$((MIN_RAM_GB * 1024 * 1024))
  if [ "${mem_kb:-0}" -ge "$min_mem_kb" ]; then
    ok "RAM meets configured baseline (${MIN_RAM_GB} GiB)"
  elif [ "$STRICT" = "1" ]; then
    fail "RAM below configured baseline (${MIN_RAM_GB} GiB); raise resources or lower the baseline explicitly"
  else
    warn "RAM below recommended baseline (${MIN_RAM_GB} GiB); use lower concurrency/resource limits on small hosts"
  fi
fi

probe_path="$STORAGE_ROOT"
while [ ! -d "$probe_path" ] && [ "$probe_path" != "/" ]; do probe_path="$(dirname "$probe_path")"; done
free_kb="$(df -Pk "$probe_path" | awk 'NR==2 {print $4}')"
min_free_kb=$((MIN_FREE_GB * 1024 * 1024))
if [ "${free_kb:-0}" -ge "$min_free_kb" ]; then
  ok "free disk space meets configured baseline (${MIN_FREE_GB} GiB)"
elif [ "$STRICT" = "1" ]; then
  fail "free disk space below configured baseline (${MIN_FREE_GB} GiB)"
else
  warn "free disk space below recommended baseline (${MIN_FREE_GB} GiB); size media retention and backup capacity carefully"
fi

if [ -f "$REPO_ROOT/vps/docker-compose.yml" ] && [ -f "$ENV_FILE" ]; then
  if docker compose --env-file "$ENV_FILE" -f "$REPO_ROOT/vps/docker-compose.yml" config --quiet; then
    ok "VPS Compose configuration renders"
  else
    fail "VPS Compose configuration did not render; inspect missing variables or YAML errors"
  fi
else
  warn "skipping Compose render because the compose file or environment file is missing"
fi

printf '\nPreflight summary: %s error(s), %s warning(s). No deployment was performed.\n' "$errors" "$warnings"
if [ "$errors" -gt 0 ]; then exit 1; fi
