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
  echo "Fill in real Appwrite, Neon, B2, R2, image-digest and node secrets, then rerun."
  exit 2
fi

# Never silently deploy placeholder secrets or an unpinned image.
if grep -Eq 'REPLACE_WITH|REPLACE_PASSWORD|REPLACE_WITH_64_HEX_DIGEST|ops@example.com' "$ENV_FILE"; then
  echo "Fresh env still contains placeholders. Set real secrets and immutable image digests before deployment." >&2
  exit 2
fi

docker compose --env-file "$ENV_FILE" -f "$ROOT/vps/docker-compose.fresh.yml" config -q
docker compose --env-file "$ENV_FILE" -f "$ROOT/vps/docker-compose.fresh.yml" up -d
docker compose --env-file "$ENV_FILE" -f "$ROOT/vps/docker-compose.fresh.yml" ps
