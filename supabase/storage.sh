#!/usr/bin/env bash
set -euo pipefail
ROOT="${TESTAGRAM_STORAGE_ROOT:-/var/lib/testagram/storage}"
SUPABASE="$ROOT/supabase"
MEDIA="$ROOT/media"
NODES="$ROOT/nodes"
mkdir -p "$SUPABASE" "$MEDIA" "$NODES"   "$MEDIA/posts" "$MEDIA/images" "$MEDIA/videos" "$MEDIA/originals"   "$MEDIA/hls" "$MEDIA/posters" "$MEDIA/avatars" "$MEDIA/stories"   "$MEDIA/messages" "$MEDIA/attachments" "$MEDIA/tmp"
chmod 0770 "$ROOT" "$SUPABASE" "$MEDIA" "$NODES"
find "$MEDIA" -mindepth 1 -maxdepth 1 -type d -exec chmod 0770 {} +
if [ "${TESTAGRAM_STORAGE_OWNER:-}" != "" ]; then chown -R "$TESTAGRAM_STORAGE_OWNER" "$ROOT"; fi
touch "$ROOT/.testagram-storage"
chmod 0660 "$ROOT/.testagram-storage"
printf 'Testagram VPS storage ready: %s\n' "$ROOT"
printf '  Supabase objects: %s\n' "$SUPABASE"
printf '  Testagram media:  %s\n' "$MEDIA"
printf '  Node volumes:     %s\n' "$NODES"
