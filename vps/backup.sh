#!/usr/bin/env bash
set -euo pipefail
umask 077

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SUPABASE_ROOT="${SUPABASE_DATA_ROOT:-/var/lib/testagram/supabase}"
PROJECT="${SUPABASE_PROJECT_ROOT:-$SUPABASE_ROOT/project}"
ENV_FILE="${TESTAGRAM_SUPABASE_ENV_FILE:-$PROJECT/.env}"
STORAGE_ROOT="${TESTAGRAM_STORAGE_ROOT:-/var/lib/testagram/storage}"
BACKUP_ROOT="${TESTAGRAM_BACKUP_ROOT:-/var/backups/testagram}"
BACKUP_REMOTE="${TESTAGRAM_BACKUP_REMOTE:-}"
AGE_RECIPIENT="${TESTAGRAM_BACKUP_AGE_RECIPIENT:-}"

for bin in docker age rsync sha256sum tar flock; do
  command -v "$bin" >/dev/null 2>&1 || { echo "$bin is required" >&2; exit 1; }
done
test -f "$ENV_FILE" || { echo "Supabase env file not found: $ENV_FILE" >&2; exit 1; }
test -d "$STORAGE_ROOT" || { echo "Storage root not found: $STORAGE_ROOT" >&2; exit 1; }
test -n "$BACKUP_REMOTE" || { echo "TESTAGRAM_BACKUP_REMOTE must be user@host:/absolute/path" >&2; exit 1; }
test -n "$AGE_RECIPIENT" || { echo "TESTAGRAM_BACKUP_AGE_RECIPIENT must be an age public recipient" >&2; exit 1; }
case "$BACKUP_REMOTE" in *:/*) ;; *) echo "Remote must use user@host:/absolute/path syntax" >&2; exit 1 ;; esac

mkdir -p "$BACKUP_ROOT"
chmod 0700 "$BACKUP_ROOT"
exec 9>"$BACKUP_ROOT/.backup.lock"
flock -n 9 || { echo "Another Testagram backup is already running" >&2; exit 1; }

STORAGE_REAL="$(realpath "$STORAGE_ROOT")"
BACKUP_REAL="$(realpath "$BACKUP_ROOT")"
case "$BACKUP_REAL/" in "$STORAGE_REAL/"*) echo "Backup root must not be inside the storage root" >&2; exit 1 ;; esac

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
STAGE="$(mktemp -d "$BACKUP_ROOT/.stage-$STAMP.XXXXXX")"
ARCHIVE="$BACKUP_ROOT/testagram-$STAMP.tar.gz"
ENCRYPTED="$ARCHIVE.age"
SUPABASE_STOPPED=0
BACKEND_STOPPED=0
PROJECT_COMPOSE=(docker compose --env-file "$ENV_FILE" -f "$PROJECT/docker-compose.yml" -f "$PROJECT/docker-compose.testagram.yml")
BACKEND_ENV_FILE="${TESTAGRAM_ENV_FILE:-$REPO_ROOT/.env}"
BACKEND_COMPOSE=(docker compose --env-file "$BACKEND_ENV_FILE" -f "$REPO_ROOT/vps/docker-compose.yml")

restart_services() {
  if [ "$BACKEND_STOPPED" -eq 1 ]; then
    "${BACKEND_COMPOSE[@]}" up -d || echo "WARNING: backend services did not all restart; inspect docker compose ps" >&2
    BACKEND_STOPPED=0
  fi
  if [ "$SUPABASE_STOPPED" -eq 1 ]; then
    mapfile -t services < <("${PROJECT_COMPOSE[@]}" config --services | grep -vx 'db' || true)
    if [ "${#services[@]}" -gt 0 ]; then "${PROJECT_COMPOSE[@]}" up -d "${services[@]}" || echo "WARNING: Supabase services did not all restart; inspect docker compose ps" >&2; fi
    SUPABASE_STOPPED=0
  fi
}
cleanup() {
  restart_services
  rm -rf "$STAGE"
}
trap cleanup EXIT INT TERM

# Quiesce every application writer while taking the DB dump and storage
# archive. This is a deliberate maintenance window to avoid a split-brain
# backup where DB metadata and object files represent different moments.
mapfile -t supabase_services < <("${PROJECT_COMPOSE[@]}" config --services | grep -vx 'db' || true)
if [ "${#supabase_services[@]}" -gt 0 ]; then
  SUPABASE_STOPPED=1
  "${PROJECT_COMPOSE[@]}" stop "${supabase_services[@]}"
fi
BACKEND_STOPPED=1
"${BACKEND_COMPOSE[@]}" stop

"${PROJECT_COMPOSE[@]}" exec -T db pg_dump -U postgres -d postgres --format=custom --no-owner --no-acl > "$STAGE/database.dump"
test -s "$STAGE/database.dump"
tar -C "$(dirname "$STORAGE_REAL")" -czf "$STAGE/storage.tar.gz" "$(basename "$STORAGE_REAL")"
cat > "$STAGE/metadata.txt" <<EOF
created_at_utc=$STAMP
repository_commit=$(git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null || echo unknown)
database_format=pg_dump-custom
storage_root=$STORAGE_REAL
EOF
tar -C "$STAGE" -czf "$ARCHIVE" database.dump storage.tar.gz metadata.txt
test -s "$ARCHIVE"
age -r "$AGE_RECIPIENT" -o "$ENCRYPTED.partial" "$ARCHIVE"
mv "$ENCRYPTED.partial" "$ENCRYPTED"
(cd "$BACKUP_ROOT" && sha256sum "$(basename "$ENCRYPTED")" > "$(basename "$ENCRYPTED").sha256")
chmod 0600 "$ENCRYPTED" "$ENCRYPTED.sha256"

# Restore service availability before the network transfer.
restart_services
rm -f "$ARCHIVE"
rsync --archive --partial --protect-args "$ENCRYPTED" "$ENCRYPTED.sha256" "$BACKUP_REMOTE/"
printf 'Encrypted backup created and transferred: %s\n' "$ENCRYPTED"
printf 'Remote destination: %s\n' "$BACKUP_REMOTE"
printf 'IMPORTANT: Run vps/restore-drill.sh against this artifact before relying on it.\n'
