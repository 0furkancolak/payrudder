package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/cells"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
)

// cellsCmd is the operator's side of cells mode (ADR 0018, docs/runbooks/cells.md). Like dlq it does not go through the gateway: it reaches plans-svc's directory
// over gRPC and the Postgres databases of the pooled deployment and of each cell with their owner roles, so it runs inside the network (a Kubernetes job, a
// bastion), with a config file that says where everything is (pkg/cells.Config).
//
//	payrudderctl cells jobs
//	payrudderctl cells doctor  --config cells.json
//	payrudderctl cells migrate --config cells.json
//	payrudderctl cells move    --config cells.json --tenant ID --to c1|pooled [--settle 60s]
//	payrudderctl cells abort   --config cells.json --tenant ID --to c1|pooled
//	payrudderctl cells retired --config cells.json [--drop NAME --service S --cell C]
func cellsCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: payrudderctl cells jobs|doctor|migrate|move|abort|retired [flags]")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("cells "+sub, flag.ContinueOnError)
	config := fs.String("config", envOr("PAYRUDDER_CELLS_CONFIG", ""), "cells config file (PAYRUDDER_CELLS_CONFIG)")
	tenantFlag := fs.String("tenant", "", "tenant id (move, abort)")
	to := fs.String("to", "", `where the tenant goes: a cell name, or "pooled" (move, abort)`)
	settle := fs.Duration("settle", 60*time.Second, "move: wait after fencing the tenant in the directory so cached answers expire")
	fenceWait := fs.Duration("fence-wait", 30*time.Second, "move: how long to wait for transactions in the source schema")
	drop := fs.String("drop", "", "retired: drop this schema set aside by a move (needs --service and --cell)")
	service := fs.String("service", "", "retired: the service of the schema to drop")
	cell := fs.String("cell", "", "retired: the cell of the schema to drop")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if sub == "jobs" {
		return cellsJobs()
	}
	if *config == "" {
		return fmt.Errorf("--config (or PAYRUDDER_CELLS_CONFIG) is required")
	}
	cfg, err := cells.Load(*config)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	switch sub {
	case "migrate":
		return cellsMigrate(ctx, cfg, log)
	case "retired":
		return cellsRetired(ctx, cfg, *drop, *service, *cell)
	}

	conn, err := grpc.NewClient(cfg.PlansAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("plans-svc %s: %w", cfg.PlansAddr, err)
	}
	defer func() { _ = conn.Close() }()
	dir := cells.NewGRPC(plansv1.NewDirectoryServiceClient(conn))

	switch sub {
	case "doctor":
		return cellsDoctor(ctx, cfg, dir)
	case "move", "abort":
		id, err := uuid.Parse(*tenantFlag)
		if err != nil {
			return fmt.Errorf("--tenant must be a tenant id: %w", err)
		}
		dest := *to
		switch dest {
		case "pooled":
			dest = ""
		case "":
			return fmt.Errorf(`--to is required: a cell name, or "pooled"`)
		}
		o := cells.MoveOptions{Settle: *settle, FenceWait: *fenceWait, Log: log}
		if sub == "abort" {
			if err := cells.AbortTenantMove(ctx, cfg, dir, id, dest, o); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "move of %s given up: the source is open, the destination is discarded, the tenant serves from where it was\n", id)
			return nil
		}
		rep, err := cells.MoveTenant(ctx, cfg, dir, id, dest, o)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "moved %s from %s to %s\n", id, placeName(rep.From), placeName(rep.To))
		for _, name := range cfg.ServiceNames() {
			c := rep.Copied[name]
			fmt.Fprintf(os.Stdout, "  %-12s %d rows in %d tables; source %s\n", name, c.Rows(), len(c.Tables), retiredName(rep.Retired[name], rep.From))
		}
		for _, p := range rep.Problems {
			fmt.Fprintln(os.Stderr, "problem after the move:", p)
		}
		if len(rep.Problems) > 0 {
			return fmt.Errorf("the move is done but %d step(s) after it failed: see above, then run doctor", len(rep.Problems))
		}
		return nil
	}
	return fmt.Errorf("unknown cells command %q", sub)
}

