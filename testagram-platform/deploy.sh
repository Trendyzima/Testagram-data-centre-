#!/usr/bin/env bash
# Explicit, bounded deployment of the Testagram application/control-plane stack.
# This does not update public DNS, run schema migrations, or delete volumes.
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="${TESTAGRAM_ENV_FILE:-$REPO_ROOT/.env}"
COMPOSE_FILE="$REPO_ROOT/vps/docker-compose.yml"
WAIT_SECONDS="${TESTAGRAM_DEPLOY_WAIT_SECONDS:-120}"
PROJECT_NAME="${COMPOSE_PROJECT_NAME:-testagram-vps}"

if [ ! -f "$ENV_FILE" ]; then
  echo "Environment file not found: $ENV_FILE" >&2
  echo "Set TESTAGRAM_ENV_FILE to the root-owned deployment env file." >&2
  exit 1
fi
if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  echo "Docker Engine and Docker Compose v2 are required." >&2
  exit 1
fi

# The preflight is read-only. It never sources or prints the env file.
TESTAGRAM_ENV_FILE="$ENV_FILE" bash "$SCRIPT_DIR/preflight.sh"
docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" config --quiet

if [ "${TESTAGRAM_DEPLOY_DRY_RUN:-0}" = "1" ]; then
  echo "Dry run complete: Compose configuration is valid; no containers changed."
  exit 0
fi

echo "Deploying Testagram services (no DNS changes, migrations, or volume deletion)..."
docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --build

deadline=$((SECONDS + WAIT_SECONDS))
while [ "$SECONDS" -lt "$deadline" ]; do
  pending=0
  for service in control-plane platform video video-worker notification worker redis edge; do
    id="$(docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" ps -q "$service" 2>/dev/null || true)"
    if [ -z "$id" ]; then
      printf 'WAIT  %s container not created yet\n' "$service"
      pending=1
      continue
    fi
    state="$(docker inspect --format '{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$id" 2>/dev/null || printf 'unknown|unknown')"
    status="${state%%|*}"
    health="${state#*|}"
    if [ "$status" != "running" ] || { [ "$health" != "none" ] && [ "$health" != "healthy" ]; }; then
      printf 'WAIT  %s state=%s health=%s\n' "$service" "$status" "$health"
      pending=1
    fi
  done
  if [ "$pending" -eq 0 ]; then
    if curl -fsS --max-time 5 http://127.0.0.1:8787/healthz >/dev/null; then
      echo "PASS  control-plane HTTP health endpoint"
      docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" ps
      echo "Testagram deployment reports all declared services running/healthy."
      echo "Note: this is not an end-to-end auth/media/federation test."
      exit 0
    fi
    echo "WAIT  control-plane HTTP health endpoint"
  fi
  sleep 3
done

echo "FAIL  deployment did not become healthy within ${WAIT_SECONDS}s" >&2
docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" ps >&2 || true
docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" logs --tail=100 >&2 || true
exit 1
