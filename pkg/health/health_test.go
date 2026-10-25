package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/health"
)

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthzAndMetrics(t *testing.T) {
	h := health.New(health.NewRegistry(), time.Second)
	if rec := get(t, h, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("/healthz = %d", rec.Code)
	}
	rec := get(t, h, "/metrics")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "go_goroutines") {
		t.Fatalf("/metrics = %d, body lacks go_goroutines", rec.Code)
	}
}

func TestReadyz(t *testing.T) {
	h := health.New(health.NewRegistry(), 50*time.Millisecond)
	h.AddCheck("db", func(context.Context) error { return nil })
	if rec := get(t, h, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("ready with passing checks = %d", rec.Code)
	}

	h.AddCheck("kafka", func(context.Context) error { return errors.New("no brokers") })
	h.AddCheck("slow", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	rec := get(t, h, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready with failing checks = %d", rec.Code)
	}
	var body struct {
		Ready  bool `json:"ready"`
		Checks []struct {
			Name string `json:"name"`
			OK   bool   `json:"ok"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range body.Checks {
		got[c.Name] = c.OK
	}
	if body.Ready || !got["db"] || got["kafka"] || got["slow"] {
		t.Fatalf("unexpected body %+v", body)
	}
}
