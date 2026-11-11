#!/usr/bin/env bash
# Downloads verified byte copies from both active object stores, packages an
# Appwrite export supplied by the approved Appwrite backup process, encrypts the
# bundle with age, and stores it in a third, independent S3-compatible archive.
set -euo pipefail
umask 077

for bin in aws age tar sha256sum find stat python3 jq mktemp base64 awk; do
  command -v "$bin" >/dev/null 2>&1 || { echo "$bin is required" >&2; exit 1; }
done

: "${B2_S3_ENDPOINT:?B2_S3_ENDPOINT is required}"
: "${B2_S3_REGION:?B2_S3_REGION is required}"
: "${B2_S3_BUCKET:?B2_S3_BUCKET is required}"
: "${B2_S3_ACCESS_KEY_ID:?B2_S3_ACCESS_KEY_ID is required}"
: "${B2_S3_SECRET_ACCESS_KEY:?B2_S3_SECRET_ACCESS_KEY is required}"
: "${CLOUDFLARE_R2_ENDPOINT:?CLOUDFLARE_R2_ENDPOINT is required}"
: "${CLOUDFLARE_R2_REGION:?CLOUDFLARE_R2_REGION is required}"
: "${CLOUDFLARE_R2_BUCKET:?CLOUDFLARE_R2_BUCKET is required}"
: "${CLOUDFLARE_R2_ACCESS_KEY_ID:?CLOUDFLARE_R2_ACCESS_KEY_ID is required}"
: "${CLOUDFLARE_R2_SECRET_ACCESS_KEY:?CLOUDFLARE_R2_SECRET_ACCESS_KEY is required}"
: "${ARCHIVE_S3_ENDPOINT:?ARCHIVE_S3_ENDPOINT must point to an independent archive provider}"
: "${ARCHIVE_S3_REGION:?ARCHIVE_S3_REGION is required}"
: "${ARCHIVE_S3_BUCKET:?ARCHIVE_S3_BUCKET is required}"
: "${ARCHIVE_S3_ACCESS_KEY_ID:?ARCHIVE_S3_ACCESS_KEY_ID is required}"
: "${ARCHIVE_S3_SECRET_ACCESS_KEY:?ARCHIVE_S3_SECRET_ACCESS_KEY is required}"
: "${TESTAGRAM_BACKUP_AGE_RECIPIENT:?age recipient is required}"
: "${APPWRITE_BACKUP_EXPORT:?APPWRITE_BACKUP_EXPORT must point to an Appwrite export/snapshot directory or file}"

APPWRITE_BACKUP_PROJECT_ID="${APPWRITE_BACKUP_PROJECT_ID:-unspecified}"
APPWRITE_BACKUP_DATABASE_ID="${APPWRITE_BACKUP_DATABASE_ID:-unspecified}"
BACKUP_ROOT="${TESTAGRAM_MEDIA_BACKUP_ROOT:-/var/backups/testagram-media}"
mkdir -p "$BACKUP_ROOT"
chmod 0700 "$BACKUP_ROOT"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
ARCHIVE_PREFIX="${TESTAGRAM_MEDIA_ARCHIVE_PREFIX:-testagram-media-backups}"
STAGE="$(mktemp -d "${BACKUP_ROOT}.stage.XXXXXX")"
trap 'rm -rf "$STAGE"' EXIT INT TERM
mkdir -p "$BACKUP_ROOT"
chmod 0700 "$BACKUP_ROOT"

endpoint_host() {
  python3 - "$1" <<'PY'
import sys
from urllib.parse import urlparse
u = urlparse(sys.argv[1])
if u.scheme != "https" or not u.hostname or u.username or u.password or u.path not in ("", "/") or u.query or u.fragment:
    raise SystemExit("endpoint must be a clean HTTPS origin")
print(u.hostname.lower())
PY
}

B2_HOST="$(endpoint_host "$B2_S3_ENDPOINT")"
R2_HOST="$(endpoint_host "$CLOUDFLARE_R2_ENDPOINT")"
ARCHIVE_HOST="$(endpoint_host "$ARCHIVE_S3_ENDPOINT")"
if [[ "$B2_HOST" == "$R2_HOST" || "$ARCHIVE_HOST" == "$B2_HOST" || "$ARCHIVE_HOST" == "$R2_HOST" ]]; then
  echo "B2, R2 and archive endpoints must be three distinct provider hosts" >&2
  exit 1
