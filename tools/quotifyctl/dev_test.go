package main

import (
	"slices"
	"testing"
)

func TestToHost(t *testing.T) {
	published := map[string]string{"postgres:5432": "localhost:5432", "redpanda:9092": "localhost:19092", "plans-svc:7001": "localhost:7001", "minio:9000": "localhost:9001"}
	for in, want := range map[string]string{
		"postgres://plans_app:plans_app_local@postgres:5432/plans_db": "postgres://plans_app:plans_app_local@localhost:5432/plans_db",
		"redpanda:9092":                               "localhost:19092",
		"dns:///plans-svc:7001":                       "localhost:7001",
		"http://minio:9000":                           "http://localhost:9001",
		"amqp://quotify:quotify_local@rabbitmq:5672/": "amqp://quotify:quotify_local@rabbitmq:5672/", // not published: left as it is
		":7001":    ":7001",
		"1m,3m,7m": "1m,3m,7m",
	} {
		if got := toHost(in, published); got != want {
			t.Errorf("toHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostServices(t *testing.T) {
	var cf composeFile
	cf.Services = map[string]composeService{
		"redpanda": {Ports: []struct {
			Target    int    `json:"target"`
			Published string `json:"published"`
		}{{19092, "19092"}}},
		"postgres": {Ports: []struct {
			Target    int    `json:"target"`
			Published string `json:"published"`
		}{{5432, "5432"}}},
		"gateway": {Build: &struct{ Dockerfile string }{"services/gateway/Dockerfile"}, Environment: map[string]string{"QUOTIFY_PLANS_ADDR": "dns:///plans-svc:7001", "QUOTIFY_OTLP_ENDPOINT": "otel-collector:4317", "QUOTIFY_KAFKA_BROKERS": "redpanda:9092"}},
		"plans-svc": {Build: &struct{ Dockerfile string }{"services/plans-svc/Dockerfile"}, Environment: map[string]string{"QUOTIFY_PG_APP_DSN": "postgres://u:p@postgres:5432/db"},
			Ports: []struct {
				Target    int    `json:"target"`
				Published string `json:"published"`
			}{{7001, "7001"}}},
		"mockpsp": {Build: &struct{ Dockerfile string }{"tools/mockpsp/Dockerfile"}},
	}
	got := hostServices(cf)
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"plans-svc", "mockpsp", "gateway"}) {
		t.Fatalf("services and order = %v; postgres is infrastructure and plans-svc starts first", names)
	}
	gw := got[2]
	if gw.Env["QUOTIFY_PLANS_ADDR"] != "localhost:7001" || gw.Env["QUOTIFY_OTLP_ENDPOINT"] != "" || gw.Main != "./cmd/gateway" || gw.Dir != "services/gateway" {
		t.Errorf("gateway = %+v", gw)
	}
	if gw.Env["QUOTIFY_KAFKA_BROKERS"] != "localhost:19092" {
		t.Errorf("kafka = %q; the host reaches Redpanda on its external listener", gw.Env["QUOTIFY_KAFKA_BROKERS"])
	}
	if got[0].Env["QUOTIFY_PG_APP_DSN"] != "postgres://u:p@localhost:5432/db" {
		t.Errorf("plans-svc env = %v", got[0].Env)
	}
	if only := filterServices(got, []string{"gateway"}, nil); len(only) != 1 {
		t.Errorf("only: %v", only)
	}
	if skipped := filterServices(got, nil, []string{"gateway"}); len(skipped) != 2 {
		t.Errorf("skip: %v", skipped)
	}
}
