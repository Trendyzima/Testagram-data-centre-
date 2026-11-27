#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL must point at the Backend administrative PostgreSQL database}"
: "${MEDIA_DATABASE_URL:?MEDIA_DATABASE_URL must point at the dedicated media database}"
: "${MEDIA_DATABASE_USER:?MEDIA_DATABASE_USER must be set}"
: "${MEDIA_DATABASE_PASSWORD:?MEDIA_DATABASE_PASSWORD must be set}"
media_database_name="${MEDIA_DATABASE_NAME:-testagram_media}"

case "$media_database_name" in
  ""|*[!A-Za-z0-9_]* )
    echo "MEDIA_DATABASE_NAME must contain only letters, digits and underscores" >&2
    exit 1
    ;;
esac
case "$MEDIA_DATABASE_USER" in
  ""|*[!A-Za-z0-9_]* )
    echo "MEDIA_DATABASE_USER must contain only letters, digits and underscores" >&2
    exit 1
    ;;
esac

admin_identity="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc \
  "SELECT current_user || ':' || current_database()")"
admin_user="${admin_identity%%:*}"
admin_database="${admin_identity#*:}"
if [ "$admin_user" = "$MEDIA_DATABASE_USER" ]; then
  echo "MEDIA_DATABASE_USER must not reuse the Backend database administrator" >&2
  exit 1
fi
if [ "$admin_database" = "$media_database_name" ]; then
  echo "Media lifecycle must use a database separate from the Backend administrative database" >&2
  exit 1
fi

export MEDIA_DATABASE_NAME="$media_database_name"
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 <<'SQL'
\getenv media_user MEDIA_DATABASE_USER
\getenv media_password MEDIA_DATABASE_PASSWORD
\getenv media_db MEDIA_DATABASE_NAME
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS', :'media_user', :'media_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'media_user') \gexec
SELECT format('ALTER ROLE %I WITH LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS', :'media_user', :'media_password') \gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'media_db', :'media_user')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'media_db') \gexec
SELECT format('ALTER DATABASE %I OWNER TO %I', :'media_db', :'media_user') \gexec
SQL

runtime_identity="$(psql "$MEDIA_DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc \
  "SELECT current_user || ':' || current_database()")"
expected_identity="$MEDIA_DATABASE_USER:$media_database_name"
if [ "$runtime_identity" != "$expected_identity" ]; then
  echo "Media runtime URL identity mismatch: expected $expected_identity, got $runtime_identity" >&2
  exit 1
fi

psql "$MEDIA_DATABASE_URL" -v ON_ERROR_STOP=1 -f /migrations/001_media_lifecycle.sql
psql "$MEDIA_DATABASE_URL" -v ON_ERROR_STOP=1 -f /migrations/002_primary_origin_restore.sql
psql "$MEDIA_DATABASE_URL" -v ON_ERROR_STOP=1 -f /migrations/003_identity_evidence_namespace.sql
psql "$MEDIA_DATABASE_URL" -v ON_ERROR_STOP=1 -f /migrations/004_identity_evidence_b2_only.sql
psql "$MEDIA_DATABASE_URL" -v ON_ERROR_STOP=1 -f /migrations/005_identity_evidence_permanent_purge.sql
psql "$MEDIA_DATABASE_URL" -v ON_ERROR_STOP=1 -f /migrations/006_identity_evidence_legacy_reverify.sql

echo "Media database provisioned and migrations applied using the dedicated non-superuser role."