fi
if [[ "$ARCHIVE_S3_BUCKET" == "$B2_S3_BUCKET" || "$ARCHIVE_S3_BUCKET" == "$CLOUDFLARE_R2_BUCKET" ]]; then
  echo "Archive bucket must be distinct from both active media buckets" >&2
  exit 1
fi

test -e "$APPWRITE_BACKUP_EXPORT" || { echo "Appwrite export does not exist" >&2; exit 1; }
if [[ -d "$APPWRITE_BACKUP_EXPORT" ]]; then
  if find "$APPWRITE_BACKUP_EXPORT" -type l -print -quit | grep -q .; then
    echo "Appwrite export must not contain symlinks" >&2
    exit 1
  fi
  mkdir -p "$STAGE/appwrite-export"
  cp -a "$APPWRITE_BACKUP_EXPORT"/. "$STAGE/appwrite-export"/
else
  mkdir -p "$STAGE/appwrite-export"
  cp -- "$APPWRITE_BACKUP_EXPORT" "$STAGE/appwrite-export/appwrite-export.bin"
fi
if ! find "$STAGE/appwrite-export" -type f -print -quit | grep -q .; then
  echo "Appwrite export is empty" >&2
  exit 1
fi

mkdir -p "$STAGE/objects/b2" "$STAGE/objects/r2"
: > "$STAGE/object-manifest.ndjson"

backup_store() {
  local store="$1" endpoint="$2" region="$3" bucket="$4" access="$5" secret="$6"
  local index=0 token="" response encoded entry key size file sha head content_type etag last_modified metadata
  export AWS_ACCESS_KEY_ID="$access"
  export AWS_SECRET_ACCESS_KEY="$secret"
  export AWS_DEFAULT_REGION="$region"
  export AWS_EC2_METADATA_DISABLED=true
  while :; do
    local args=(s3api list-objects-v2 --bucket "$bucket" --max-keys 1000 --endpoint-url "$endpoint" --region "$region" --output json --no-cli-pager)
    if [[ -n "$token" ]]; then args+=(--continuation-token "$token"); fi
    response="$(aws "${args[@]}")"
    while IFS= read -r encoded; do
      [[ -n "$encoded" ]] || continue
      entry="$(printf '%s' "$encoded" | base64 -d)"
      key="$(jq -r '.Key' <<<"$entry")"
      size="$(jq -r '.Size' <<<"$entry")"
      index=$((index + 1))
      file="objects/$store/$(printf '%012d' "$index").blob"
      mkdir -p "$STAGE/objects/$store"
      aws s3api get-object --bucket "$bucket" --key "$key" --endpoint-url "$endpoint" --region "$region" --no-cli-pager "$STAGE/$file" >/dev/null
      actual_size="$(stat -c '%s' "$STAGE/$file")"
      [[ "$actual_size" == "$size" ]] || { echo "$store object size mismatch at inventory item $index" >&2; return 1; }
      sha="$(sha256sum "$STAGE/$file" | awk '{print $1}')"
      head="$(aws s3api head-object --bucket "$bucket" --key "$key" --endpoint-url "$endpoint" --region "$region" --output json --no-cli-pager)"
      content_type="$(jq -r '.ContentType // ""' <<<"$head")"
      etag="$(jq -r '.ETag // ""' <<<"$head")"
      last_modified="$(jq -r '.LastModified // ""' <<<"$head")"
      metadata="$(jq -c '.Metadata // {}' <<<"$head")"
      jq -cn --arg store "$store" --arg key "$key" --arg file "$file" --arg sha256 "$sha" \
        --arg content_type "$content_type" --arg etag "$etag" --arg last_modified "$last_modified" \
        --argjson size_bytes "$size" --argjson metadata "$metadata" \
        '{store:$store,object_key:$key,file:$file,size_bytes:$size_bytes,sha256:$sha256,content_type:$content_type,etag:$etag,last_modified:$last_modified,metadata:$metadata}' \
        >> "$STAGE/object-manifest.ndjson"
    done < <(jq -r '.Contents[]? | @base64' <<<"$response")
    token="$(jq -r '.NextContinuationToken // empty' <<<"$response")"
    [[ -n "$token" ]] || break
  done
  printf '%s objects inventoried from %s\n' "$index" "$store"
}

