#!/usr/bin/env bash
set -euo pipefail
ROOT="${TESTAGRAM_ROOT:-/opt/testagram}"
ENV_FILE="${TESTAGRAM_ENV_FILE:-$ROOT/.env}"
REPO_URL="${TESTAGRAM_REPO_URL:-https://github.com/Trendyzima/Testagram-data-centre-.git}"
DOMAIN="${TESTAGRAM_DOMAIN:-localhost}"
SUPABASE_URL="${SUPABASE_PUBLIC_URL:-https://$DOMAIN}"
command -v docker >/dev/null || { echo "Docker Engine is required"; exit 1; }
docker compose version >/dev/null || { echo "Docker Compose v2 is required"; exit 1; }
command -v git >/dev/null || { echo "git is required"; exit 1; }
command -v openssl >/dev/null || { echo "openssl is required"; exit 1; }
mkdir -p "$ROOT"
if [ ! -d "$ROOT/.git" ]; then git clone "$REPO_URL" "$ROOT"; else git -C "$ROOT" fetch origin main && git -C "$ROOT" reset --hard origin/main; fi
if [ ! -f "$ENV_FILE" ]; then
  jwt_secret="$(openssl rand -hex 32)"
  b64url() { printf '%s' "$1" | openssl base64 -A | tr '+/' '-_' | tr -d '='; }
  jwt() {
    local role="$1" header payload signing
    header="$(b64url '{"alg":"HS256","typ":"JWT"}')"
    payload="$(b64url "{\"role\":\"$role\",\"iss\":\"supabase\",\"iat\":0,\"exp\":4102444800}")"
    signing="$header.$payload"
    printf '%s.%s' "$signing" "$(printf '%s' "$signing" | openssl dgst -sha256 -hmac "$jwt_secret" -binary | openssl base64 -A | tr '+/' '-_' | tr -d '=')"
  }
  cat > "$ENV_FILE" <<EOF
TESTAGRAM_ROOT=$ROOT
SUPABASE_DATA_ROOT=/var/lib/testagram/supabase
TESTAGRAM_STORAGE_ROOT=/var/lib/testagram/storage
SUPABASE_NETWORK=testagram-supabase
TESTAGRAM_DOMAIN=$DOMAIN
TESTAGRAM_ORIGIN=https://testagram.site
SUPABASE_PUBLIC_URL=$SUPABASE_URL
SUPABASE_API_EXTERNAL_URL=$SUPABASE_URL/auth/v1
SITE_URL=$SUPABASE_URL
TLS_ADMIN_EMAIL=admin@localhost
POSTGRES_PASSWORD=$(openssl rand -hex 24)
JWT_SECRET=$jwt_secret
ANON_KEY=$(jwt anon)
SERVICE_ROLE_KEY=$(jwt service_role)
DASHBOARD_USERNAME=admin
DASHBOARD_PASSWORD=$(openssl rand -hex 24)
VPS_INTERNAL_TOKEN=$(openssl rand -hex 32)
VPS_NODE_BOOTSTRAP_TOKEN=$(openssl rand -hex 32)
VIDEO_JWT_SECRET=$jwt_secret
VIDEO_SIGNING_SECRET=$(openssl rand -hex 32)
NOTIFICATION_JWT_SECRET=$jwt_secret
NOTIFICATION_INTERNAL_TOKEN=$(openssl rand -hex 32)
DATABASE_URL=postgresql://postgres:${POSTGRES_PASSWORD}@db:5432/postgres
EOF
  chmod 600 "$ENV_FILE"
fi
export TESTAGRAM_ROOT="$ROOT" TESTAGRAM_ENV_FILE="$ENV_FILE" TESTAGRAM_DOMAIN="$DOMAIN" SUPABASE_PUBLIC_URL="$SUPABASE_URL"
bash "$ROOT/vps/install.sh"
echo "Testagram Docker stack is running."
docker compose --env-file "$ENV_FILE" -f "$ROOT/vps/docker-compose.yml" ps
