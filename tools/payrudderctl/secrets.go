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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/secretbox/reseal"
)

// secretsCmd is step 3 of replacing the secrets key (docs/runbooks/secrets-key-rotation.md). Like cells it does not go through the gateway: it opens the databases
// of one service with that service's own settings, so it runs where the service does (a job, a bastion), with the environment of the service:
//
//	payrudderctl secrets reseal --service plans|payments [--dry-run] [--batch 200]
//
// PAYRUDDER_PG_SYSTEM_DSN and PAYRUDDER_CELLS (cells mode) say where the boxes are, PAYRUDDER_SECRETS_KEY, _ID, _PREVIOUS and _PREVIOUS_ID which keys open them and which
// seals.
func secretsCmd(args []string) error {
	if len(args) == 0 || args[0] != "reseal" {
		return fmt.Errorf("usage: payrudderctl secrets reseal --service plans|payments [--dry-run] [--batch N]")
	}
	fs := flag.NewFlagSet("secrets reseal", flag.ContinueOnError)
	service := fs.String("service", "", "plans | payments: the service whose boxes are sealed again (run it once for each, with that service's environment)")
	dry := fs.Bool("dry-run", false, "open every box that would be sealed again and change nothing")
	batch := fs.Int("batch", 200, "boxes read at a time")
	if err := fs.Parse(args[1:]); err != nil {
		return err
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
	settings, err := secretSettings(os.Getenv)
	if err != nil {
		return err
	}
	box, err := settings.SecretBox()
	if err != nil {
		return err
	}
	if box == nil {
		return fmt.Errorf("PAYRUDDER_SECRETS_KEY is required")
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

	reports, err := reseal.Run(ctx, reseal.Input{Box: box, Columns: cols, Pooled: pooled, Cells: cells, Batch: *batch, DryRun: *dry,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))})
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