backup_store b2 "$B2_S3_ENDPOINT" "$B2_S3_REGION" "$B2_S3_BUCKET" "$B2_S3_ACCESS_KEY_ID" "$B2_S3_SECRET_ACCESS_KEY"
backup_store r2 "$CLOUDFLARE_R2_ENDPOINT" "$CLOUDFLARE_R2_REGION" "$CLOUDFLARE_R2_BUCKET" "$CLOUDFLARE_R2_ACCESS_KEY_ID" "$CLOUDFLARE_R2_SECRET_ACCESS_KEY"
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_DEFAULT_REGION

# Hash every Appwrite export file. These hashes are validated by the restore drill.
: > "$STAGE/appwrite-manifest.sha256"
while IFS= read -r -d '' path; do
  (cd "$STAGE" && sha256sum "${path#"$STAGE/"}") >> "$STAGE/appwrite-manifest.sha256"
done < <(find "$STAGE/appwrite-export" -type f -print0)

cat > "$STAGE/backup-metadata.txt" <<EOF
created_at_utc=$STAMP
b2_bucket=$B2_S3_BUCKET
b2_endpoint_host=$B2_HOST
r2_bucket=$CLOUDFLARE_R2_BUCKET
r2_endpoint_host=$R2_HOST
archive_bucket=$ARCHIVE_S3_BUCKET
archive_endpoint_host=$ARCHIVE_HOST
appwrite_project_id=$APPWRITE_BACKUP_PROJECT_ID
appwrite_database_id=$APPWRITE_BACKUP_DATABASE_ID
object_manifest=object-manifest.ndjson
appwrite_manifest=appwrite-manifest.sha256
checksum_algorithm=sha256
EOF

BUNDLE="$BACKUP_ROOT/testagram-media-$STAMP.tar.gz"
ENCRYPTED="$BUNDLE.age"
tar -C "$STAGE" -czf "$BUNDLE" .
age -r "$TESTAGRAM_BACKUP_AGE_RECIPIENT" -o "$ENCRYPTED.partial" "$BUNDLE"
mv "$ENCRYPTED.partial" "$ENCRYPTED"
sha256sum "$ENCRYPTED" > "$ENCRYPTED.sha256"
chmod 0600 "$ENCRYPTED" "$ENCRYPTED.sha256"

export AWS_ACCESS_KEY_ID="$ARCHIVE_S3_ACCESS_KEY_ID"
export AWS_SECRET_ACCESS_KEY="$ARCHIVE_S3_SECRET_ACCESS_KEY"
export AWS_DEFAULT_REGION="$ARCHIVE_S3_REGION"
export AWS_EC2_METADATA_DISABLED=true
aws s3 cp "$ENCRYPTED" "s3://$ARCHIVE_S3_BUCKET/$ARCHIVE_PREFIX/$(basename "$ENCRYPTED")" --endpoint-url "$ARCHIVE_S3_ENDPOINT" --region "$ARCHIVE_S3_REGION" --only-show-errors
aws s3 cp "$ENCRYPTED.sha256" "s3://$ARCHIVE_S3_BUCKET/$ARCHIVE_PREFIX/$(basename "$ENCRYPTED").sha256" --endpoint-url "$ARCHIVE_S3_ENDPOINT" --region "$ARCHIVE_S3_REGION" --only-show-errors
aws s3api get-object --bucket "$ARCHIVE_S3_BUCKET" --key "$ARCHIVE_PREFIX/$(basename "$ENCRYPTED")" --endpoint-url "$ARCHIVE_S3_ENDPOINT" --region "$ARCHIVE_S3_REGION" --no-cli-pager "$STAGE/archive-readback.age" >/dev/null
cmp -s "$ENCRYPTED" "$STAGE/archive-readback.age" || { echo "Archive provider read-back verification failed" >&2; exit 1; }
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_DEFAULT_REGION

printf 'Encrypted independent media backup uploaded: s3://%s/%s/%s\n' "$ARCHIVE_S3_BUCKET" "$ARCHIVE_PREFIX" "$(basename "$ENCRYPTED")"
printf 'B2/R2 object bytes and supplied Appwrite export included. Restore/import drill is still required.\n'
