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
-- The provisioning connection is the existing database administrator. Grant
-- it temporary membership so it can transfer pre-existing objects without
-- granting superuser or database-admin rights to the Federation runtime role.
SELECT format('GRANT %I TO %I', :'federation_user', current_user)
WHERE NOT pg_has_role(current_user, :'federation_user', 'MEMBER') \gexec
\connect :federation_db
-- Older installations may contain tables created by the Backend migration
-- connection. Changing database ownership alone does not change table owners;
-- the runtime schema initializer then fails on ALTER TABLE with SQLSTATE 42501.
GRANT USAGE, CREATE ON SCHEMA public TO :"federation_user";
ALTER SCHEMA public OWNER TO :"federation_user";
SELECT format('ALTER TABLE %I.%I OWNER TO %I', n.nspname, c.relname, :'federation_user')
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind IN ('r','p')
  AND pg_get_userbyid(c.relowner) <> :'federation_user'
\gexec
SELECT format('ALTER SEQUENCE %I.%I OWNER TO %I', n.nspname, c.relname, :'federation_user')
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind = 'S'
  AND pg_get_userbyid(c.relowner) <> :'federation_user'
\gexec
SELECT format('ALTER VIEW %I.%I OWNER TO %I', n.nspname, c.relname, :'federation_user')
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind = 'v'
  AND pg_get_userbyid(c.relowner) <> :'federation_user'
\gexec
SELECT format('ALTER MATERIALIZED VIEW %I.%I OWNER TO %I', n.nspname, c.relname, :'federation_user')
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind = 'm'
  AND pg_get_userbyid(c.relowner) <> :'federation_user'
\gexec
DO $verify$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'public'
      AND c.relkind IN ('r','p','S','v','m')
      AND pg_get_userbyid(c.relowner) <> current_user
  ) THEN
    RAISE EXCEPTION 'Federation schema contains objects not owned by the provisioning role';
  END IF;
END;
$verify$;
SQL
