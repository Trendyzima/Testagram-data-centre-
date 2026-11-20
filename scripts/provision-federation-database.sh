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
SELECT NOT pg_has_role(current_user, :'federation_user', 'MEMBER') AS federation_membership_granted \gset
\if :federation_membership_granted
  SELECT format('GRANT %I TO %I', :'federation_user', current_user) \gexec
\endif
SELECT format('CREATE DATABASE %I OWNER %I', :'federation_db', :'federation_user')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'federation_db') \gexec
SELECT format('ALTER DATABASE %I OWNER TO %I', :'federation_db', :'federation_user') \gexec
\connect :federation_db
-- Transfer only Federation public-schema relations. REASSIGN OWNED is deliberately
-- avoided: it also touches shared objects and objects in other databases owned
-- by the old role, and can fail on system-required objects (as it did when the
-- Backend administrator owned the existing Federation tables).
CREATE TEMP TABLE federation_old_owner_memberships(owner_name name PRIMARY KEY)
  ON COMMIT PRESERVE ROWS;
INSERT INTO federation_old_owner_memberships(owner_name)
SELECT DISTINCT owners.owner_name::name
FROM (
  SELECT pg_get_userbyid(c.relowner) AS owner_name
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'public' AND c.relkind IN ('r','p','S','v','m')
  UNION
  SELECT pg_get_userbyid(nspowner)
  FROM pg_namespace
  WHERE nspname = 'public'
) owners
WHERE owners.owner_name NOT IN (:'federation_user', 'pg_database_owner')
  AND owners.owner_name <> current_user
  AND NOT pg_has_role(current_user, owners.owner_name, 'MEMBER');

SELECT format('GRANT %I TO %I', owner_name, current_user)
FROM federation_old_owner_memberships
\gexec

ALTER SCHEMA public OWNER TO :"federation_user";
-- Change only objects in public; never reassign shared/system objects or
-- objects owned by the Backend administrator in another database.
SELECT format(
  'ALTER %s %I.%I OWNER TO %I',
  CASE c.relkind
    WHEN 'r' THEN 'TABLE'
    WHEN 'p' THEN 'TABLE'
    WHEN 'S' THEN 'SEQUENCE'
    WHEN 'v' THEN 'VIEW'
    WHEN 'm' THEN 'MATERIALIZED VIEW'
  END,
  n.nspname,
  c.relname,
  :'federation_user'
)
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public'
  AND c.relkind IN ('r','p','S','v','m')
  AND pg_get_userbyid(c.relowner) <> :'federation_user'
ORDER BY CASE c.relkind
  WHEN 'r' THEN 1 WHEN 'p' THEN 1
  WHEN 'S' THEN 2 WHEN 'v' THEN 3 WHEN 'm' THEN 4
END, c.relname
\gexec
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
RESET ROLE;
SELECT format('REVOKE %I FROM %I', owner_name, current_user)
FROM federation_old_owner_memberships
\gexec
\if :federation_membership_granted
  SELECT format('REVOKE %I FROM %I', :'federation_user', current_user) \gexec
\endif
DROP TABLE federation_old_owner_memberships;
SQL
