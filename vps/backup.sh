#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT="${TESTAGRAM_ROOT:-/opt/testagram}"
ENV_FILE="${TESTAGRAM_FRESH_ENV_FILE:-$ROOT/vps/fresh.env}"
BACKUP_ROOT="${TESTAGRAM_BACKUP_ROOT:-/var/backups/testagram}"
BACKUP_REMOTE="${TESTAGRAM_BACKUP_REMOTE:-}"
AGE_RECIPIENT="${TESTAGRAM_BACKUP_AGE_RECIPIENT:-}"
POSTGRES_IMAGE="${POSTGRES_CLIENT_IMAGE:-postgres:17-alpine}"

for bin in docker age rsync sha256sum tar flock realpath; do
  command -v "$bin" >/dev/null 2>&1 || { echo "$bin is required" >&2; exit 1; }
done
test -f "$ENV_FILE" || { echo "Fresh environment file not found: $ENV_FILE" >&2; exit 1; }
set -a
. "$ENV_FILE"
set +a
POSTGRES_IMAGE="${POSTGRES_CLIENT_IMAGE:-postgres:17-alpine}"
test -n "${DATA_CENTRE_DATABASE_URL:-}" || { echo "DATA_CENTRE_DATABASE_URL is required" >&2; exit 1; }
test -n "${NEON_DATABASE_URL:-}" || { echo "NEON_DATABASE_URL is required" >&2; exit 1; }
test -n "$BACKUP_REMOTE" || { echo "TESTAGRAM_BACKUP_REMOTE must be user@host:/absolute/path" >&2; exit 1; }
test -n "$AGE_RECIPIENT" || { echo "TESTAGRAM_BACKUP_AGE_RECIPIENT must be an age public recipient" >&2; exit 1; }
case "$BACKUP_REMOTE" in *:/*) ;; *) echo "Remote must use user@host:/absolute/path syntax" >&2; exit 1 ;; esac

mkdir -p "$BACKUP_ROOT"
chmod 0700 "$BACKUP_ROOT"
exec 9>"$BACKUP_ROOT/.backup.lock"
flock -n 9 || { echo "Another Testagram backup is already running" >&2; exit 1; }
BACKUP_REAL="$(realpath "$BACKUP_ROOT")"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
STAGE="$(mktemp -d "$BACKUP_ROOT/.stage-$STAMP.XXXXXX")"
ARCHIVE="$BACKUP_ROOT/testagram-$STAMP.tar.gz"
ENCRYPTED="$ARCHIVE.age"
cleanup() { rm -rf "$STAGE"; rm -f "$ARCHIVE" "$ENCRYPTED.partial"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Each pg_dump is transactionally consistent for its own database. These
# independent dumps are not a cross-database atomic snapshot.
docker run --rm --network host -e "DATABASE_URL=$DATA_CENTRE_DATABASE_URL" "$POSTGRES_IMAGE" \
  sh -ec 'pg_dump "$DATABASE_URL" --format=custom --no-owner --no-acl' > "$STAGE/data-centre.dump"
docker run --rm --network host -e "DATABASE_URL=$NEON_DATABASE_URL" "$POSTGRES_IMAGE" \
  sh -ec 'pg_dump "$DATABASE_URL" --format=custom --no-owner --no-acl' > "$STAGE/neon-social.dump"
test -s "$STAGE/data-centre.dump"
test -s "$STAGE/neon-social.dump"

if [ -d /var/lib/testagram/storage/nodes ]; then
  tar -C /var/lib/testagram/storage -czf "$STAGE/node-state.tar.gz" nodes
fi
cat > "$STAGE/metadata.txt" <<EOF
created_at_utc=$STAMP
repository_commit=$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)
data_centre_database=pg_dump-custom
neon_social_database=pg_dump-custom
media_bytes=retained_in_private_B2_and_R2; not duplicated in this database archive
appwrite_identity_and_documents=not_exported_by_this_script
EOF
tar -C "$STAGE" -czf "$ARCHIVE" .
test -s "$ARCHIVE"
age -r "$AGE_RECIPIENT" -o "$ENCRYPTED.partial" "$ARCHIVE"
mv "$ENCRYPTED.partial" "$ENCRYPTED"
(cd "$BACKUP_ROOT" && sha256sum "$(basename "$ENCRYPTED")" > "$(basename "$ENCRYPTED").sha256")
chmod 0600 "$ENCRYPTED" "$ENCRYPTED.sha256"
rsync --archive --partial --protect-args "$ENCRYPTED" "$ENCRYPTED.sha256" "$BACKUP_REMOTE/"
printf 'Encrypted database backup created and transferred: %s\n' "$ENCRYPTED"
printf 'Remote destination: %s\n' "$BACKUP_REMOTE"
printf 'Note: Appwrite documents and B2/R2 media bytes are outside this database archive.\n'
