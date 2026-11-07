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
test -n "$RESTORE_IMAGE" && [[ "$RESTORE_IMAGE" == *@sha256:* ]] || { echo "TESTAGRAM_RESTORE_POSTGRES_IMAGE must be an approved immutable PostgreSQL image@sha256:digest" >&2; exit 1; }
BACKUP_FILE="$(realpath "$BACKUP_FILE")"
BACKUP_DIR="$(dirname "$BACKUP_FILE")"
CHECKSUM_FILE="$BACKUP_FILE.sha256"
test -f "$CHECKSUM_FILE" || { echo "Checksum sidecar missing: $CHECKSUM_FILE" >&2; exit 1; }
(cd "$BACKUP_DIR" && sha256sum -c "$(basename "$CHECKSUM_FILE")")

STAGE="$(mktemp -d)"
CONTAINER="testagram-restore-drill-$(date -u +%s)-$$"
PASS="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  rm -rf "$STAGE"
}
trap cleanup EXIT INT TERM

age -d -i "$IDENTITY_FILE" -o "$STAGE/backup.tar.gz" "$BACKUP_FILE"
tar -tzf "$STAGE/backup.tar.gz" >/dev/null
tar -xzf "$STAGE/backup.tar.gz" -C "$STAGE"
test -s "$STAGE/database.dump"
if [ -s "$STAGE/storage.tar.gz" ]; then
  tar -tzf "$STAGE/storage.tar.gz" >/dev/null
  mkdir "$STAGE/storage-restored"
  tar -xzf "$STAGE/storage.tar.gz" -C "$STAGE/storage-restored"
  find "$STAGE/storage-restored" -name .testagram-storage-root -print -quit | grep -q . || {
    echo "Restored storage archive is missing the Testagram storage marker" >&2
    exit 1
  }
else
  echo "No local node-storage archive was included; provider objects require their own B2/R2 restore drill."
fi

# The container is deliberately network-isolated and never publishes a port.
# Use the exact approved PostgreSQL image digest from the deployment.
docker run -d --name "$CONTAINER" --network none -e "POSTGRES_PASSWORD=$PASS" "$RESTORE_IMAGE" >/dev/null
ready=0
for _ in $(seq 1 60); do
  if docker exec "$CONTAINER" pg_isready -U postgres -d postgres >/dev/null 2>&1; then ready=1; break; fi
  sleep 2
done
test "$ready" -eq 1 || { echo "Isolated restore database did not become ready" >&2; exit 1; }
docker cp "$STAGE/database.dump" "$CONTAINER:/tmp/database.dump" >/dev/null
docker exec "$CONTAINER" pg_restore -U postgres -d postgres --no-owner --no-acl --exit-on-error /tmp/database.dump
EXPECTED="$(docker exec "$CONTAINER" psql -U postgres -d postgres -Atqc "SELECT (to_regclass('public.vps_nodes') IS NOT NULL AND to_regclass('public.vps_workloads') IS NOT NULL AND to_regclass('testagram_hosting.backup_manifests') IS NOT NULL)::text")"
test "$EXPECTED" = "true" || { echo "Restored database is missing expected Data Centre schemas/tables" >&2; exit 1; }

echo "RESTORE DRILL PASSED"
echo "Database: pg_restore completed and expected Data Centre tables are present."
if [ -s "$STAGE/storage.tar.gz" ]; then echo "Storage: local node-storage archive integrity and marker passed."; else echo "Storage: local node storage not included in this drill."; fi
echo "Isolation: temporary PostgreSQL container had no network and no published ports."
echo "This validates archive readability, not full application login/upload/playback or a measured production RTO."
