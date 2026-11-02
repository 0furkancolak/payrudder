package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
)

const auditUsage = `usage: payrudderctl audit verify [--heads]
       payrudderctl audit prune --older-than 8760h [--apply]`

// auditCmd checks and trims the hash chain of the audit log (docs/audit-log-and-two-factor.md, "The hash chain"). Like secrets it does not go through the gateway: it opens
// the databases of one service with that service's own settings (PAYRUDDER_PG_SYSTEM_DSN, and PAYRUDDER_CELLS in cells mode), so it runs where the service does and
// checks one service's log at a time (plans, billing, payments-worker each keep their own).
func auditCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", auditUsage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "verify":
		fs := flag.NewFlagSet("audit verify", flag.ContinueOnError)
		heads := fs.Bool("heads", false, "print the newest entry of every chain too: keep them elsewhere, a chain that ends before a remembered head lost its tail")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		dbs, closeAll, err := auditDatabases(ctx)
		if err != nil {
			return err
		}
		defer closeAll()
		return auditVerify(ctx, os.Stdout, dbs, *heads)
	case "prune":
		fs := flag.NewFlagSet("audit prune", flag.ContinueOnError)
		older := fs.Duration("older-than", 0, "delete entries older than this, from the beginning of each chain (for example 8760h); required")
		apply := fs.Bool("apply", false, "delete; without it the command only says what it would delete")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *older <= 0 {
			return fmt.Errorf("--older-than is required (a duration such as 8760h): the log is kept forever unless this command is run\n%s", auditUsage)
		}
		dbs, closeAll, err := auditDatabases(ctx)
		if err != nil {
			return err
		}
		defer closeAll()
		return auditPrune(ctx, os.Stdout, dbs, time.Now().Add(-*older), !*apply)
	default:
		return fmt.Errorf("%s", auditUsage)
	}
}

// auditDB is one database of the service: the pooled one, or a cell.
type auditDB struct {
	name string
	pool *pgxpool.Pool
}

func auditDatabases(ctx context.Context) ([]auditDB, func(), error) {
	dsn := os.Getenv("PAYRUDDER_PG_SYSTEM_DSN")
	if dsn == "" {
		return nil, nil, fmt.Errorf("PAYRUDDER_PG_SYSTEM_DSN (the system role of the service's database) is required")
	}
	cellDSNs, err := pgxx.CellSettings{Cells: os.Getenv("PAYRUDDER_CELLS")}.Parse()
	if err != nil {
		return nil, nil, err
	}
	var dbs []auditDB
	closeAll := func() {
		for _, d := range dbs {
			d.pool.Close()
		}
	}
	pooled, err := pgxx.NewPool(ctx, dsn)
	if err != nil {
		return nil, nil, err
	}
	dbs = append(dbs, auditDB{name: "pooled", pool: pooled})
	names := make([]string, 0, len(cellDSNs))
	for name := range cellDSNs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p, err := pgxx.NewPool(ctx, cellDSNs[name].System)
		if err != nil {
			closeAll()
			return nil, nil, fmt.Errorf("cell %s: %w", name, err)
		}
		dbs = append(dbs, auditDB{name: "cell " + name, pool: p})
	}
	return dbs, closeAll, nil
}

// auditVerify prints one line for each table that holds a log and every problem it finds; it returns an error when there is one.
func auditVerify(ctx context.Context, out io.Writer, dbs []auditDB, heads bool) error {
	var problems int
	for _, db := range dbs {
		schemas, err := audit.Schemas(ctx, db.pool)
		if err != nil {
			return fmt.Errorf("%s: %w", db.name, err)
		}
		for _, schema := range schemas {
			rep, err := audit.Verify(ctx, db.pool, schema)
			if err != nil {
				return fmt.Errorf("%s, schema %s: %w", db.name, schema, err)
			}
			status := "ok"
			if !rep.OK() {
				status = fmt.Sprintf("%d problem(s)", len(rep.Problems))
			}
			fmt.Fprintf(out, "%s, schema %s: %d entries in %d chain(s), %s\n", db.name, schema, rep.Entries, rep.Chains, status)
			for _, p := range rep.Problems {
				fmt.Fprintf(out, "  %s\n", p)
			}
			if heads {
				for _, h := range rep.Heads {
					fmt.Fprintf(out, "  head %s %d %s\n", h.Chain, h.Seq, h.RowHash)
				}
			}
			problems += len(rep.Problems)
		}
	}
	if problems > 0 {
		return fmt.Errorf("the audit log does not verify: %d problem(s) listed above", problems)
	}
	return nil
}

// auditPrune deletes (or, with dry, lists) the entries before cutoff in every table that holds a log.
func auditPrune(ctx context.Context, out io.Writer, dbs []auditDB, cutoff time.Time, dry bool) error {
	verb := "deleted"
	if dry {
		verb = "would delete"
	}
	var total int64
	for _, db := range dbs {
		schemas, err := audit.Schemas(ctx, db.pool)
		if err != nil {
			return fmt.Errorf("%s: %w", db.name, err)
		}
		for _, schema := range schemas {
			res, err := audit.Prune(ctx, db.pool.Begin, schema, cutoff, dry)
			for _, r := range res {
				fmt.Fprintf(out, "%s, schema %s, chain %s: %s %d entries up to entry %d\n", db.name, schema, r.Chain, verb, r.Deleted, r.Through.Seq)
				total += r.Deleted
			}
			if err != nil {
				return fmt.Errorf("%s, schema %s: %w", db.name, schema, err)
			}
		}
	}
	if dry {
		fmt.Fprintf(out, "dry run: %d entries would go; nothing was changed (--apply deletes them)\n", total)
	} else {
		fmt.Fprintf(out, "%d entries deleted; each chain says so in an audit.pruned entry\n", total)
	}
	return nil
}