func placeName(cell string) string {
	if cell == "" {
		return "the pooled database"
	}
	return "cell " + cell
}

func retiredName(name, from string) string {
	switch {
	case name != "":
		return "set aside as " + name
	case from == "":
		return "rows deleted from the pooled database"
	}
	return "retired"
}

func cellsJobs() error {
	left := tenantdir.Unconverted()
	for _, j := range tenantdir.Jobs {
		state := "converted"
		if !j.Converted {
			state = "NOT converted"
		}
		fmt.Fprintf(os.Stdout, "%-14s %-48s %-14s %s\n", j.Service, j.Name, state, j.Where)
	}
	if err := tenantdir.CellsReady(); err != nil {
		fmt.Fprintf(os.Stdout, "\n%d jobs left; the directory refuses to place a tenant in a cell: %v\n", len(left), err)
		return nil
	}
	fmt.Fprintln(os.Stdout, "\nevery job is converted: tenants can be placed in cells")
	return nil
}

func cellsDoctor(ctx context.Context, cfg cells.Config, dir cells.Directory) error {
	findings, err := cells.Doctor(ctx, cfg, dir, cells.DoctorOptions{})
	if err != nil {
		return err
	}
	var errors int
	for _, f := range findings {
		fmt.Fprintln(os.Stdout, f)
		if f.Level == cells.Error {
			errors++
		}
	}
	if len(findings) == 0 {
		fmt.Fprintln(os.Stdout, "nothing to report")
	}
	if errors > 0 {
		return fmt.Errorf("%d error(s)", errors)
	}
	return nil
}

func cellsMigrate(ctx context.Context, cfg cells.Config, log *slog.Logger) error {
	var failed []string
	for _, service := range cfg.ServiceNames() {
		schemas, err := cfg.Schemas(service)
		if err != nil {
			return err
		}
		for _, cell := range cfg.CellNames() {
			ep, err := cfg.Endpoint(service, cell)
			if err != nil {
				return err
			}
			if err := pgxx.EnsureShared(ctx, ep.MigratorDSN, schemas.Roles); err != nil {
				failed = append(failed, fmt.Sprintf("%s@%s: %v", service, cell, err))
				continue
			}
			rep, err := pgxx.MigrateTenants(ctx, ep.MigratorDSN, schemas, log)
			fmt.Fprintf(os.Stdout, "%s@%s: %d tenant schemas, %d migrated, %d failed\n", service, cell, rep.Schemas, len(rep.Applied), len(rep.Failed))
			if err != nil {
				failed = append(failed, fmt.Sprintf("%s@%s: %v", service, cell, err))
			}
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s", strings.Join(failed, "\n"))
	}
	return nil
}

func cellsRetired(ctx context.Context, cfg cells.Config, drop, service, cell string) error {
	if drop != "" {
		if service == "" || cell == "" {
			return fmt.Errorf("--drop needs --service and --cell")
		}
		ep, err := cfg.Endpoint(service, cell)
		if err != nil {
			return err
		}
		if err := pgxx.DropRetired(ctx, ep.MigratorDSN, drop); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "dropped %s from %s@%s\n", drop, service, cell)
		return nil
	}
	for _, service := range cfg.ServiceNames() {
		for _, cell := range cfg.CellNames() {
			ep, err := cfg.Endpoint(service, cell)
			if err != nil {
				return err
			}
			conn, err := pgx.Connect(ctx, ep.MigratorDSN)
			if err != nil {
				return fmt.Errorf("%s@%s: %w", service, cell, err)
			}
			set, err := pgxx.RetiredSchemas(ctx, conn)
			_ = conn.Close(ctx)
			if err != nil {
				return err
			}
			for _, r := range set {
				fmt.Fprintf(os.Stdout, "%s@%s %s tenant %s set aside %s\n", service, cell, r.Name, r.TenantID, r.At.Format(time.RFC3339))
			}
		}
	}
	return nil
}
