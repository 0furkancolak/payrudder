#!/bin/sh
# Writes the config into /tmp (the image's /etc/pgbouncer is not writable by its user) and starts PgBouncer in the foreground.
set -eu
: "${PGBOUNCER_AUTH_PASSWORD:?the password of the postgres user (Postgres.PGPASSWORD)}"
dir=/tmp/pgbouncer
mkdir -p "$dir"
# auth_user reads every other role's verifier from the cluster; only its own password is in the file.
printf '"postgres" "%s"\n' "$PGBOUNCER_AUTH_PASSWORD" >"$dir/userlist.txt"
cat >"$dir/pgbouncer.ini" <<INI
[databases]
* = host=${PG_HOST:-postgres.railway.internal} port=5432

[pgbouncer]
listen_addr = *
listen_port = ${PORT:-6432}
auth_type = scram-sha-256
auth_file = $dir/userlist.txt
auth_user = postgres
auth_query = SELECT usename, passwd FROM pg_shadow WHERE usename = \$1
pool_mode = transaction
default_pool_size = 16
max_client_conn = 1000
max_prepared_statements = 200
admin_users = postgres
ignore_startup_parameters = extra_float_digits
INI
exec pgbouncer "$dir/pgbouncer.ini"
