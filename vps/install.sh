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
set -a
. "$ENV_FILE"
set +a
export TESTAGRAM_STORAGE_ROOT SUPABASE_DATA_ROOT
"$ROOT/supabase/prepare.sh"
cd "$ROOT/supabase/project"
docker compose pull
docker compose up -d --wait
cd "$ROOT"
docker compose --env-file "$ENV_FILE" -f vps/docker-compose.yml up -d --build --wait
cd "$ROOT/supabase/project"
for f in "$ROOT"/migrations/*.sql; do docker compose exec -T db psql -U postgres -d postgres -v ON_ERROR_STOP=1 < "$f"; done
cd "$ROOT"
mkdir -p "$ROOT/bin" /etc/testagram
if command -v go >/dev/null 2>&1; then
  go build -trimpath -ldflags="-s -w" -o "$ROOT/bin/node-agent" ./node-agent
else
  docker run --rm -v "$ROOT:/src" -w /src golang:1.23-alpine sh -c "go build -trimpath -ldflags=\"-s -w\" -o /src/bin/node-agent ./node-agent"
fi
cat > /etc/testagram/node-agent.env <<EOF
VPS_CONTROL_PLANE_URL=http://127.0.0.1:8787
VPS_NODE_BOOTSTRAP_TOKEN=$VPS_NODE_BOOTSTRAP_TOKEN
VPS_NODE_NAME=$(hostname)-node
VPS_NODE_ENDPOINT=outbound-only
VPS_VOLUME_ROOT=$STORAGE_ROOT/nodes
VPS_NODE_STATE_FILE=$STORAGE_ROOT/nodes/node-state.json
EOF
chmod 600 /etc/testagram/node-agent.env
install -m 0644 "$ROOT/vps/node-agent.service" /etc/systemd/system/testagram-node-agent.service
systemctl daemon-reload
systemctl enable --now testagram-node-agent.service
docker compose --env-file "$ENV_FILE" -f vps/docker-compose.yml ps
cd "$ROOT/supabase/project"
docker compose ps
