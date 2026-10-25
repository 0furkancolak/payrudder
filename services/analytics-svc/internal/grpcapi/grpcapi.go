// Package grpcapi wires the modules into AnalyticsService. It holds no logic: each method is served by the module that owns it, and a test
// fails when one is served by none.
package grpcapi

import (
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	analyticsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/analytics/v1"
	"github.com/0furkancolak/payrudder/services/analytics-svc/internal/usage"
	usageqry "github.com/0furkancolak/payrudder/services/analytics-svc/internal/usage/query"
)

type usageHandler = usage.Handler

// unimplemented is embedded one level deeper than the module handlers, so that their methods win over its.
type unimplemented struct {
	analyticsv1.UnimplementedAnalyticsServiceServer
}

// Server implements analyticsv1.AnalyticsServiceServer by delegating each method to the module that owns it.
type Server struct {
	unimplemented
	*usageHandler
}

// New returns the server.
func New(conn driver.Conn) *Server {
	return &Server{usageHandler: usage.New(usageqry.New(conn))}
}
