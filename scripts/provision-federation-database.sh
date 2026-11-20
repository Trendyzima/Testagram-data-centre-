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
-- Existing Federation tables may belong to a separate Backend migration role.
-- Database ownership does not transfer ownership of objects already inside it.
-- Discover every owner in public, grant the provisioning administrator temporary
-- membership (required by PostgreSQL REASSIGN OWNED), then transfer only within
-- this isolated Federation database. The runtime role remains non-superuser.
\connect :federation_db
SELECT format('GRANT %I TO %I', owners.owner_name, current_user)
FROM (
  SELECT DISTINCT pg_get_userbyid(c.relowner) AS owner_name
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'public'
  UNION
  SELECT pg_get_userbyid(nspowner)
  FROM pg_namespace
  WHERE nspname = 'public'
) owners
WHERE owners.owner_name <> :'federation_user'
  AND NOT pg_has_role(current_user, owners.owner_name, 'MEMBER')
\gexec
-- REASSIGN OWNED requires membership in both the old and new roles. The
-- provisioning administrator is already a member of the Federation role.
SELECT format('REASSIGN OWNED BY %I TO %I', owners.owner_name, :'federation_user')
FROM (
  SELECT DISTINCT pg_get_userbyid(c.relowner) AS owner_name
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'public'
  UNION
  SELECT pg_get_userbyid(nspowner)
  FROM pg_namespace
  WHERE nspname = 'public'
) owners
WHERE owners.owner_name <> :'federation_user'
\gexec
ALTER SCHEMA public OWNER TO :"federation_user";
SET ROLE :"federation_user";
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
    RAISE EXCEPTION 'Federation schema contains objects not owned by the Federation runtime role';
  END IF;
  IF EXISTS (
    SELECT 1 FROM pg_namespace
    WHERE nspname = 'public' AND pg_get_userbyid(nspowner) <> current_user
  ) THEN
    RAISE EXCEPTION 'Federation public schema is not owned by the Federation runtime role';
  END IF;
END;
$verify$;
SQL
