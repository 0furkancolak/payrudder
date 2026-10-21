#!/bin/sh
# One-shot, idempotent: one database and three roles per service on Railway's managed Postgres (the same layout as deploy/compose/postgres/01-databases.sh,
# docs/06-multi-tenancy.md):
#   <svc>_migrator  owns the database, runs migrations
#   <svc>_app       request handlers; subject to RLS
#   <svc>_system    consumers, relays, schedulers; BYPASSRLS
# Needs a role that may CREATE ROLE ... BYPASSRLS (Railway's `postgres` user is a superuser). Connection: the standard PG* variables.
# Passwords: PG_MIGRATOR_PASSWORD, PG_APP_PASSWORD, PG_SYSTEM_PASSWORD, shared by every service (each role can only connect to its own database).
# Re-running rotates the passwords to the current values.
set -eu

: "${PGHOST:?}" "${PGUSER:?}" "${PGPASSWORD:?}"
: "${PG_MIGRATOR_PASSWORD:?}" "${PG_APP_PASSWORD:?}" "${PG_SYSTEM_PASSWORD:?}"
export PGDATABASE="${PGDATABASE:-postgres}"
SERVICES="${QUOTIFY_PG_SERVICES:-plans ingest aggregator billing payments notifier assets mockpsp}"

for pw in "$PG_MIGRATOR_PASSWORD" "$PG_APP_PASSWORD" "$PG_SYSTEM_PASSWORD"; do
  case "$pw" in
    *[!A-Za-z0-9]* | "") echo "role passwords must be alphanumeric (they go into URLs): use openssl rand -hex 24" >&2; exit 1 ;;
  esac
done

until pg_isready -q; do echo "waiting for postgres"; sleep 2; done

# role <name> <password> <extra attributes>: create when missing, then (re)set the password and attributes.
role() {
  psql -v ON_ERROR_STOP=1 -q -v role="$1" -v pw="$2" -v attrs="$3" <<'SQL'
SELECT format('CREATE ROLE %I', :'role') WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'role') \gexec
SELECT format('ALTER ROLE %I LOGIN PASSWORD %L %s', :'role', :'pw', :'attrs') \gexec
SQL
}

for svc in $SERVICES; do
  echo "== $svc"
  role "${svc}_migrator" "$PG_MIGRATOR_PASSWORD" "NOBYPASSRLS"
  role "${svc}_app" "$PG_APP_PASSWORD" "NOBYPASSRLS"
  role "${svc}_system" "$PG_SYSTEM_PASSWORD" "BYPASSRLS"
  psql -v ON_ERROR_STOP=1 -q -v svc="$svc" <<'SQL'
SELECT format('CREATE DATABASE %I OWNER %I', :'svc' || '_db', :'svc' || '_migrator')
  WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'svc' || '_db') \gexec
SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', :'svc' || '_db') \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO %I, %I, %I', :'svc' || '_db', :'svc' || '_migrator', :'svc' || '_app', :'svc' || '_system') \gexec
SQL
  PGDATABASE="${svc}_db" psql -v ON_ERROR_STOP=1 -q -v svc="$svc" <<'SQL'
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
SELECT format('ALTER SCHEMA public OWNER TO %I', :'svc' || '_migrator') \gexec
SELECT format('GRANT USAGE ON SCHEMA public TO %I, %I', :'svc' || '_app', :'svc' || '_system') \gexec
SQL
done
echo "postgres roles and databases ready"
