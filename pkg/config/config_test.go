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
