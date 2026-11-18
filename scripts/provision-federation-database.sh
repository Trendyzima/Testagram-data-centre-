#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL must point at the Backend-owned PostgreSQL database}"
: "${FEDERATION_DATABASE_USER:?FEDERATION_DATABASE_USER must be set}"
: "${FEDERATION_DATABASE_PASSWORD:?FEDERATION_DATABASE_PASSWORD must be set}"
federation_database_name="${FEDERATION_DATABASE_NAME:-testagram_federation}"

case "$federation_database_name" in
  ""|*[!A-Za-z0-9_]* )
    echo "FEDERATION_DATABASE_NAME must contain only letters, digits and underscores" >&2
    exit 1
    ;;
esac
case "$FEDERATION_DATABASE_USER" in
  ""|*[!A-Za-z0-9_]* )
    echo "FEDERATION_DATABASE_USER must contain only letters, digits and underscores" >&2
    exit 1
    ;;
esac

admin_user="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc 'SELECT current_user')"
if [ "$admin_user" = "$FEDERATION_DATABASE_USER" ]; then
  echo "FEDERATION_DATABASE_USER must not reuse the Backend database administrator" >&2
  exit 1
fi

export FEDERATION_DATABASE_NAME="$federation_database_name"
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 <<'SQL'
\getenv federation_user FEDERATION_DATABASE_USER
\getenv federation_password FEDERATION_DATABASE_PASSWORD
\getenv federation_db FEDERATION_DATABASE_NAME
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS', :'federation_user', :'federation_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'federation_user') \gexec
SELECT format('ALTER ROLE %I WITH LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS', :'federation_user', :'federation_password') \gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'federation_db', :'federation_user')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'federation_db') \gexec
SELECT format('ALTER DATABASE %I OWNER TO %I', :'federation_db', :'federation_user') \gexec
SQL
