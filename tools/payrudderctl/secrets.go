package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/secretbox/reseal"
)

// secretsCmd is step 3 of replacing the secrets key (docs/runbooks/secrets-key-rotation.md). Like cells it does not go through the gateway: it opens the databases
// of one service with that service's own settings, so it runs where the service does (a job, a bastion), with the environment of the service:
//
//	payrudderctl secrets reseal --service plans|payments [--dry-run] [--verify] [--batch 200]
//	payrudderctl secrets accounts-backfill [--dry-run]
//	payrudderctl secrets account-ids [--dry-run] [--verify] [--batch 64] [--pause 100ms]
//
// --verify opens every box of the service, whatever key sealed it, and changes nothing. accounts-backfill is step 2 of the account_id migration (ADR 0026, payments only):
// it copies every old account into the new tables, owned by its tenant, sealed again to the new account's id.
//
// account-ids is step 4: it fills account_id on the ledger and its neighbours from the key, page range by page range (--batch pages, --pause between statements), and can be
// stopped and run again; --verify changes nothing and exits non-zero while a key maps to two ids or a row's id is not the account its key names. It needs no secrets key.
//
// PAYRUDDER_PG_SYSTEM_DSN and PAYRUDDER_CELLS (cells mode) say where the boxes are, PAYRUDDER_SECRETS_KEY, _ID, _PREVIOUS and _PREVIOUS_ID which keys open them and which
// seals.
func secretsCmd(args []string) error {
	if len(args) == 0 || (args[0] != "reseal" && args[0] != "accounts-backfill" && args[0] != "account-ids") {
		return fmt.Errorf("usage: payrudderctl secrets reseal --service plans|payments [--dry-run] [--verify] [--batch N] | payrudderctl secrets accounts-backfill [--dry-run] | payrudderctl secrets account-ids [--dry-run] [--verify] [--batch N] [--pause D]")
	}
	backfill, ids := args[0] == "accounts-backfill", args[0] == "account-ids"
	fs := flag.NewFlagSet("secrets "+args[0], flag.ContinueOnError)
	service := fs.String("service", "", "plans | payments: the service whose boxes are sealed again (run it once for each, with that service's environment)")
	dry := fs.Bool("dry-run", false, "open every box that would be sealed again and change nothing")
	batch := fs.Int("batch", 200, "boxes read at a time")
	pause := fs.Duration("pause", 100*time.Millisecond, "account-ids: the wait between two statements")
	verify := fs.Bool("verify", false, "open every box of the service, whatever key sealed it, and change nothing")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if backfill || ids {
		*service = "payments"
	}
	var cols []reseal.Column
	switch *service {
	case "plans":
		cols = reseal.Plans
	case "payments":
		cols = reseal.Payments
	default:
		return fmt.Errorf("--service must be plans or payments")
	}
	var box *secretbox.Box
	if !ids {
		settings, err := secretSettings(os.Getenv)
		if err != nil {
			return err
		}
		if box, err = settings.SecretBox(); err != nil {
			return err
		}
		if box == nil {
			return fmt.Errorf("PAYRUDDER_SECRETS_KEY is required")
		}
	}
	dsn := os.Getenv("PAYRUDDER_PG_SYSTEM_DSN")
	if dsn == "" {
		return fmt.Errorf("PAYRUDDER_PG_SYSTEM_DSN (the system role of the service's database) is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pooled, err := pgxx.NewPool(ctx, dsn)
	if err != nil {
		return err
	}
	defer pooled.Close()
	cellDSNs, err := pgxx.CellSettings{Cells: os.Getenv("PAYRUDDER_CELLS")}.Parse()
	if err != nil {
		return err
	}
	cells := map[string]*pgxpool.Pool{}
	for name, d := range cellDSNs {
		if cells[name], err = pgxx.NewPool(ctx, d.System); err != nil {
			return fmt.Errorf("cell %s: %w", name, err)
		}
		defer cells[name].Close()
	}

	in := reseal.Input{Box: box, Columns: cols, Pooled: pooled, Cells: cells, Batch: *batch, DryRun: *dry, Verify: *verify,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))}
	if ids {
		return accountIDs(ctx, in, *dry, *verify, *batch, *pause)
	}
	if backfill {
		r, err := reseal.BackfillAccounts(ctx, in)
		fmt.Fprintf(os.Stdout, "old accounts %d: created %d, refreshed %d, unchanged %d; bindings written %d; do not open %d; new accounts with no old row %d (dry run: %v)\n",
			r.Old, r.Created, r.Refreshed, r.Unchanged, r.Bindings, r.Unreadable, r.Orphans, *dry)
		if err == nil && r.Unreadable > 0 {
			err = fmt.Errorf("%d old account(s) do not open with the keys given and were skipped", r.Unreadable)
		}
		return err
	}
	reports, err := reseal.Run(ctx, in)
	printReports(reports, *dry)
	if err != nil {
		return err
	}
	var unreadable, behind int
	for _, r := range reports {
		unreadable += r.Unreadable
		behind += r.Boxes - r.Current
	}
	switch {
	case *verify:
		if unreadable > 0 {
			return fmt.Errorf("%d box(es) do not open with the keys given (listed above)", unreadable)
		}
		fmt.Fprintln(os.Stdout, "verify: every box opens")
	case unreadable > 0:
		return fmt.Errorf("%d box(es) do not open with the keys given (listed above): the previous key must stay until they are dealt with", unreadable)
	case *dry:
		fmt.Fprintf(os.Stdout, "dry run: nothing was changed; every box that is not on key %d opens\n", box.ActiveID())
	case behind > 0:
		return fmt.Errorf("%d box(es) are not on key %d (saved by a service that still seals with another key, or while the job ran): roll the new key out to every replica and run it again", behind, box.ActiveID())
	default:
		fmt.Fprintf(os.Stdout, "every box is sealed with key %d: the previous key can be dropped (runbook, step 4)\n", box.ActiveID())
	}
	return nil
}

