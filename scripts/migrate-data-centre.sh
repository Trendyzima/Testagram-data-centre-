#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL must point at the Backend-owned PostgreSQL database}"

migration_table='public.testagram_data_centre_schema_migrations'

psql "$DATABASE_URL" -v ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE IF NOT EXISTS public.testagram_data_centre_schema_migrations (
  version text PRIMARY KEY,
  checksum text NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
  applied_at timestamptz NOT NULL DEFAULT now()
);
SQL

found=0
for migration in /migrations/*.sql; do
  [ -f "$migration" ] || continue
  found=1
  version=$(basename "$migration")
  case "$version" in
    *[!A-Za-z0-9_.-]*)
      echo "Refusing unexpected migration filename: $version" >&2
      exit 1
      ;;
  esac
  checksum=$(sha256sum "$migration" | awk '{print $1}')
  existing=$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc "SELECT checksum FROM $migration_table WHERE version = '$version'")

  if [ -n "$existing" ]; then
    if [ "$existing" != "$checksum" ]; then
      echo "Applied migration $version was modified; add a new forward-only migration instead." >&2
      exit 1
    fi
    echo "Skipping applied migration: $version"
    continue
  fi

  echo "Applying migration: $version"
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
    -c 'BEGIN' \
    -f "$migration" \
    -c "INSERT INTO $migration_table (version, checksum) VALUES ('$version', '$checksum')" \
    -c 'COMMIT'
done

if [ "$found" -ne 1 ]; then
  echo "No Data Centre SQL migrations found in /migrations" >&2
  exit 1
fi
