#!/usr/bin/env bash
set -euo pipefail
umask 077

BACKUP_FILE="${1:-}"
IDENTITY_FILE="${TESTAGRAM_BACKUP_AGE_IDENTITY_FILE:-}"
RESTORE_IMAGE="${TESTAGRAM_RESTORE_POSTGRES_IMAGE:-}"
for bin in docker age sha256sum tar; do
  command -v "$bin" >/dev/null 2>&1 || { echo "$bin is required" >&2; exit 1; }
done
test -n "$BACKUP_FILE" && test -f "$BACKUP_FILE" || { echo "Usage: TESTAGRAM_BACKUP_AGE_IDENTITY_FILE=/secure/age.key TESTAGRAM_RESTORE_POSTGRES_IMAGE='registry/image@sha256:...' $0 /path/to/backup.tar.gz.age" >&2; exit 2; }
test -n "$IDENTITY_FILE" && test -f "$IDENTITY_FILE" || { echo "TESTAGRAM_BACKUP_AGE_IDENTITY_FILE must point to the private age identity file" >&2; exit 1; }
test -n "$RESTORE_IMAGE" && [[ "$RESTORE_IMAGE" == *@sha256:* ]] || { echo "TESTAGRAM_RESTORE_POSTGRES_IMAGE must be an approved immutable image@sha256:digest" >&2; exit 1; }
BACKUP_FILE="$(realpath "$BACKUP_FILE")"
BACKUP_DIR="$(dirname "$BACKUP_FILE")"
CHECKSUM_FILE="$BACKUP_FILE.sha256"
test -f "$CHECKSUM_FILE" || { echo "Checksum sidecar missing: $CHECKSUM_FILE" >&2; exit 1; }
(cd "$BACKUP_DIR" && sha256sum -c "$(basename "$CHECKSUM_FILE")")

STAGE="$(mktemp -d)"
CONTAINER="testagram-restore-drill-$(date -u +%s)-$$"
PASS="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
cleanup() { docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; rm -rf "$STAGE"; }
trap cleanup EXIT INT TERM

age -d -i "$IDENTITY_FILE" -o "$STAGE/backup.tar.gz" "$BACKUP_FILE"
tar -tzf "$STAGE/backup.tar.gz" >/dev/null
tar -xzf "$STAGE/backup.tar.gz" -C "$STAGE"
test -s "$STAGE/data-centre.dump"
test -s "$STAGE/neon-social.dump"

# Restore into a network-isolated disposable database. No public port is bound.
docker run -d --name "$CONTAINER" --network none -e "POSTGRES_PASSWORD=$PASS" "$RESTORE_IMAGE" >/dev/null
ready=0
for _ in $(seq 1 60); do
  if docker exec "$CONTAINER" pg_isready -U postgres -d postgres >/dev/null 2>&1; then ready=1; break; fi
  sleep 2
done
test "$ready" -eq 1 || { echo "Isolated restore database did not become ready" >&2; exit 1; }
docker exec "$CONTAINER" createdb -U postgres neon_restore
docker cp "$STAGE/data-centre.dump" "$CONTAINER:/tmp/data-centre.dump" >/dev/null
docker cp "$STAGE/neon-social.dump" "$CONTAINER:/tmp/neon-social.dump" >/dev/null
docker exec "$CONTAINER" pg_restore -U postgres -d postgres --no-owner --no-acl --exit-on-error /tmp/data-centre.dump
docker exec "$CONTAINER" pg_restore -U postgres -d neon_restore --no-owner --no-acl --exit-on-error /tmp/neon-social.dump
for database in postgres neon_restore; do
  count="$(docker exec "$CONTAINER" psql -U postgres -d "$database" -Atqc "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema')")"
  test "$count" -gt 0 || { echo "Restore contains no user tables in $database" >&2; exit 1; }
done

echo "RESTORE DRILL PASSED"
echo "Both Data Centre and Neon PostgreSQL archives restored in an isolated, network-disabled container."
echo "This validates database archive integrity only; Appwrite documents and B2/R2 media need separate recovery drills."
