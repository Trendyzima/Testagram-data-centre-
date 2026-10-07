#!/usr/bin/env bash
set -euo pipefail

ROOT="${SUPABASE_DATA_ROOT:-/var/lib/testagram/supabase}"
SRC="$ROOT/upstream"
mkdir -p "$SRC"

command -v git >/dev/null 2>&1 || { echo "git is required"; exit 1; }

declare -A REPOS=(
  [postgres]="https://github.com/supabase/postgres.git"
  [auth]="https://github.com/supabase/auth.git"
  [rest]="https://github.com/PostgREST/postgrest.git"
  [realtime]="https://github.com/supabase/realtime.git"
  [storage]="https://github.com/supabase/storage.git"
  [edge-runtime]="https://github.com/supabase/edge-runtime.git"
  [postgres-meta]="https://github.com/supabase/postgres-meta.git"
  [supavisor]="https://github.com/supabase/supavisor.git"
)

declare -A REFS=(
  [postgres]="develop"
  [auth]="master"
  [rest]="main"
  [realtime]="main"
  [storage]="master"
  [edge-runtime]="main"
  [postgres-meta]="master"
  [supavisor]="main"
)

for name in "${!REPOS[@]}"; do
  dir="$SRC/$name"
  url="${REPOS[$name]}"
  ref="${REFS[$name]}"

  if [ -d "$dir/.git" ]; then
    git -C "$dir" remote set-url origin "$url"
    git -C "$dir" fetch --depth=1 origin "$ref"
    git -C "$dir" checkout -B "$ref" "origin/$ref"
    git -C "$dir" reset --hard "origin/$ref"
  else
    git clone --depth=1 --branch "$ref" "$url" "$dir"
  fi

  actual_url="$(git -C "$dir" remote get-url origin)"
  actual_ref="$(git -C "$dir" branch --show-current)"
  actual_sha="$(git -C "$dir" rev-parse HEAD)"
  test "$actual_url" = "$url"
  test "$actual_ref" = "$ref"
  printf '%-16s %s %s\n' "$name" "$actual_ref" "$actual_sha"
done

printf '\nSupabase upstream inventory is ready under %s\n' "$SRC"
