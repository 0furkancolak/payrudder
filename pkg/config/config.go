// Package config loads typed service configuration from environment variables.
//
// Every service embeds Base in its own config struct and calls Load. Validation runs after parsing, so a
// service never starts with a half-valid configuration.
package config

import (
	"errors"
	"fmt"

	"github.com/caarlos0/env/v11"
)

// Environment names the deployment environment of a process (not a tenant's sandbox/live environment).
type Environment string

const (
	EnvLocal Environment = "local"
	EnvCI    Environment = "ci"
	EnvKind  Environment = "kind"
)

// Base holds settings shared by every Quotify process.
type Base struct {
	Env          Environment `env:"QUOTIFY_ENV" envDefault:"local"`
	ServiceName  string      `env:"QUOTIFY_SERVICE_NAME,required"`
	Version      string      `env:"QUOTIFY_VERSION" envDefault:"dev"`
	LogLevel     string      `env:"QUOTIFY_LOG_LEVEL" envDefault:"info"`
	MetricsAddr  string      `env:"QUOTIFY_METRICS_ADDR" envDefault:":9100"`
	OTLPEndpoint string      `env:"QUOTIFY_OTLP_ENDPOINT"` // empty disables trace export
	// MigrateOnly makes a service that migrates on start (QUOTIFY_MIGRATE_ON_START) exit once the migrations are applied.
	// Kubernetes runs it as a pre-install/pre-upgrade Job with the service's own image.
	MigrateOnly bool `env:"QUOTIFY_MIGRATE_ONLY" envDefault:"false"`
}

// Validate checks the shared settings.
func (b Base) Validate() error {
	var errs []error
	switch b.Env {
	case EnvLocal, EnvCI, EnvKind:
	default:
		errs = append(errs, fmt.Errorf("QUOTIFY_ENV: unknown environment %q", b.Env))
	}
	switch b.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("QUOTIFY_LOG_LEVEL: unknown level %q", b.LogLevel))
	}
	return errors.Join(errs...)
}

// Validator is implemented by config structs that need checks beyond parsing.
type Validator interface {
	Validate() error
}

// Load parses environment variables into T and validates the result.
// If T (or a pointer to it) implements Validator, Validate is called.
func Load[T any]() (T, error) {
	return LoadFrom[T](nil)
}

// LoadFrom is Load with an explicit environment map. A nil map reads the process environment.
func LoadFrom[T any](environ map[string]string) (T, error) {
	var cfg T
	opts := env.Options{}
	if environ != nil {
		opts.Environment = environ
	}
	if err := env.ParseWithOptions(&cfg, opts); err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	if v, ok := any(&cfg).(Validator); ok {
		if err := v.Validate(); err != nil {
			return cfg, fmt.Errorf("config: %w", err)
		}
	}
	return cfg, nil
}
