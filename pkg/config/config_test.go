package config_test

import (
	"strings"
	"testing"

	"github.com/0furkancolak/quotify/pkg/config"
)

type svcConfig struct {
	config.Base
	GRPCAddr string `env:"QUOTIFY_GRPC_ADDR" envDefault:":7000"`
}

func (c *svcConfig) Validate() error { return c.Base.Validate() }

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.LoadFrom[svcConfig](map[string]string{"QUOTIFY_SERVICE_NAME": "hello"})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.ServiceName != "hello" || cfg.Env != config.EnvLocal || cfg.GRPCAddr != ":7000" || cfg.MetricsAddr != ":9100" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		environ map[string]string
		wantErr string
	}{
		{"missing required", map[string]string{}, "QUOTIFY_SERVICE_NAME"},
		{"unknown env", map[string]string{"QUOTIFY_SERVICE_NAME": "x", "QUOTIFY_ENV": "prod"}, "unknown environment"},
		{"unknown log level", map[string]string{"QUOTIFY_SERVICE_NAME": "x", "QUOTIFY_LOG_LEVEL": "loud"}, "unknown level"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.LoadFrom[svcConfig](tt.environ)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

type httpConfig struct {
	config.Base
	HTTPAddr string `env:"QUOTIFY_HTTP_ADDR" envDefault:":8000"`
}

func TestPort(t *testing.T) {
	with := func(kv ...string) map[string]string {
		m := map[string]string{"QUOTIFY_SERVICE_NAME": "x"}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}

	t.Run("a service without an HTTP API serves its operational endpoints on PORT", func(t *testing.T) {
		cfg, err := config.LoadFrom[svcConfig](with("PORT", "4321"))
		if err != nil || cfg.MetricsAddr != ":4321" || cfg.GRPCAddr != ":7000" {
			t.Fatalf("got %+v, %v", cfg, err)
		}
	})
	t.Run("a service with an HTTP API listens on PORT and keeps its metrics address", func(t *testing.T) {
		cfg, err := config.LoadFrom[httpConfig](with("PORT", "4321"))
		if err != nil || cfg.HTTPAddr != ":4321" || cfg.MetricsAddr != ":9100" {
			t.Fatalf("got %+v, %v", cfg, err)
		}
	})
	t.Run("an explicit address wins", func(t *testing.T) {
		cfg, err := config.LoadFrom[httpConfig](with("PORT", "4321", "QUOTIFY_HTTP_ADDR", ":9999"))
		if err != nil || cfg.HTTPAddr != ":9999" || cfg.MetricsAddr != ":9100" {
			t.Fatalf("got %+v, %v", cfg, err)
		}
	})
	t.Run("without PORT nothing changes", func(t *testing.T) {
		cfg, err := config.LoadFrom[httpConfig](with())
		if err != nil || cfg.HTTPAddr != ":8000" || cfg.MetricsAddr != ":9100" {
			t.Fatalf("got %+v, %v", cfg, err)
		}
	})
	t.Run("a bad PORT is an error", func(t *testing.T) {
		if _, err := config.LoadFrom[svcConfig](with("PORT", "http")); err == nil || !strings.Contains(err.Error(), "PORT") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("railway is a known environment", func(t *testing.T) {
		if _, err := config.LoadFrom[svcConfig](with("QUOTIFY_ENV", "railway")); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPayrudderPrefixWins(t *testing.T) {
	cfg, err := config.LoadFrom[httpConfig](map[string]string{
		"QUOTIFY_SERVICE_NAME": "old", "PAYRUDDER_SERVICE_NAME": "new",
		"QUOTIFY_LOG_LEVEL":   "debug",
		"PAYRUDDER_HTTP_ADDR": ":9999",
	})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.ServiceName != "new" || cfg.LogLevel != "debug" || cfg.HTTPAddr != ":9999" {
		t.Fatalf("PAYRUDDER_ must win and QUOTIFY_ must still work alone: %+v", cfg)
	}
	// PORT does not override a PAYRUDDER_ address either.
	cfg, err = config.LoadFrom[httpConfig](map[string]string{"PAYRUDDER_SERVICE_NAME": "x", "PAYRUDDER_HTTP_ADDR": ":9999", "PORT": "8080"})
	if err != nil || cfg.HTTPAddr != ":9999" {
		t.Fatalf("PORT overrode the explicit address: %+v %v", cfg, err)
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("QUOTIFY_X_TEST", "old")
	if config.Getenv("QUOTIFY_X_TEST") != "old" {
		t.Fatal("QUOTIFY_ alone must work")
	}
	t.Setenv("PAYRUDDER_X_TEST", "new")
	if config.Getenv("QUOTIFY_X_TEST") != "new" {
		t.Fatal("PAYRUDDER_ must win")
	}
}
