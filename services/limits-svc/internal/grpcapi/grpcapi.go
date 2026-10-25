// Package grpcapi wires the modules into LimitsService. It holds no logic: each method is served by the module that owns it, and a test
// fails when one is served by none.
package grpcapi

import (
	"github.com/redis/go-redis/v9"

	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	"github.com/0furkancolak/payrudder/pkg/meters"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/engine"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/entitlements"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/reconcile"
	reconcilecmd "github.com/0furkancolak/payrudder/services/limits-svc/internal/reconcile/command"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/status"
	statusqry "github.com/0furkancolak/payrudder/services/limits-svc/internal/status/query"
)

type (
	statusHandler    = status.Handler
	reconcileHandler = reconcile.Handler
)

// unimplemented is embedded one level deeper than the module handlers, so that their methods win over its.
type unimplemented struct {
	limitsv1.UnimplementedLimitsServiceServer
}

// Server implements limitsv1.LimitsServiceServer by delegating each method to the module that owns it.
type Server struct {
	unimplemented
	*statusHandler
	*reconcileHandler
}

// New returns the server. rec may be nil (ReconcileNow then answers Unimplemented).
func New(rdb *redis.Client, ents *entitlements.Store, ms meters.Source, rec *engine.Reconciler) *Server {
	return &Server{
		statusHandler:    status.New(statusqry.New(rdb, ents, ms)),
		reconcileHandler: reconcile.New(reconcilecmd.New(rec)),
	}
}
