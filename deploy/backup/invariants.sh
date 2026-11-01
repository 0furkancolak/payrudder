#!/bin/sh
# Checks that a restored database holds consistent data and prints how much of it there is: invariants.sh <database>...
# The restore drills run it on a restored copy (verify.sh for the dumps, drill.sh for point-in-time recovery), so a restore that "works" but lost or tore data is found
# before the day the copy is needed. It only reads. Environment: PGHOST PGPORT PGUSER PGPASSWORD of a role that sees every row (a superuser or BYPASSRLS: with row level
# security forced any other role sees no rows and every check passes on nothing).
#
# What must hold in every consistent copy (tables a database does not have are skipped, so one script serves every service database; the tenant schemas t_* of a cell
# database are checked like public):
#   invoices          total_minor = max(0, sum of its lines) and subtotal_minor = sum of its lines but tax (an invoice with lines; drafts too: lines are written with it)
#   credit_lots       remaining = sum of the lot's ledger rows (grant, consume, expire)
#   manual_receipts   amount_minor = the amount of the payment_ledger row it belongs to
# Prints "ok" lines and one "counts <database> table=rows ..." line per database (the key tables' row counts, which the point-in-time drill compares with the live database
# at the target time). A violation is printed to stderr with its row count and the exit status is 1.
set -eu
[ "$#" -gt 0 ] || { echo "usage: invariants.sh <database>..." >&2; exit 1; }
bad=0

q() { psql -X -qAt -v ON_ERROR_STOP=1 "$@"; }

# check <database> <schema> <label> <table that must exist> <sql counting the violating rows>
check() {
  [ "$(PGOPTIONS="-c search_path=$2" q -d "$1" -c "SELECT to_regclass('$4') IS NOT NULL")" = t ] || return 0
  n="$(PGOPTIONS="-c search_path=$2" q -d "$1" -c "$5")"
  checks=$((checks + 1))
  if [ "$n" != 0 ]; then
    echo "invariants: $1 schema $2: $3: $n violating rows" >&2
    bad=1
  fi
}

for db in "$@"; do
  checks=0
  schemas="$(q -d "$db" -c "SELECT DISTINCT table_schema FROM information_schema.tables WHERE table_type = 'BASE TABLE' AND (table_schema = 'public' OR table_schema LIKE 't\_%') ORDER BY 1")"
  counts=""
  for s in $schemas; do
    case "$s" in *[!a-z0-9_]*) continue ;; esac
    check "$db" "$s" "invoice totals differ from the sum of their lines" invoice_lines "
      SELECT count(*) FROM invoices i, LATERAL (
        SELECT sum(l.amount_minor) AS total, coalesce(sum(l.amount_minor) FILTER (WHERE l.kind <> 'TAX'), 0) AS sub
        FROM invoice_lines l WHERE l.tenant_id = i.tenant_id AND l.invoice_id = i.id HAVING count(*) > 0) x
      WHERE i.total_minor <> greatest(0, x.total) OR i.subtotal_minor <> x.sub"
    check "$db" "$s" "credit lot balances differ from their ledger" credit_ledger "
      SELECT count(*) FROM credit_lots l
      WHERE l.remaining <> coalesce((SELECT sum(e.amount) FROM credit_ledger e WHERE e.tenant_id = l.tenant_id AND e.lot_id = l.id), 0)"
    check "$db" "$s" "manual receipts differ from their ledger rows" manual_receipts "
      SELECT count(*) FROM manual_receipts r JOIN payment_ledger l ON l.id = r.ledger_id WHERE r.amount_minor <> l.amount_minor"
    for t in invoices invoice_lines credit_lots credit_ledger payment_ledger manual_receipts; do
      if [ "$(PGOPTIONS="-c search_path=$s" q -d "$db" -c "SELECT to_regclass('$t') IS NOT NULL")" = t ]; then
        counts="$counts$t $(PGOPTIONS="-c search_path=$s" q -d "$db" -c "SELECT count(*) FROM $t")
"
      fi
    done
  done
  echo "invariants: $db $checks checks run"
  echo "counts $db $(printf '%s' "$counts" | awk 'NF {c[$1] += $2} END {for (t in c) print t "=" c[t]}' | sort | tr '\n' ' ')"
done
exit "$bad"
