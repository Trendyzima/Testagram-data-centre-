#!/usr/bin/env bash
set -euo pipefail
umask 077

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${TESTAGRAM_FRESH_ENV_FILE:-${TESTAGRAM_ROOT:-/opt/testagram}/vps/fresh.env}"
test -f "$ENV_FILE" || { echo "Fresh runtime env file not found: $ENV_FILE" >&2; exit 1; }
read_env_value() {
  local key="$1" value
  value="$(sed -n "s/^${key}=//p" "$ENV_FILE" | head -n 1)"
  value="${value%$'\\r'}"
  if [[ "$value" == \"*\" ]]; then value="${value:1:${#value}-2}"; fi
  printf '%s' "$value"
}
STORAGE_ROOT="${TESTAGRAM_STORAGE_ROOT:-$(read_env_value TESTAGRAM_STORAGE_ROOT)}"
STORAGE_ROOT="${STORAGE_ROOT:-/var/lib/testagram/storage}"
BACKUP_ROOT="${TESTAGRAM_BACKUP_ROOT:-/var/backups/testagram}"
BACKUP_REMOTE="${TESTAGRAM_BACKUP_REMOTE:-$(read_env_value TESTAGRAM_BACKUP_REMOTE)}"
AGE_RECIPIENT="${TESTAGRAM_BACKUP_AGE_RECIPIENT:-$(read_env_value TESTAGRAM_BACKUP_AGE_RECIPIENT)}"

for bin in pg_dump age rsync sha256sum tar flock sed; do
  command -v "$bin" >/dev/null 2>&1 || { echo "$bin is required" >&2; exit 1; }
done
test -f "$ENV_FILE" || { echo "Fresh runtime env file not found: $ENV_FILE" >&2; exit 1; }
DATABASE_URL="$(read_env_value DATA_CENTRE_DATABASE_URL)"
test -n "$DATABASE_URL" || { echo "DATA_CENTRE_DATABASE_URL is missing from $ENV_FILE" >&2; exit 1; }
case "$DATABASE_URL" in
  *sslmode=require*|*sslmode=verify-ca*|*sslmode=verify-full*) ;;
  *) echo "Data Centre backup requires a TLS PostgreSQL URL (sslmode=require or stronger)" >&2; exit 1 ;;
esac
test -n "$BACKUP_REMOTE" || { echo "TESTAGRAM_BACKUP_REMOTE must be user@host:/absolute/path" >&2; exit 1; }
test -n "$AGE_RECIPIENT" || { echo "TESTAGRAM_BACKUP_AGE_RECIPIENT must be an age public recipient" >&2; exit 1; }
case "$BACKUP_REMOTE" in *:/*) ;; *) echo "Remote must use user@host:/absolute/path syntax" >&2; exit 1 ;; esac

mkdir -p "$BACKUP_ROOT"
chmod 0700 "$BACKUP_ROOT"
exec 9>"$BACKUP_ROOT/.backup.lock"
flock -n 9 || { echo "Another Testagram backup is already running" >&2; exit 1; }

BACKUP_REAL="$(realpath "$BACKUP_ROOT")"
if [ -d "$STORAGE_ROOT" ]; then
  STORAGE_REAL="$(realpath "$STORAGE_ROOT")"
  case "$BACKUP_REAL/" in "$STORAGE_REAL/"*) echo "Backup root must not be inside the storage root" >&2; exit 1 ;; esac
fi

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
STAGE="$(mktemp -d "$BACKUP_ROOT/.stage-$STAMP.XXXXXX")"
ARCHIVE="$BACKUP_ROOT/testagram-data-centre-$STAMP.tar.gz"
ENCRYPTED="$ARCHIVE.age"
cleanup() { rm -rf "$STAGE"; rm -f "$ARCHIVE" "$ENCRYPTED.partial"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# pg_dump uses a consistent MVCC snapshot; application services do not need to
# be stopped. This database is Data Centre infrastructure state only.
pg_dump --dbname="$DATABASE_URL" --format=custom --no-owner --no-acl --file="$STAGE/database.dump"
test -s "$STAGE/database.dump"

storage_archived=false
if [ -d "$STORAGE_ROOT" ]; then
  # Coordinate a quiet period or point TESTAGRAM_STORAGE_ROOT at a filesystem
  # snapshot when node writers may be active.
  STORAGE_REAL="$(realpath "$STORAGE_ROOT")"
  tar -C "$(dirname "$STORAGE_REAL")" -czf "$STAGE/storage.tar.gz" "$(basename "$STORAGE_REAL")"
  storage_archived=true
fi
cat > "$STAGE/metadata.txt" <<EOF
created_at_utc=$STAMP
repository_commit=$(git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null || echo unknown)
database_format=pg_dump-custom
database_scope=data-centre-infrastructure-only
storage_root=${STORAGE_ROOT}
storage_archived=$storage_archived
EOF
if [ "$storage_archived" = true ]; then
  tar -C "$STAGE" -czf "$ARCHIVE" database.dump storage.tar.gz metadata.txt
else
  tar -C "$STAGE" -czf "$ARCHIVE" database.dump metadata.txt
fi
test -s "$ARCHIVE"
age -r "$AGE_RECIPIENT" -o "$ENCRYPTED.partial" "$ARCHIVE"
mv "$ENCRYPTED.partial" "$ENCRYPTED"
(cd "$BACKUP_ROOT" && sha256sum "$(basename "$ENCRYPTED")" > "$(basename "$ENCRYPTED").sha256")
chmod 0600 "$ENCRYPTED" "$ENCRYPTED.sha256"
rsync --archive --partial --protect-args "$ENCRYPTED" "$ENCRYPTED.sha256" "$BACKUP_REMOTE/"
rm -f "$ARCHIVE"
printf 'Encrypted Data Centre backup created and transferred: %s\n' "$ENCRYPTED"
printf 'Remote destination: %s\n' "$BACKUP_REMOTE"
printf 'Scope: Data Centre infrastructure PostgreSQL; storage archived=%s\n' "$storage_archived"
printf 'IMPORTANT: Run vps/restore-drill.sh against this artifact before relying on it.\n'
