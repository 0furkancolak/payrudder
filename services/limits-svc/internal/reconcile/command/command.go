// Package command runs the reconciliation of the Redis counters with the aggregator's totals on request.
package command

import (
	"context"
	"log/slog"

	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/engine"
)

// Commands runs reconciliation.
type Commands struct{ reconciler *engine.Reconciler }

// New returns the reconcile commands. reconciler may be nil (Run then answers Unimplemented).
func New(reconciler *engine.Reconciler) *Commands { return &Commands{reconciler: reconciler} }

// reconcileFailed answers for a reconciliation that did not run: the cause goes to the log, the client gets a fixed message.
func reconcileFailed(ctx context.Context, err error) error {
	slog.WarnContext(ctx, "reconcile failed", "error", err)
	return errkey.New(codes.Unavailable, "limits.reconcile.unavailable", "reconciliation could not run right now")
}

// Run reconciles the partitions this pod owns once, and returns what it found. Only an operator may ask.
func (c *Commands) Run(ctx context.Context) (engine.Result, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return engine.Result{}, err
	}
	if c.reconciler == nil {
		return engine.Result{}, errkey.New(codes.Unimplemented, "limits.reconcile.not_configured", "reconciliation is not configured")
	}
	res, err := c.reconciler.RunOnce(ctx)
	if err != nil {
		return engine.Result{}, reconcileFailed(ctx, err)
	}
	return res, nil
}
