#!/bin/sh
# Builds a service's schema in a scratch database from its goose migrations (the Up part of each file, in order) without goose: schema.sh <database> <migrations dir>.
# For the backup tests and the point-in-time drill, which need the real tables to check real invariants. Environment: PG* of a role that may create objects.
set -eu
db="${1:?usage: schema.sh <database> <migrations dir>}"
dir="${2:?usage: schema.sh <database> <migrations dir>}"
for f in $(ls "$dir"/*.sql | sort); do
  sed -n '/^-- +goose Up/,/^-- +goose Down/p' "$f" | psql -X -q -v ON_ERROR_STOP=1 -d "$db" -f -
done
