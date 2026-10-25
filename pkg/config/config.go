// Package config loads typed service configuration.
//
// Defaults come from YAML (a flat map of variable name to value) embedded in the binary: this package's base.default.yaml and the service's own
// config.default.yaml. An environment variable of the same name overrides them, and secrets (DSNs, tokens, keys) exist only in the environment: CheckDefaults
// rejects a YAML file that names one. Every service embeds Base in its own config struct and calls Load. Validation runs after parsing, so a service never
// starts with a half-valid configuration.
package config

import (
	_ "embed" // base.default.yaml
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/caarlos0/env/v11"
	"gopkg.in/yaml.v3"

	"github.com/0furkancolak/payrudder/pkg/brand"
	"github.com/0furkancolak/payrudder/pkg/memlimit"
)

//go:embed base.default.yaml
var baseDefaults []byte

// Environment names the deployment environment of a process (not a tenant's sandbox/live environment).
type Environment string

const (
	EnvLocal Environment = "local"
	EnvCI    Environment = "ci"
	EnvKind  Environment = "kind"
	// EnvRailway is a deployment on Railway (deploy/railway).
	EnvRailway Environment = "railway"
)

// Base holds settings shared by every Payrudder process.
type Base struct {
	Env          Environment `env:"PAYRUDDER_ENV"`
	ServiceName  string      `env:"PAYRUDDER_SERVICE_NAME,required"`
	Version      string      `env:"PAYRUDDER_VERSION"`
	LogLevel     string      `env:"PAYRUDDER_LOG_LEVEL"`
	MetricsAddr  string      `env:"PAYRUDDER_METRICS_ADDR"`
	OTLPEndpoint string      `env:"PAYRUDDER_OTLP_ENDPOINT"` // empty disables trace export
	// MigrateOnly makes a service that migrates on start (PAYRUDDER_MIGRATE_ON_START) exit once the migrations are applied.
	// Kubernetes runs it as a pre-install/pre-upgrade Job with the service's own image.
	MigrateOnly bool `env:"PAYRUDDER_MIGRATE_ONLY"`
	// BrandName is the product name people see (hosted pages, webhook headers, the console). See pkg/brand.
	BrandName string `env:"BRAND_NAME"`
	// Demo turns on demo mode: only the mock provider is used, mail goes to the local catcher and the console says so. Never next to real provider accounts.
	Demo bool `env:"PAYRUDDER_DEMO"`
}

// Validate checks the shared settings.
func (b Base) Validate() error {
	var errs []error
	switch b.Env {
	case EnvLocal, EnvCI, EnvKind, EnvRailway:
	default:
		errs = append(errs, fmt.Errorf("PAYRUDDER_ENV: unknown environment %q", b.Env))
	}
	switch b.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("PAYRUDDER_LOG_LEVEL: unknown level %q", b.LogLevel))
	}
	return errors.Join(errs...)
}

// Apply puts the settings that other packages read into effect; Load calls it after Validate.
func (b Base) Apply() {
	brand.Set(b.BrandName)
	memlimit.Apply()
}

type applier interface{ Apply() }

// Validator is implemented by config structs that need checks beyond parsing.
type Validator interface {
	Validate() error
}

// Load parses the defaults (the base file, then each of defaults in order) and the process environment into T and validates the result.
// If T (or a pointer to it) implements Validator, Validate is called.
func Load[T any](defaults ...[]byte) (T, error) {
	return LoadFrom[T](nil, defaults...)
}

// LoadFrom is Load with an explicit environment map, which replaces the process environment (for tests). A nil map reads the process environment.
func LoadFrom[T any](environ map[string]string, defaults ...[]byte) (T, error) {
	var cfg T
	merged, err := layers(defaults)
	if err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	if environ == nil {
		environ = processEnv()
	}
	environ, legacy := UpgradeLegacy(environ)
	if len(legacy) > 0 {
		slog.Warn("deprecated environment variables: rename the PAYRUDDER_ prefix to PAYRUDDER_ (the old names stop working in a later release)", "names", legacy)
	}
	for k, v := range environ {
		merged[k] = v
	}
	if err := applyPort[T](merged, environ); err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	if err := env.ParseWithOptions(&cfg, env.Options{Environment: merged}); err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	if v, ok := any(&cfg).(Validator); ok {
		if err := v.Validate(); err != nil {
			return cfg, fmt.Errorf("config: %w", err)
		}
	}
	if a, ok := any(&cfg).(applier); ok {
		a.Apply()
	}
	return cfg, nil
}

