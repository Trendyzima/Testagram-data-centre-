#!/usr/bin/env bash
set -euo pipefail
ROOT="${TESTAGRAM_STORAGE_ROOT:-/var/lib/testagram/storage}"
SUPABASE="$ROOT/supabase"
MEDIA="$ROOT/media"
NODES="$ROOT/nodes"

# Canonical VPS-owned media layout. Application containers only receive the
# explicit media mount; the host filesystem itself is never exposed directly.
mkdir -p "$SUPABASE" "$NODES"   "$MEDIA/posts/images" "$MEDIA/posts/videos" "$MEDIA/uploads" "$MEDIA/videos/originals" "$MEDIA/videos/hls" "$MEDIA/videos/posters" "$MEDIA/avatars" "$MEDIA/stories" "$MEDIA/messages" "$MEDIA/attachments" "$MEDIA/tmp"

chmod 0770 "$ROOT" "$SUPABASE" "$MEDIA" "$NODES"
find "$MEDIA" -type d -exec chmod 0770 {} +
if [ "${TESTAGRAM_STORAGE_OWNER:-}" != "" ]; then
  chown -R "$TESTAGRAM_STORAGE_OWNER" "$ROOT"
fi

touch "$ROOT/.testagram-storage-root"
chmod 0660 "$ROOT/.testagram-storage-root"

printf 'Testagram VPS storage ready: %s\n' "$ROOT"
printf '  Supabase objects: %s\n' "$SUPABASE"
printf '  Testagram media:  %s\n' "$MEDIA"
printf '  Node volumes:     %s\n' "$NODES"
