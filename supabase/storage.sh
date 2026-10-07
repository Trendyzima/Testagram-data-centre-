#!/usr/bin/env bash
set -euo pipefail

ROOT="${TESTAGRAM_STORAGE_ROOT:-/var/lib/testagram/storage}"
SUPABASE="$ROOT/supabase"
MEDIA="$ROOT/media"
NODES="$ROOT/nodes"

mkdir -p "$SUPABASE" "$MEDIA" "$NODES"
chmod 0770 "$ROOT" "$SUPABASE" "$MEDIA" "$NODES"

if [ "${TESTAGRAM_STORAGE_OWNER:-}" != "" ]; then
  chown -R "$TESTAGRAM_STORAGE_OWNER" "$ROOT"
fi

printf 'Testagram VPS storage ready: %s\n' "$ROOT"
printf '  Supabase objects: %s\n' "$SUPABASE"
printf '  Testagram media:  %s\n' "$MEDIA"
printf '  Node volumes:     %s\n' "$NODES"