// portTargets are the settings PORT can fill, in order of preference: the address of a service's public HTTP API, else its operational address.
var portTargets = []string{"PAYRUDDER_HTTP_ADDR", "MOCKPSP_HTTP_ADDR", "PAYRUDDER_METRICS_ADDR"}

// applyPort honours the PORT variable that platforms such as Railway inject: the platform's health check and public domain reach a process only on that
// port. PORT fills the first of portTargets that T has, unless the environment sets that variable itself, so the YAML and environment contract is unchanged
// where PORT is absent. A service with an HTTP API listens there; every other service serves /healthz, /readyz and /metrics there (its gRPC port stays fixed).
func applyPort[T any](merged, environ map[string]string) error {
	port := environ["PORT"]
	if port == "" {
		return nil
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("PORT: %q is not a port number", port)
	}
	names := map[string]bool{}
	collectTags(reflect.TypeFor[T](), names, map[string]bool{})
	for _, target := range portTargets {
		if !names[target] {
			continue
		}
		if _, set := environ[target]; !set {
			merged[target] = ":" + port
		}
		return nil
	}
	return nil
}

const (
	prefix = "PAYRUDDER_"
	// legacyPrefix is the deprecated spelling of every setting, from before the product was renamed (ADR 0022). It still works and logs a warning.
	legacyPrefix = "PAYRUDDER_"
)

// UpgradeLegacy returns environ with every deprecated PAYRUDDER_X setting also under PAYRUDDER_X, unless PAYRUDDER_X is set: the new name wins when both are
// present. It also returns the deprecated names it found, sorted. The input map is not changed.
func UpgradeLegacy(environ map[string]string) (map[string]string, []string) {
	out := make(map[string]string, len(environ))
	for k, v := range environ {
		out[k] = v
	}
	var legacy []string
	for k, v := range environ {
		rest, ok := strings.CutPrefix(k, legacyPrefix)
		if !ok {
			continue
		}
		legacy = append(legacy, k)
		if _, set := environ[prefix+rest]; !set {
			out[prefix+rest] = v
		}
	}
	sort.Strings(legacy)
	return out, legacy
}

// Getenv reads a setting by its PAYRUDDER_ name and falls back to the deprecated PAYRUDDER_ spelling, with a warning. For tools that read the environment
// without Load.
func Getenv(name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	if rest, ok := strings.CutPrefix(name, prefix); ok {
		if v := os.Getenv(legacyPrefix + rest); v != "" {
			slog.Warn("deprecated environment variable: use the new name", "deprecated", legacyPrefix+rest, "use", name)
			return v
		}
	}
	return ""
}

func processEnv() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// layers merges the base defaults and then each given file; a later file wins.
func layers(files [][]byte) (map[string]string, error) {
	out := map[string]string{}
	for i, f := range append([][]byte{baseDefaults}, files...) {
		var m map[string]any
		if err := yaml.Unmarshal(f, &m); err != nil {
			return nil, fmt.Errorf("defaults file %d: %w", i, err)
		}
		for k, v := range m {
			out[k] = fmt.Sprint(v)
		}
	}
	return out, nil
}

var (
	secretName  = regexp.MustCompile(`(DSN|TOKEN|SECRET|PASSWORD|_KEY$)`)
	withSecrets = regexp.MustCompile(`://[^/@\s]*:[^/@\s]*@`)
)

// CheckDefaults is for a service's test: every name in the defaults files must be an `env` tag of T (a typo would otherwise be ignored), none may look like a
// secret, and a required setting must not have a default.
func CheckDefaults[T any](defaults ...[]byte) error {
	names, required := map[string]bool{}, map[string]bool{}
	collectTags(reflect.TypeFor[T](), names, required)
	merged, err := layers(defaults)
	if err != nil {
		return err
	}
	var errs []error
	for k := range merged {
		switch {
		case !names[k]:
			errs = append(errs, fmt.Errorf("%s is in the defaults but is not a setting of this service", k))
		case secretName.MatchString(k) || withSecrets.MatchString(merged[k]):
			errs = append(errs, fmt.Errorf("%s looks like a secret or carries credentials: it belongs in the environment, not in YAML", k))
		case required[k]:
			errs = append(errs, fmt.Errorf("%s is required and must not have a default", k))
		}
	}
	return errors.Join(errs...)
}

func collectTags(t reflect.Type, names, required map[string]bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous || f.Type.Kind() == reflect.Struct {
			collectTags(f.Type, names, required)
		}
		tag, ok := f.Tag.Lookup("env")
		if !ok {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		names[name] = true
		if strings.Contains(opts, "required") {
			required[name] = true
		}
	}
}
