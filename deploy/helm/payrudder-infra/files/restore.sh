#!/bin/sh
# Restores one dump into a database: restore.sh <dump file> <database> [--force]. The database is created when it is not there; one that exists is replaced only with
# --force (it is dropped first, so connections to it must be closed). The owner roles of the dump are not restored (--no-owner), so grant the roles of the
# service afterwards (docs/runbooks/backup-restore.md). Environment: PGHOST PGPORT PGUSER PGPASSWORD of an admin role.
set -eu
file="${1:?usage: restore.sh <dump file> <database> [--force]}"
db="${2:?usage: restore.sh <dump file> <database> [--force]}"
force="${3:-}"
[ -r "$file" ] || { echo "restore: cannot read $file" >&2; exit 1; }
case "$db" in *[!a-zA-Z0-9_]*) echo "restore: the database name may hold letters, digits and underscores only" >&2; exit 1 ;; esac

exists="$(psql -XAt -d postgres -c "SELECT 1 FROM pg_database WHERE datname = '$db'")"
if [ -n "$exists" ]; then
  [ "$force" = "--force" ] || { echo "restore: $db exists; pass --force to replace it" >&2; exit 1; }
  psql -X -v ON_ERROR_STOP=1 -d postgres -c "DROP DATABASE \"$db\""
fi
psql -X -v ON_ERROR_STOP=1 -d postgres -c "CREATE DATABASE \"$db\""
pg_restore --no-owner --exit-on-error --dbname="$db" "$file"
echo "restore: $file -> $db"
