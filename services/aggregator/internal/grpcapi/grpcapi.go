// Package grpcapi wires the modules into UsageQueryService. It holds no logic: each method is served by the module that owns it, and a
// test fails when one is served by none.
package grpcapi

import (
	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/periods"
	periodscmd "github.com/0furkancolak/payrudder/services/aggregator/internal/periods/command"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/settings"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/usage"
	usageqry "github.com/0furkancolak/payrudder/services/aggregator/internal/usage/query"
)

type (
	usageHandler   = usage.Handler
	periodsHandler = periods.Handler
)

// unimplemented is embedded one level deeper than the module handlers, so that their methods win over its.
type unimplemented struct {
	aggregatorv1.UnimplementedUsageQueryServiceServer
}

// Server implements aggregatorv1.UsageQueryServiceServer by delegating each method to the module that owns it.
type Server struct {
	unimplemented
	*usageHandler
	*periodsHandler
}

// New returns the server. app is the app-role pool, or its router when cells are on. group and topic key the consumer_offsets rows of the live consumer.
func New(app pgxx.Beginner, src *settings.Source, group, topic string) *Server {
	return &Server{
		usageHandler:   usage.New(usageqry.New(app, group, topic)),
		periodsHandler: periods.New(periodscmd.New(app, src)),
	}
}
