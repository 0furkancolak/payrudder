package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/health"
)

func TestWithLiveness(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := withLiveness(api, health.New(health.NewRegistry(), 0))

	for path, want := range map[string]int{"/healthz": http.StatusOK, "/readyz": http.StatusTeapot, "/metrics": http.StatusTeapot, "/v1/plans": http.StatusTeapot} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
}
