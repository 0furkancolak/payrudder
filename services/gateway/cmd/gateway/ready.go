package main

import (
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// grpcCodeIsUp reports whether an error still proves plans-svc answered (a NotFound for the probe prefix is healthy).
func grpcCodeIsUp(err error) bool {
	switch status.Code(err) {
	case codes.NotFound, codes.InvalidArgument:
		return true
	default:
		return false
	}
}

// withLiveness serves GET /healthz on the public listener and everything else from api. A platform health check (Railway's) can only probe the port that
// receives public traffic, and the operational port (health.Serve) must not be public: it serves /metrics. Only liveness is exposed here, never readiness.
func withLiveness(api, ops http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", ops)
	mux.Handle("/", api)
	return mux
}
