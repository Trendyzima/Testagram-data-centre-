#!/usr/bin/env bash
set -euo pipefail

ROOT="${SUPABASE_DATA_ROOT:-/var/lib/testagram/supabase}"
SRC="$ROOT/upstream"
LOCK_FILE="$(dirname "$0")/upstream.lock"
mkdir -p "$SRC"

command -v git >/dev/null 2>&1 || { echo "git is required" >&2; exit 1; }
test -f "$LOCK_FILE" || { echo "Supabase upstream lock file is missing: $LOCK_FILE" >&2; exit 1; }

# Every checkout is detached at an immutable, reviewed SHA. The branch column
# in upstream.lock is provenance only; it is never fetched as a build input.
while IFS=$'\t' read -r name url source_ref sha; do
  [[ -z "${name:-}" || "$name" == \#* ]] && continue
  if [[ ! "$sha" =~ ^[0-9a-f]{40}$ ]]; then
    echo "Invalid pinned SHA for $name: $sha" >&2
    exit 1
  fi
  dir="$SRC/$name"

  if [ ! -d "$dir/.git" ]; then
    mkdir -p "$dir"
    git -C "$dir" init -q
    git -C "$dir" remote add origin "$url"
  else
    git -C "$dir" remote set-url origin "$url"
  fi

  git -C "$dir" fetch --depth=1 origin "$sha"
  git -C "$dir" checkout --detach --force FETCH_HEAD
  actual_url="$(git -C "$dir" remote get-url origin)"
  actual_sha="$(git -C "$dir" rev-parse HEAD)"
  test "$actual_url" = "$url"
  test "$actual_sha" = "$sha"
  printf '%-16s source-ref=%-8s sha=%s\n' "$name" "$source_ref" "$actual_sha"
done < "$LOCK_FILE"

printf '\nPinned Supabase upstream inventory is ready under %s\n' "$SRC"
