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

for cmd in docker awk df grep stat curl; do
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
    POSTGRES_PASSWORD DATABASE_URL TESTAGRAM_DATABASE_URL VPS_INTERNAL_TOKEN VPS_NODE_BOOTSTRAP_TOKEN
    PLATFORM_INTERNAL_TOKEN VIDEO_JWT_SECRET VIDEO_SIGNING_SECRET
    NOTIFICATION_JWT_SECRET NOTIFICATION_INTERNAL_TOKEN ALGORITHM_INTERNAL_TOKEN
    TESTAGRAM_BACKEND_IMAGE TESTAGRAM_ALGORITHM_IMAGE TESTAGRAM_AUTH_PROVIDER
    SUPABASE_PUBLIC_URL ANON_KEY SERVICE_ROLE_KEY
    MEDIA_S3_ENDPOINT MEDIA_S3_ACCESS_KEY MEDIA_S3_SECRET_KEY MEDIA_S3_REGION
    MEDIA_PRIVATE_BUCKET MEDIA_PUBLIC_BUCKET MEDIA_PUBLIC_BASE_URL
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

  canonical_database_url="$(awk 'index($0, "TESTAGRAM_DATABASE_URL=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
  if printf '%s' "$canonical_database_url" | grep -Eq '^postgres(ql)?://[^[:space:]]+@[^/?#]+/[^?]+\?[^#]*sslmode=(require|verify-full)([&]|$)' && ! printf '%s' "$canonical_database_url" | grep -Eiq 'REPLACE_WITH|CHANGE_ME|CHANGEME|TODO|example'; then
    ok "canonical Testagram PostgreSQL URL is configured with TLS verification"
  else
    fail "TESTAGRAM_DATABASE_URL must be a real PostgreSQL URL with sslmode=require or sslmode=verify-full"
  fi

  for image_key in TESTAGRAM_BACKEND_IMAGE TESTAGRAM_ALGORITHM_IMAGE; do
    image_value="$(awk -v key="$image_key" 'index($0, key "=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
    if printf '%s' "$image_value" | grep -Eq '@sha256:[0-9a-fA-F]{64}$'; then
      ok "$image_key is pinned by immutable SHA-256 digest"
    else
      fail "$image_key must use image@sha256:<64 hex characters>; mutable tags such as latest are not accepted"
    fi
  done

  auth_provider="$(awk 'index($0, "TESTAGRAM_AUTH_PROVIDER=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
  case "$auth_provider" in
    appwrite)
      appwrite_endpoint="$(awk 'index($0, "APPWRITE_ENDPOINT=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
      appwrite_project="$(awk 'index($0, "APPWRITE_PROJECT_ID=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
      if printf '%s' "$appwrite_endpoint" | grep -Eq '^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/v1)?$' && [ -n "$appwrite_project" ] && ! printf '%s' "$appwrite_project" | grep -Eq '^(REPLACE_WITH_|CHANGE_ME|TODO)'; then
        ok "Appwrite endpoint and project ID are configured"
      else
        fail "Appwrite is selected but APPWRITE_ENDPOINT or APPWRITE_PROJECT_ID is invalid"
      fi
      appwrite_ingest_token="$(awk 'index($0, "APPWRITE_IDENTITY_INGEST_TOKEN=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
      appwrite_kyc_binding="$(awk 'index($0, "APPWRITE_KYC_BINDING_ENABLED=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
      if [ "$appwrite_kyc_binding" = "true" ]; then
        ok "trusted Appwrite KYC binding is explicitly enabled"
      else
        fail "APPWRITE_KYC_BINDING_ENABLED must be true before Appwrite production cutover"
      fi
      if [ "${#appwrite_ingest_token}" -ge 32 ] && ! printf '%s' "$appwrite_ingest_token" | grep -Eq '^(CHANGE_ME|REPLACE_WITH|TODO)'; then
        ok "trusted identity-ingest token is configured"
      else
        fail "APPWRITE_IDENTITY_INGEST_TOKEN must be a real random secret of at least 32 characters"
      fi
      appwrite_kyc_binding="$(awk 'index($0, "APPWRITE_KYC_BINDING_ENABLED=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
      if [ "$appwrite_kyc_binding" = "true" ]; then
        ok "trusted KYC-to-Appwrite binding is explicitly enabled"
      else
        fail "APPWRITE_KYC_BINDING_ENABLED must be true before selecting Appwrite for production"
      fi
      ;;
    supabase)
      require_appwrite="$(awk 'index($0, "TESTAGRAM_REQUIRE_APPWRITE=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
      if [ "$require_appwrite" = "1" ]; then
        fail "TESTAGRAM_REQUIRE_APPWRITE=1 but TESTAGRAM_AUTH_PROVIDER is still supabase"
      else
        warn "legacy Supabase authentication remains selected; Appwrite production cutover is not complete"
      fi
      ;;
    *)
      fail "TESTAGRAM_AUTH_PROVIDER must be exactly appwrite or supabase"
      ;;
  esac

  media_endpoint="$(awk 'index($0, "MEDIA_S3_ENDPOINT=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
  if printf '%s' "$media_endpoint" | grep -Eq '^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$'; then
    ok "S3-compatible media endpoint uses HTTPS without embedded credentials or paths"
  else
    fail "MEDIA_S3_ENDPOINT must be an HTTPS host (no credentials, query, fragment or path) for production"
  fi

  media_public_url="$(awk 'index($0, "MEDIA_PUBLIC_BASE_URL=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
  if printf '%s' "$media_public_url" | grep -Eq '^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[^?#]*)?$'; then
    ok "public media base URL uses HTTPS"
  else
    fail "MEDIA_PUBLIC_BASE_URL must be an HTTPS URL without credentials, query or fragment"
  fi

  private_bucket="$(awk 'index($0, "MEDIA_PRIVATE_BUCKET=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
  public_bucket="$(awk 'index($0, "MEDIA_PUBLIC_BUCKET=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
  for bucket in "$private_bucket" "$public_bucket"; do
    if printf '%s' "$bucket" | grep -Eq '^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$'; then
      ok "S3 bucket name has a valid DNS-compatible shape"
    else
      fail "S3 bucket names must be 3-63 lowercase DNS-compatible characters"
    fi
  done
  if [ "$private_bucket" = "$public_bucket" ]; then
    fail "MEDIA_PRIVATE_BUCKET and MEDIA_PUBLIC_BUCKET must be different buckets"
  fi

  algorithm_token="$(awk 'index($0, "ALGORITHM_INTERNAL_TOKEN=") == 1 { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$ENV_FILE")"
  if [ "${#algorithm_token}" -ge 32 ]; then
    ok "Algorithm internal token meets the minimum length"
  else
    fail "ALGORITHM_INTERNAL_TOKEN must contain at least 32 characters"
  fi
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
