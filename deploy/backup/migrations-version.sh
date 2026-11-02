#!/bin/sh
# Compares the migrations a database has applied with the ones the code ships: migrations-version.sh <database> <migrations dir>. Run it after every restore, before a service
# is pointed at the copy: a service that is newer than its database runs its migration hook (fine), but a database that is NEWER than the service's migrations (a restore of
# a later timeline into an older image, or an older image deployed over a newer database) is a mismatch no migration can repair, and the service may read columns it does not know.
# Reads goose's table (goose_db_version, the highest applied version) and the highest NNNNN_*.sql of the directory (the cells' tenant/ folder is not looked at). Prints
#   migrations <database> applied=<N> shipped=<M> <equal|behind|ahead>
# Exit status: 0 equal; 0 behind (the database is older: the migration hook brings it up, as after any restore to a time before a deploy) unless STRICT=1, which fails it; 1 ahead
# (never acceptable) and 1 when the table or the directory cannot be read. Environment: PG* of a role that may read the table.
set -eu
db="${1:?usage: migrations-version.sh <database> <migrations dir>}"
dir="${2:?usage: migrations-version.sh <database> <migrations dir>}"
[ -d "$dir" ] || { echo "migrations-version: $dir is not a directory" >&2; exit 1; }
shipped="$(ls "$dir" | sed -n 's/^\([0-9][0-9]*\)_.*\.sql$/\1/p' | sort -n | tail -1 | sed 's/^0*//')"
[ -n "$shipped" ] || { echo "migrations-version: no migrations in $dir" >&2; exit 1; }
applied="$(psql -X -qAt -v ON_ERROR_STOP=1 -d "$db" -c "SELECT COALESCE(max(version_id), 0) FROM goose_db_version WHERE is_applied")" || { echo "migrations-version: $db has no goose_db_version table" >&2; exit 1; }
state=equal
[ "$applied" -lt "$shipped" ] && state=behind
[ "$applied" -gt "$shipped" ] && state=ahead
echo "migrations $db applied=$applied shipped=$shipped $state"
case "$state" in
  equal) ;;
  behind) [ "${STRICT:-}" != 1 ] || { echo "migrations-version: $db is behind the code (STRICT=1)" >&2; exit 1; } ;;
  ahead) echo "migrations-version: $db has migrations the code does not know; do not start services of this version on it" >&2; exit 1 ;;
esac
