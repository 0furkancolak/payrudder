// Command mockpsp is the mock payment provider used for sandbox tenants and the local demo.
package main

import (
	"context"
	_ "embed" // config.default.yaml
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/0furkancolak/quotify/pkg/config"
	"github.com/0furkancolak/quotify/pkg/logx"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/tools/mockpsp/internal/psp"
	"github.com/0furkancolak/quotify/tools/mockpsp/migrations"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	HTTPAddr       string        `env:"MOCKPSP_HTTP_ADDR"`
	PGAppDSN       string        `env:"QUOTIFY_PG_APP_DSN,required"`
	PGMigratorDSN  string        `env:"QUOTIFY_PG_MIGRATOR_DSN"`
	MigrateOnStart bool          `env:"QUOTIFY_MIGRATE_ON_START"`
	DeclineRate    float64       `env:"MOCKPSP_DECLINE_RATE"`
	ErrorRate      float64       `env:"MOCKPSP_ERROR_RATE"`
	TimeoutRate    float64       `env:"MOCKPSP_TIMEOUT_RATE"`
	LatencyMS      int           `env:"MOCKPSP_LATENCY_MS"`
	HangFor        time.Duration `env:"MOCKPSP_HANG_FOR"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	if c.MigrateOnStart && c.PGMigratorDSN == "" {
		errs = append(errs, errors.New("QUOTIFY_PG_MIGRATOR_DSN is required with QUOTIFY_MIGRATE_ON_START"))
	}
	for name, v := range map[string]float64{"MOCKPSP_DECLINE_RATE": c.DeclineRate, "MOCKPSP_ERROR_RATE": c.ErrorRate, "MOCKPSP_TIMEOUT_RATE": c.TimeoutRate} {
		if v < 0 || v > 1 {
			errs = append(errs, fmt.Errorf("%s must be in [0,1]", name))
		}
	}
	return errors.Join(errs...)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mockpsp:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load[Config](defaults)
	if err != nil {
		return err
	}
	log, err := logx.New(logx.Options{Service: cfg.ServiceName, Version: cfg.Version, Level: cfg.LogLevel, Writer: os.Stdout})
	if err != nil {
		return err
	}
	slog.SetDefault(log)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.MigrateOnStart {
		if err := pgxx.Migrate(ctx, cfg.PGMigratorDSN, migrations.FS, log); err != nil {
			return err
		}
	}
	if cfg.MigrateOnly {
		log.Info("migrations applied; exiting (QUOTIFY_MIGRATE_ONLY)")
		return nil
	}
	pool, err := pgxx.NewPool(ctx, cfg.PGAppDSN)
	if err != nil {
		return err
	}
	defer pool.Close()
	srv := &http.Server{Addr: cfg.HTTPAddr, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 2 * time.Minute,
		Handler: psp.New(pool, psp.Config{DeclineRate: cfg.DeclineRate, ErrorRate: cfg.ErrorRate, TimeoutRate: cfg.TimeoutRate,
			Latency: time.Duration(cfg.LatencyMS) * time.Millisecond, HangFor: cfg.HangFor})}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	log.Info("mockpsp listening", "addr", cfg.HTTPAddr, "decline", cfg.DeclineRate, "error", cfg.ErrorRate, "timeout", cfg.TimeoutRate)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
