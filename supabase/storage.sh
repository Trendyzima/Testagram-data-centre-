#!/usr/bin/env bash
set -euo pipefail

# The VPS is the storage authority. This path is the only host root that
# Testagram services use for persistent application media and node volumes.
ROOT="${TESTAGRAM_STORAGE_ROOT:-/var/lib/testagram/storage}"
SUPABASE="$ROOT/supabase"
MEDIA="$ROOT/media"
NODES="$ROOT/nodes"

# Stable layout: application uploads and derived media never depend on
# container-local filesystems.
mkdir -p \
  "$SUPABASE" \
  "$MEDIA/posts/images" \
  "$MEDIA/posts/videos" \
  "$MEDIA/uploads" \
  "$MEDIA/videos/originals" \
  "$MEDIA/videos/hls" \
  "$MEDIA/videos/posters" \
  "$MEDIA/tmp" \
  "$NODES"

# Marker makes the managed root unambiguous during installation/auditing.
printf 'testagram-vps-storage-v1\n' > "$ROOT/.testagram-storage-root"
chmod 0660 "$ROOT/.testagram-storage-root"

# Keep the tree private to the VPS services by default. An operator can
# explicitly provide an owner/group for a native host deployment.
chmod 0770 "$ROOT" "$SUPABASE" "$MEDIA" "$NODES"
find "$MEDIA" -type d -exec chmod 0770 {} +
if [ -n "${TESTAGRAM_STORAGE_OWNER:-}" ]; then
  chown -R "$TESTAGRAM_STORAGE_OWNER" "$ROOT"
fi
if [ -n "${TESTAGRAM_STORAGE_GROUP:-}" ]; then
  chgrp -R "$TESTAGRAM_STORAGE_GROUP" "$ROOT"
fi

printf 'Testagram VPS storage ready: %s\n' "$ROOT"
printf '  Supabase objects: %s\n' "$SUPABASE"
printf '  Testagram media:  %s\n' "$MEDIA"
printf '  Post images:      %s\n' "$MEDIA/posts/images"
printf '  Post videos:      %s\n' "$MEDIA/posts/videos"
printf '  Video originals:  %s\n' "$MEDIA/videos/originals"
printf '  HLS output:       %s\n' "$MEDIA/videos/hls"
printf '  Posters:          %s\n' "$MEDIA/videos/posters"
printf '  Node volumes:     %s\n' "$NODES"