// secretSettings reads the four variables of secretbox.Settings; the library parses a service's environment with its own tool, which this one does not carry.
func secretSettings(get func(string) string) (secretbox.Settings, error) {
	s := secretbox.Settings{SecretsKey: get("PAYRUDDER_SECRETS_KEY"), SecretsKeyPrevious: get("PAYRUDDER_SECRETS_KEY_PREVIOUS")}
	for name, dst := range map[string]*int{"PAYRUDDER_SECRETS_KEY_ID": &s.SecretsKeyID, "PAYRUDDER_SECRETS_KEY_PREVIOUS_ID": &s.SecretsKeyPreviousID} {
		if v := get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return s, fmt.Errorf("%s must be a number: %w", name, err)
			}
			*dst = n
		}
	}
	return s, nil
}

func printReports(reports []reseal.Report, dry bool) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	sealed := "resealed"
	if dry {
		sealed = "would reseal"
	}
	fmt.Fprintf(tw, "column\tboxes\ton the key\t%s\tchanged meanwhile\tdo not open\n", sealed)
	for _, r := range reports {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\n", r.Column, r.Boxes, r.Current, r.Resealed, r.Changed, r.Unreadable)
	}
	_ = tw.Flush()
}

// accountIDs runs step 4 (see the command's comment) and prints one line per table.
func accountIDs(ctx context.Context, in reseal.Input, dry, verify bool, batch int, pause time.Duration) error {
	var reports []reseal.IDReport
	var err error
	switch {
	case verify:
		reports, err = reseal.VerifyAccountIDs(ctx, in)
	default:
		in.Batch = batch
		reports, err = reseal.BackfillAccountIDs(ctx, in, reseal.IDOptions{Batch: batch, Pause: pause, DryRun: dry})
	}
	var bad, pending int
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "table\trows\tfilled\tpending\tno account\tno provider\tkey with two ids\tid not the key's account")
	for _, r := range reports {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n", r.Table, r.Rows, r.Filled, r.Pending, r.NoAccount, r.Keyless, r.Conflicts, r.Mismatched)
		bad += r.Conflicts + r.Mismatched
		pending += r.Pending
	}
	tw.Flush()
	switch {
	case err != nil:
		return err
	case bad > 0:
		return fmt.Errorf("%d key(s) or row(s) carry an id that is not the account the key names: do not go to step 5", bad)
	case verify && pending > 0:
		return fmt.Errorf("%d row(s) with an account have no id yet: run account-ids again", pending)
	case verify:
		fmt.Fprintln(os.Stdout, "verify: every row whose key has an account carries its id; rows with no account are listed above")
	case dry:
		fmt.Fprintln(os.Stdout, "dry run: nothing was changed (filled = rows that would be)")
	}
	return nil
}
