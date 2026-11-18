#!/usr/bin/env bash
set -euo pipefail

ROOT="${TESTAGRAM_ROOT:-/opt/testagram}"
BRANCH="${TESTAGRAM_REPO_BRANCH:-main}"
REPO_URL="${TESTAGRAM_REPO_URL:-https://github.com/Trendyzima/Testagram-data-centre-.git}"
ENV_FILE="${TESTAGRAM_VPS_ENV_FILE:-${TESTAGRAM_FRESH_ENV_FILE:-$ROOT/vps/fresh.env}}"
STORAGE_ROOT="${TESTAGRAM_STORAGE_ROOT:-/var/lib/testagram/storage}"

command -v git >/dev/null || { echo "git is required" >&2; exit 1; }
command -v docker >/dev/null || { echo "Docker Engine is required" >&2; exit 1; }
docker compose version >/dev/null || { echo "Docker Compose v2 is required" >&2; exit 1; }
command -v systemctl >/dev/null || { echo "systemd is required for the node agent" >&2; exit 1; }

mkdir -p "$ROOT"
if [ ! -d "$ROOT/.git" ]; then
  git clone --branch "$BRANCH" "$REPO_URL" "$ROOT"
else
  git -C "$ROOT" fetch origin "$BRANCH"
  git -C "$ROOT" checkout "$BRANCH"
  git -C "$ROOT" reset --hard "origin/$BRANCH"
fi

if [ ! -f "$ENV_FILE" ]; then
  mkdir -p "$(dirname "$ENV_FILE")"
  cp "$ROOT/vps/fresh.env.example" "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  echo "Created $ENV_FILE. Set the canonical Backend image digest, native-auth/database settings, Caddy/Postgres digests and node tokens, then rerun." >&2
  exit 2
fi

set -a
. "$ENV_FILE"
set +a

for key in TESTAGRAM_BACKEND_IMAGE CADDY_IMAGE POSTGRES_CLIENT_IMAGE DATABASE_URL TESTAGRAM_AUTH_JWT_SECRET ALLOWED_ORIGINS DATA_CENTRE_DATABASE_URL TLS_ADMIN_EMAIL VPS_INTERNAL_TOKEN VPS_NODE_BOOTSTRAP_TOKEN; do
  value="${!key:-}"
  if [ -z "$value" ] || [[ "$value" == *REPLACE* ]]; then
    echo "Required canonical deployment setting is missing or still a placeholder: $key" >&2
    exit 2
  fi
done
for key in TESTAGRAM_BACKEND_IMAGE CADDY_IMAGE POSTGRES_CLIENT_IMAGE; do
  value="${!key}"
  if [[ ! "$value" =~ @sha256:[a-f0-9]{64}$ ]]; then
    echo "$key must be pinned to an immutable sha256 image digest." >&2
    exit 2
  fi
done
if [ "${#TESTAGRAM_AUTH_JWT_SECRET}" -lt 32 ] || [ "${#VPS_INTERNAL_TOKEN}" -lt 32 ] || [ "${#VPS_NODE_BOOTSTRAP_TOKEN}" -lt 32 ]; then
  echo "Native-auth JWT, control-plane and node bootstrap secrets must contain at least 32 characters." >&2
  exit 2
fi
if [[ "$VPS_NODE_BOOTSTRAP_TOKEN" == *$'\n'* || "$VPS_NODE_BOOTSTRAP_TOKEN" == *$'\r'* ]]; then
  echo "Node bootstrap token must be a single line." >&2
  exit 2
fi

