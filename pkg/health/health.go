// Package health serves the operational HTTP endpoints of every Quotify process on its metrics port:
// /healthz (liveness), /readyz (readiness with pluggable checks) and /metrics (Prometheus).
package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// CheckFunc reports whether one dependency is ready. It must honour ctx.
type CheckFunc func(ctx context.Context) error

// Handler serves the operational endpoints.
type Handler struct {
	mu       sync.RWMutex
	checks   map[string]CheckFunc
	timeout  time.Duration
	registry *prometheus.Registry
	mux      *http.ServeMux
}

// NewRegistry returns a Prometheus registry with the Go runtime and process collectors.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return reg
}

// New returns a Handler exposing metrics from reg. Each readiness check gets checkTimeout.
func New(reg *prometheus.Registry, checkTimeout time.Duration) *Handler {
	h := &Handler{checks: map[string]CheckFunc{}, timeout: checkTimeout, registry: reg, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	h.mux.HandleFunc("GET /readyz", h.ready)
	h.mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	return h
}

// AddCheck registers a named readiness check. Registering the same name again replaces it.
func (h *Handler) AddCheck(name string, fn CheckFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[name] = fn
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

type checkResult struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	names := make([]string, 0, len(h.checks))
	for n := range h.checks {
		names = append(names, n)
	}
	checks := make(map[string]CheckFunc, len(h.checks))
	for n, fn := range h.checks {
		checks[n] = fn
	}
	h.mu.RUnlock()
	sort.Strings(names)

	results := make([]checkResult, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
			defer cancel()
			err := checks[name](ctx)
			if err == nil && ctx.Err() != nil {
				err = ctx.Err()
			}
			results[i] = checkResult{Name: name, OK: err == nil}
			if err != nil {
				results[i].Error = err.Error()
			}
		}()
	}
	wg.Wait()

	code := http.StatusOK
	for _, res := range results {
		if !res.OK {
			code = http.StatusServiceUnavailable
		}
	}
	writeJSON(w, code, map[string]any{"ready": code == http.StatusOK, "checks": results})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Serve runs the handler on addr until ctx is cancelled, then shuts down gracefully.
func Serve(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
