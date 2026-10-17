#!/usr/bin/env bash
# Creates one database per service and the three roles per service ([06-multi-tenancy.md](../../../docs/06-multi-tenancy.md)):
#   <svc>_migrator  owns the database, runs migrations
#   <svc>_app       request handlers; subject to RLS (FORCE ROW LEVEL SECURITY is set per table by migrations)
#   <svc>_system    consumers, relays, schedulers; BYPASSRLS
# Local development only: passwords are "<role>_local".
set -euo pipefail

SERVICES=(plans ingest aggregator billing payments notifier assets mockpsp)

for svc in "${SERVICES[@]}"; do
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<-SQL
	CREATE ROLE ${svc}_migrator LOGIN PASSWORD '${svc}_migrator_local';
	CREATE ROLE ${svc}_app      LOGIN PASSWORD '${svc}_app_local';
	CREATE ROLE ${svc}_system   LOGIN PASSWORD '${svc}_system_local' BYPASSRLS;
	CREATE DATABASE ${svc}_db OWNER ${svc}_migrator;
	REVOKE ALL ON DATABASE ${svc}_db FROM PUBLIC;
	GRANT CONNECT ON DATABASE ${svc}_db TO ${svc}_migrator, ${svc}_app, ${svc}_system;
SQL
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "${svc}_db" <<-SQL
	REVOKE CREATE ON SCHEMA public FROM PUBLIC;
	ALTER SCHEMA public OWNER TO ${svc}_migrator;
	GRANT USAGE ON SCHEMA public TO ${svc}_app, ${svc}_system;
SQL
done
