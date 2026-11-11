#!/usr/bin/env bash
# Offline byte-level restore drill. It never writes to live B2, R2 or Appwrite.
set -euo pipefail
umask 077

for bin in age sha256sum tar jq stat find mktemp realpath grep awk wc tr; do
  command -v "$bin" >/dev/null 2>&1 || { echo "$bin is required" >&2; exit 1; }
done

BACKUP_FILE="${1:-}"
IDENTITY_FILE="${TESTAGRAM_BACKUP_AGE_IDENTITY_FILE:-}"
[[ -n "$BACKUP_FILE" && -f "$BACKUP_FILE" ]] || {
  echo "Usage: TESTAGRAM_BACKUP_AGE_IDENTITY_FILE=/secure/age.key $0 /path/to/testagram-media.tar.gz.age" >&2
  exit 2
}
[[ -n "$IDENTITY_FILE" && -f "$IDENTITY_FILE" ]] || {
  echo "TESTAGRAM_BACKUP_AGE_IDENTITY_FILE must point to the age private identity" >&2
  exit 1
}
BACKUP_FILE="$(realpath "$BACKUP_FILE")"
CHECKSUM_FILE="$BACKUP_FILE.sha256"
[[ -f "$CHECKSUM_FILE" ]] || { echo "Encrypted backup checksum sidecar missing" >&2; exit 1; }
(cd "$(dirname "$BACKUP_FILE")" && sha256sum -c "$(basename "$CHECKSUM_FILE")")

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT INT TERM
age -d -i "$IDENTITY_FILE" -o "$STAGE/backup.tar.gz" "$BACKUP_FILE"
tar -tzf "$STAGE/backup.tar.gz" > "$STAGE/archive.list"
# Reject absolute paths and parent traversal before extracting an untrusted archive.
if grep -Eq '(^/|(^|/)\.\.(/|$))' "$STAGE/archive.list"; then
  echo "Backup archive contains an unsafe path" >&2
  exit 1
fi
tar --no-same-owner --no-same-permissions -xzf "$STAGE/backup.tar.gz" -C "$STAGE"

[[ -s "$STAGE/backup-metadata.txt" ]] || { echo "Backup metadata missing" >&2; exit 1; }
[[ -s "$STAGE/object-manifest.ndjson" ]] || { echo "Object inventory missing" >&2; exit 1; }
[[ -s "$STAGE/appwrite-manifest.sha256" ]] || { echo "Appwrite export manifest missing" >&2; exit 1; }
[[ -d "$STAGE/appwrite-export" ]] || { echo "Appwrite export payload missing" >&2; exit 1; }
(cd "$STAGE" && sha256sum -c appwrite-manifest.sha256)

b2_count=0
r2_count=0
while IFS= read -r row; do
  [[ -n "$row" ]] || continue
  store="$(jq -r '.store' <<<"$row")"
  file="$(jq -r '.file' <<<"$row")"
  key="$(jq -r '.object_key' <<<"$row")"
  size="$(jq -r '.size_bytes' <<<"$row")"
  expected="$(jq -r '.sha256' <<<"$row")"
  case "$store:$file" in
    b2:objects/b2/*) b2_count=$((b2_count + 1)) ;;
    r2:objects/r2/*) r2_count=$((r2_count + 1)) ;;
    *) echo "Invalid object manifest path/store mapping" >&2; exit 1 ;;
  esac
  [[ -n "$key" && "$key" != /* && "$key" != *".."* ]] || {
    echo "Invalid object key in backup manifest" >&2
    exit 1
  }
  [[ -f "$STAGE/$file" ]] || { echo "Object bytes missing for inventory entry $key" >&2; exit 1; }
  actual_size="$(stat -c '%s' "$STAGE/$file")"
  actual_sha="$(sha256sum "$STAGE/$file" | awk '{print $1}')"
  [[ "$actual_size" == "$size" && "$actual_sha" == "$expected" ]] || {
    echo "Object integrity mismatch for inventory entry $key" >&2
    exit 1
  }
done < "$STAGE/object-manifest.ndjson"

appwrite_files="$(find "$STAGE/appwrite-export" -type f | wc -l | tr -d ' ')"
[[ "$appwrite_files" -gt 0 ]] || { echo "Appwrite export has no files" >&2; exit 1; }

echo "BYTE-RESTORE-DRILL-PASSED"
echo "B2 object bytes verified: $b2_count"
echo "R2 object bytes verified: $r2_count"
echo "Appwrite export files verified: $appwrite_files"
echo "IMPORTANT: This is an offline byte-integrity drill, not an Appwrite import or live provider restore."
echo "A separate isolated Appwrite project import and B2/R2 staging-bucket restore/playback drill are still required."
