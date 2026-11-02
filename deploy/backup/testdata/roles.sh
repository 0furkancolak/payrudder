#!/bin/sh
# The services' own roles on a restored copy of the databases: roles.sh <tenant uuid> <invoices the tenant has in billing_db>. A physical restore brings the roles and their
# grants, but a copy is only usable if the roles that the services connect as work on it: billing_app is subject to row-level security (no rows without a tenant, the
# tenant's with one), payments_system bypasses it. They connect over the network, so their passwords are checked too ("<role>_local" is what the compose file gives them).
# What a service's /readyz asks of Postgres is a ping as its app role; this is that, and one query that needs the grants and the policies. Starting the services themselves
# also needs Kafka and RabbitMQ (their readiness checks them), which the drill does not run. Environment: the superuser connection of the scratch server (PGUSER).
set -eu
tenant="${1:?usage: roles.sh <tenant uuid> <invoices of the tenant>}"
want="${2:?usage: roles.sh <tenant uuid> <invoices of the tenant>}"
host="$(hostname -i)"
as() { u="$1"; d="$2"; shift 2; PGPASSWORD="${u}_local" psql -X -qAt -v ON_ERROR_STOP=1 -h "$host" -U "$u" -d "$d" "$@"; }
[ "$(as billing_app billing_db -c 'SELECT count(*) FROM invoices')" = 0 ] || { echo "roles: billing_app sees invoices without a tenant" >&2; exit 1; }
got="$(as billing_app billing_db -c "SELECT set_config('app.tenant_id', '$tenant', false)" -c 'SELECT count(*) FROM invoices' | tail -1)"
[ "$got" = "$want" ] || { echo "roles: billing_app sees $got invoices of the tenant, want $want" >&2; exit 1; }
seen="$(as payments_system payments_db -c 'SELECT count(*) FROM payment_ledger')"
all="$(psql -X -qAt -v ON_ERROR_STOP=1 -d payments_db -c 'SELECT count(*) FROM payment_ledger')"
{ [ "$seen" = "$all" ] && [ "$seen" -gt 0 ]; } || { echo "roles: payments_system sees $seen ledger rows, the superuser sees $all" >&2; exit 1; }
echo "roles ok: billing_app sees $got invoices of the tenant and none without one, payments_system sees all $seen ledger rows"
