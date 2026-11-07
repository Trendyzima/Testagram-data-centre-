#!/usr/bin/env bash
set -euo pipefail

ROOT="${TESTAGRAM_ROOT:-/opt/testagram}"
ENV_FILE="${TESTAGRAM_FRESH_ENV_FILE:-$ROOT/vps/fresh.env}"
REPO_URL="${TESTAGRAM_REPO_URL:-https://github.com/Trendyzima/Testagram-data-centre-.git}"
BRANCH="${TESTAGRAM_REPO_BRANCH:-main}"

command -v docker >/dev/null || { echo "Docker Engine is required" >&2; exit 1; }
docker compose version >/dev/null || { echo "Docker Compose v2 is required" >&2; exit 1; }
command -v git >/dev/null || { echo "git is required" >&2; exit 1; }
mkdir -p "$ROOT"
if [ ! -d "$ROOT/.git" ]; then
  git clone --branch "$BRANCH" "$REPO_URL" "$ROOT"
else
  git -C "$ROOT" fetch origin "$BRANCH"
  git -C "$ROOT" checkout "$BRANCH"
  git -C "$ROOT" reset --hard "origin/$BRANCH"
fi

if [ ! -f "$ENV_FILE" ]; then
  cp "$ROOT/vps/fresh.env.example" "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  echo "Created $ENV_FILE from the fresh Appwrite/Neon/B2/R2 template."
  echo "Set the Appwrite project/API credentials and core image digests, then rerun."
  echo "Ranking, Federation and node control-plane can be enabled independently after their dependencies are provisioned."
  exit 2
fi
chmod 600 "$ENV_FILE"

# Read plain KEY=value entries without sourcing the user's environment file.
# This avoids executing shell substitutions from a deployment configuration file.
env_value() {
  local key="$1"
  local line
  line="$(grep -E "^$key=" "$ENV_FILE" | tail -n 1 || true)"
  printf '%s' "${line#*=}"
}

require_digest() {
  local key="$1"
  local value
  value="$(env_value "$key")"
  if [[ ! "$value" =~ ^[^[:space:]@]+@sha256:[a-fA-F0-9]{64}$ ]]; then
    echo "$key must be an immutable image reference ending in @sha256:<64 hex characters>." >&2
    exit 2
  fi
}

require_secret() {
  local key="$1"
  local value
  value="$(env_value "$key")"
  if [ "${#value}" -lt 32 ] || [[ "$value" == *$'\r'* || "$value" == *$'\n'* ]]; then
    echo "$key must contain at least 32 characters and no line breaks." >&2
    exit 2
  fi
}

require_value() {
  local key="$1"
  local value
  value="$(env_value "$key")"
  if [ -z "$value" ]; then
    echo "$key is required when its optional service profile is enabled." >&2
    exit 2
  fi
}

for key in TESTAGRAM_ENABLE_RANKING TESTAGRAM_ENABLE_FEDERATION TESTAGRAM_ENABLE_NODES; do
  value="$(env_value "$key")"
  if [ "$value" != "true" ] && [ "$value" != "false" ]; then
    echo "$key must be exactly true or false." >&2
    exit 2
  fi
done

# The public login/API edge always runs and requires only the core Backend and Caddy images.
if grep -Eq 'REPLACE_WITH|REPLACE_PASSWORD|ops@example.com|ep-example|TESTAGRAM_ROLE|FEDERATION_ROLE' "$ENV_FILE"; then
  echo "Fresh env still contains placeholders. Replace core deployment placeholders before deployment." >&2
  exit 2
fi
require_digest TESTAGRAM_FRESH_BACKEND_IMAGE
require_digest CADDY_IMAGE

profiles=()
if [ "$(env_value TESTAGRAM_ENABLE_RANKING)" = "true" ]; then
  require_digest TESTAGRAM_ALGORITHM_IMAGE
  require_secret ALGORITHM_INTERNAL_TOKEN
  require_value ALGORITHM_ENDPOINT
  profiles+=(--profile ranking)
fi
if [ "$(env_value TESTAGRAM_ENABLE_FEDERATION)" = "true" ]; then
  require_digest TESTAGRAM_FEDERATION_IMAGE
  require_secret FRESH_FEDERATION_INTERNAL_TOKEN
  require_value FEDERATION_DATABASE_URL
  require_value FEDERATION_BACKEND_INTERNAL_URL
  profiles+=(--profile federation)
fi
if [ "$(env_value TESTAGRAM_ENABLE_NODES)" = "true" ]; then
  require_digest POSTGRES_CLIENT_IMAGE
  require_value DATA_CENTRE_DATABASE_URL
  require_secret VPS_INTERNAL_TOKEN
  require_secret VPS_NODE_BOOTSTRAP_TOKEN
  profiles+=(--profile nodes)
  cp "$ROOT/vps/fresh/Caddyfile.nodes" "$ROOT/vps/fresh/Caddyfile"
fi

compose=(docker compose --env-file "$ENV_FILE" -f "$ROOT/vps/docker-compose.fresh.yml")
"${compose[@]}" "${profiles[@]}" config -q
"${compose[@]}" "${profiles[@]}" up -d
"${compose[@]}" "${profiles[@]}" ps
