#!/usr/bin/env bash
set -euo pipefail
umask 077

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${TESTAGRAM_FRESH_ENV_FILE:-$REPO_ROOT/vps/fresh.env}"
STORAGE_ROOT="${TESTAGRAM_STORAGE_ROOT:-}"
BACKUP_ROOT="${TESTAGRAM_BACKUP_ROOT:-/var/backups/testagram}"
BACKUP_REMOTE="${TESTAGRAM_BACKUP_REMOTE:-}"
AGE_RECIPIENT="${TESTAGRAM_BACKUP_AGE_RECIPIENT:-}"

for bin in pg_dump age rsync sha256sum tar flock; do
  command -v "$bin" >/dev/null 2>&1 || { echo "$bin is required" >&2; exit 1; }
done
test -f "$ENV_FILE" || { echo "Fresh environment file not found: $ENV_FILE" >&2; exit 1; }
set -a
. "$ENV_FILE"
set +a
DATABASE_URL="${DATA_CENTRE_DATABASE_URL:-}"
test -n "$DATABASE_URL" || { echo "DATA_CENTRE_DATABASE_URL is required" >&2; exit 1; }
test -n "$BACKUP_REMOTE" || { echo "TESTAGRAM_BACKUP_REMOTE must be user@host:/absolute/path" >&2; exit 1; }
test -n "$AGE_RECIPIENT" || { echo "TESTAGRAM_BACKUP_AGE_RECIPIENT must be an age public recipient" >&2; exit 1; }
case "$BACKUP_REMOTE" in *:/*) ;; *) echo "Remote must use user@host:/absolute/path syntax" >&2; exit 1 ;; esac
if [ -n "$STORAGE_ROOT" ]; then
  test -d "$STORAGE_ROOT" || { echo "Optional node storage root not found: $STORAGE_ROOT" >&2; exit 1; }
fi

mkdir -p "$BACKUP_ROOT"
chmod 0700 "$BACKUP_ROOT"
exec 9>"$BACKUP_ROOT/.backup.lock"
flock -n 9 || { echo "Another Testagram backup is already running" >&2; exit 1; }

if [ -n "$STORAGE_ROOT" ]; then
  STORAGE_REAL="$(realpath "$STORAGE_ROOT")"
  BACKUP_REAL="$(realpath "$BACKUP_ROOT")"
  case "$BACKUP_REAL/" in "$STORAGE_REAL/"*) echo "Backup root must not be inside node storage" >&2; exit 1 ;; esac
fi

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
STAGE="$(mktemp -d "$BACKUP_ROOT/.stage-$STAMP.XXXXXX")"
ARCHIVE="$BACKUP_ROOT/testagram-datacentre-$STAMP.tar.gz"
ENCRYPTED="$ARCHIVE.age"
cleanup() {
  rm -rf "$STAGE"
  rm -f "$ARCHIVE" "$ENCRYPTED.partial"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# pg_dump takes a consistent MVCC snapshot; application writers do not need to
# be stopped for the infrastructure database backup.
pg_dump --dbname "$DATABASE_URL" --format=custom --no-owner --no-acl > "$STAGE/data-centre.dump"
test -s "$STAGE/data-centre.dump"

if [ -n "$STORAGE_ROOT" ]; then
  tar --one-file-system -C "$(dirname "$STORAGE_REAL")" -czf "$STAGE/node-storage.tar.gz" "$(basename "$STORAGE_REAL")"
  test -s "$STAGE/node-storage.tar.gz"
fi

cat > "$STAGE/metadata.txt" <<EOF
created_at_utc=$STAMP
repository_commit=$(git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null || echo unknown)
database_owner=data-centre-infrastructure
database_format=pg_dump-custom
node_storage_included=$([ -n "$STORAGE_ROOT" ] && echo true || echo false)
EOF
if [ -f "$STAGE/node-storage.tar.gz" ]; then
  tar -C "$STAGE" -czf "$ARCHIVE" data-centre.dump node-storage.tar.gz metadata.txt
else
  tar -C "$STAGE" -czf "$ARCHIVE" data-centre.dump metadata.txt
fi
test -s "$ARCHIVE"
age -r "$AGE_RECIPIENT" -o "$ENCRYPTED.partial" "$ARCHIVE"
mv "$ENCRYPTED.partial" "$ENCRYPTED"
(cd "$BACKUP_ROOT" && sha256sum "$(basename "$ENCRYPTED")" > "$(basename "$ENCRYPTED").sha256")
chmod 0600 "$ENCRYPTED" "$ENCRYPTED.sha256"
rm -f "$ARCHIVE"
rsync --archive --partial --protect-args "$ENCRYPTED" "$ENCRYPTED.sha256" "$BACKUP_REMOTE/"

printf 'Encrypted Data Centre backup created and transferred: %s\n' "$ENCRYPTED"
printf 'Remote destination: %s\n' "$BACKUP_REMOTE"
printf 'IMPORTANT: Run vps/restore-drill.sh against this artifact before relying on it.\n'
