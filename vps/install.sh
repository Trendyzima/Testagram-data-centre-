#!/usr/bin/env bash
set -euo pipefail

ROOT="${TESTAGRAM_ROOT:-/opt/testagram}"
BRANCH="${TESTAGRAM_REPO_BRANCH:-main}"
REPO_URL="${TESTAGRAM_REPO_URL:-https://github.com/Trendyzima/Testagram-data-centre-.git}"
ENV_FILE="${TESTAGRAM_FRESH_ENV_FILE:-$ROOT/vps/fresh.env}"
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
  echo "Created $ENV_FILE. Fill in Appwrite, Neon, B2/R2, image digests and node tokens, then rerun." >&2
  exit 2
fi

set -a
. "$ENV_FILE"
set +a

for key in APPWRITE_PROJECT_ID APPWRITE_DATABASE_ID APPWRITE_SERVER_API_KEY NEON_DATABASE_URL DATA_CENTRE_DATABASE_URL FEDERATION_DATABASE_URL FRESH_FEDERATION_INTERNAL_TOKEN ALGORITHM_INTERNAL_TOKEN VPS_INTERNAL_TOKEN VPS_NODE_BOOTSTRAP_TOKEN; do
  value="${!key:-}"
  if [ -z "$value" ] || [[ "$value" == *REPLACE* ]]; then
    echo "Required fresh deployment setting is missing or still a placeholder: $key" >&2
    exit 2
  fi
done
if [ "${#FRESH_FEDERATION_INTERNAL_TOKEN}" -lt 32 ] || [ "${#ALGORITHM_INTERNAL_TOKEN}" -lt 32 ] || [ "${#VPS_INTERNAL_TOKEN}" -lt 32 ] || [ "${#VPS_NODE_BOOTSTRAP_TOKEN}" -lt 32 ]; then
  echo "Federation, Algorithm, control-plane and node bootstrap tokens must contain at least 32 characters." >&2
  exit 2
fi
if [[ "$VPS_NODE_BOOTSTRAP_TOKEN" == *$'\n'* || "$VPS_NODE_BOOTSTRAP_TOKEN" == *$'\r'* ]]; then
  echo "Node bootstrap token must be a single line." >&2
  exit 2
fi
if grep -Eq 'REPLACE_WITH|REPLACE_PASSWORD|REPLACE_WITH_64_HEX_DIGEST|ops@example.com' "$ENV_FILE"; then
  echo "Fresh env still contains placeholders. Set real secrets and immutable image digests before deployment." >&2
  exit 2
fi

mkdir -p "$STORAGE_ROOT/nodes" "$ROOT/bin" /etc/testagram
chmod 0750 "$STORAGE_ROOT" "$STORAGE_ROOT/nodes"
docker compose --env-file "$ENV_FILE" -f "$ROOT/vps/docker-compose.yml" config -q
docker compose --env-file "$ENV_FILE" -f "$ROOT/vps/docker-compose.yml" up -d --wait

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
EOF
chmod 0600 /etc/testagram/node-agent.env
install -m 0644 "$ROOT/vps/node-agent.service" /etc/systemd/system/testagram-node-agent.service
systemctl daemon-reload
systemctl enable --now testagram-node-agent.service

docker compose --env-file "$ENV_FILE" -f "$ROOT/vps/docker-compose.yml" ps
systemctl --no-pager --full status testagram-node-agent.service
