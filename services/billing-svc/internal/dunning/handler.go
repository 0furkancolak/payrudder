// Package dunning serves the dunning-settings methods of BillingService: it maps requests to commands and queries.
package dunning

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/dunning/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/dunning/query"
)

// Handler implements the dunning-settings methods of billingv1.BillingServiceServer.
type Handler struct {
	app      pgxx.Beginner
	defaults query.Defaults
}

// New returns the handler over the billing_app pool.
func New(app pgxx.Beginner, defaults query.Defaults) *Handler {
	return &Handler{app: app, defaults: defaults}
}

// GetDunningSettings implements billingv1.BillingServiceServer.
func (h *Handler) GetDunningSettings(ctx context.Context, _ *billingv1.GetDunningSettingsRequest) (*billingv1.GetDunningSettingsResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	s, err := query.Get(ctx, h.app, h.defaults, ti)
	if err != nil {
		return nil, err
	}
	out := &billingv1.DunningSettings{RetryHours: s.RetryHours, DefaultsInUse: s.DefaultsInUse}
	if s.UpdatedAt != nil {
		out.UpdateTime = timestamppb.New(*s.UpdatedAt)
	}
	return &billingv1.GetDunningSettingsResponse{Settings: out}, nil
}

// SetDunningSettings implements billingv1.BillingServiceServer.
func (h *Handler) SetDunningSettings(ctx context.Context, req *billingv1.SetDunningSettingsRequest) (*billingv1.SetDunningSettingsResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	hours, at, err := command.Set(ctx, h.app, ti.ID, req.GetRetryHours())
	if err != nil {
		return nil, err
	}
	return &billingv1.SetDunningSettingsResponse{Settings: &billingv1.DunningSettings{RetryHours: hours, UpdateTime: timestamppb.New(at)}}, nil
}