if [ "${VPS_ENABLE_WATCHDOG_ACTIVATION:-false}" = "true" ]; then
  compose_version="$(docker compose version --short | sed 's/^v//')"
  if ! printf '2.17.0\n%s\n' "$compose_version" | sort -V -C; then
    echo "Watchdog activation requires Docker Compose v2.17.0 or newer for health-wait support." >&2
    exit 2
  fi
  if [ -z "${VPS_WATCHDOG_INTERNAL_TOKEN:-}" ] || [ "${#VPS_WATCHDOG_INTERNAL_TOKEN}" -lt 32 ]; then
    echo "Watchdog activation requires a dedicated VPS_WATCHDOG_INTERNAL_TOKEN of at least 32 characters." >&2
    exit 2
  fi
  for key in VPS_WATCHDOG_DATA_CENTRE_COMPOSE_FILE VPS_WATCHDOG_CDN_COMPOSE_FILE; do
    value="${!key:-}"
    if [[ "$value" != /* ]] || [ ! -f "$value" ]; then
      echo "Watchdog activation requires an existing absolute path in $key." >&2
      exit 2
    fi
  done
fi

if grep -Eq 'REPLACE_WITH|REPLACE_PASSWORD|REPLACE_WITH_64_HEX_DIGEST|ops@example.com' "$ENV_FILE"; then
  echo "Runtime env still contains placeholders. Set real secrets and immutable image digests before deployment." >&2
  exit 2
fi
mkdir -p "$STORAGE_ROOT/nodes" "$ROOT/bin" /etc/testagram
chmod 0750 "$STORAGE_ROOT" "$STORAGE_ROOT/nodes"
# Backend owns and creates the private shared networks. Deploy its Compose stack first.
for network in testagram-platform testagram-ranker; do
  if ! docker network inspect "$network" >/dev/null 2>&1; then
    echo "Required private Docker network $network is missing. Start the Backend self-hosted Compose stack first." >&2
    exit 2
  fi
done
# Start optional private services only when their complete, directional
# credentials and immutable images are already present in fresh.env.
# The services remain fail-closed: Federation workers are governed by their
# individual FEDERATION_*_ENABLED switches (all false by default).
compose_profiles=(--profile nodes)
if [ -n "${TESTAGRAM_ALGORITHM_IMAGE:-}" ] && [ -n "${ALGORITHM_INTERNAL_TOKEN:-}" ] && [ "${#ALGORITHM_INTERNAL_TOKEN}" -ge 32 ]; then
  compose_profiles+=(--profile ranking)
fi
if [ -n "${TESTAGRAM_FEDERATION_IMAGE:-}" ] && [ -n "${FEDERATION_DATABASE_URL:-}" ] \
  && [ -n "${FEDERATION_BACKEND_INTERNAL_TOKEN:-}" ] && [ "${#FEDERATION_BACKEND_INTERNAL_TOKEN}" -ge 32 ] \
  && [ -n "${FEDERATION_INGRESS_TOKEN:-}" ] && [ "${#FEDERATION_INGRESS_TOKEN}" -ge 32 ]; then
  compose_profiles+=(--profile federation)
fi
docker compose --env-file "$ENV_FILE" "${compose_profiles[@]}" -f "$ROOT/vps/docker-compose.yml" config -q
docker compose --env-file "$ENV_FILE" "${compose_profiles[@]}" -f "$ROOT/vps/docker-compose.yml" up -d --wait

if command -v go >/dev/null 2>&1; then
  (cd "$ROOT" && go build -trimpath -ldflags="-s -w" -o "$ROOT/bin/node-agent" ./node-agent)
else
  docker run --rm -v "$ROOT:/src" -w /src golang:1.27.2-alpine sh -c 'go build -trimpath -ldflags="-s -w" -o /src/bin/node-agent ./node-agent'
fi

cat > /etc/testagram/node-agent.env <<EOF
VPS_CONTROL_PLANE_URL=http://127.0.0.1:8787
VPS_NODE_BOOTSTRAP_TOKEN=$VPS_NODE_BOOTSTRAP_TOKEN
VPS_ALLOWED_WORKLOAD_IMAGES=${VPS_ALLOWED_WORKLOAD_IMAGES:-}
VPS_NODE_NAME=$(hostname)-node
VPS_NODE_ENDPOINT=outbound-only
VPS_VOLUME_ROOT=$STORAGE_ROOT/nodes
VPS_NODE_STATE_FILE=$STORAGE_ROOT/nodes/node-state.json
VPS_ENABLE_WATCHDOG_ACTIVATION=${VPS_ENABLE_WATCHDOG_ACTIVATION:-false}
VPS_WATCHDOG_DATA_CENTRE_COMPOSE_FILE=${VPS_WATCHDOG_DATA_CENTRE_COMPOSE_FILE:-$ROOT/vps/docker-compose.yml}
VPS_WATCHDOG_CDN_COMPOSE_FILE=${VPS_WATCHDOG_CDN_COMPOSE_FILE:-}
EOF
chmod 0600 /etc/testagram/node-agent.env
install -m 0644 "$ROOT/vps/node-agent.service" /etc/systemd/system/testagram-node-agent.service
systemctl daemon-reload
systemctl enable --now testagram-node-agent.service

docker compose --env-file "$ENV_FILE" -f "$ROOT/vps/docker-compose.yml" ps
systemctl --no-pager --full status testagram-node-agent.service
