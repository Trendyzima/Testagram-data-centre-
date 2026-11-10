#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL must point at the Backend-owned PostgreSQL database}"

# The Backend Compose stack may have just started. Wait for its PostgreSQL
# endpoint instead of making first deployment depend on operator timing.
database_ready=0
attempt=1
while [ "$attempt" -le 60 ]; do
  if psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc 'SELECT 1' >/dev/null 2>&1; then
    database_ready=1
    break
  fi
  attempt=$((attempt + 1))
  sleep 2
done
if [ "$database_ready" -ne 1 ]; then
  echo "Backend-owned PostgreSQL did not become ready within 120 seconds" >&2
  exit 1
fi
federation_database_name="${FEDERATION_DATABASE_NAME:-testagram_federation}"
case "$federation_database_name" in
  ""|*[!A-Za-z0-9_]* )
    echo "FEDERATION_DATABASE_NAME must contain only letters, digits and underscores" >&2
    exit 1
    ;;
esac

# Federation protocol state has a separate database on the same self-hosted PostgreSQL
# server. This isolates protocol tables without adding a second database provider.
federation_database_exists=$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc "SELECT 1 FROM pg_database WHERE datname = '$federation_database_name'")
if [ "$federation_database_exists" != "1" ]; then
  echo "Creating Federation database: $federation_database_name"
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$federation_database_name\""
fi

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
