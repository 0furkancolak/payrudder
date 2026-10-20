package api

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
)

const (
	maxDunningRetries = 6
	maxRetryHours     = 720
)

// GetDunningSettings implements billingv1.BillingServiceServer.
func (s *Server) GetDunningSettings(ctx context.Context, _ *billingv1.GetDunningSettingsRequest) (*billingv1.GetDunningSettingsResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	out := &billingv1.DunningSettings{}
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		var at time.Time
		err := tx.QueryRow(ctx, `SELECT retry_hours, updated_at FROM dunning_settings WHERE tenant_id = $1`, ti.ID).Scan(&out.RetryHours, &at)
		if errors.Is(err, pgx.ErrNoRows) {
			out.DefaultsInUse = true
			out.RetryHours = defaultRetryHours(ti.Environment == "sandbox")
			return nil
		}
		out.UpdateTime = timestamppb.New(at)
		return err
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &billingv1.GetDunningSettingsResponse{Settings: out}, nil
}

// SetDunningSettings implements billingv1.BillingServiceServer.
func (s *Server) SetDunningSettings(ctx context.Context, req *billingv1.SetDunningSettingsRequest) (*billingv1.SetDunningSettingsResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	hours := req.GetRetryHours()
	if len(hours) > maxDunningRetries {
		return nil, errkey.New(codes.InvalidArgument, "billing.dunning.too_many_retries", "retry_hours has at most 6 entries")
	}
	for _, h := range hours {
		if h < 1 || h > maxRetryHours {
			return nil, errkey.New(codes.InvalidArgument, "billing.dunning.bad_delay", "each retry delay is 1..720")
		}
	}
	if hours == nil {
		hours = []int32{}
	}
	out := &billingv1.DunningSettings{RetryHours: hours}
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		var at time.Time
		if err := tx.QueryRow(ctx, `INSERT INTO dunning_settings (tenant_id, retry_hours) VALUES ($1, $2)
			ON CONFLICT (tenant_id) DO UPDATE SET retry_hours = EXCLUDED.retry_hours, updated_at = now() RETURNING updated_at`, ti.ID, hours).Scan(&at); err != nil {
			return err
		}
		out.UpdateTime = timestamppb.New(at)
		return nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &billingv1.SetDunningSettingsResponse{Settings: out}, nil
}

// defaultRetryHours shows the service-wide delays the way a tenant would set them: hours, or minutes in a sandbox tenant.
func defaultRetryHours(sandbox bool) []int32 {
	d, unit := saga.DefaultDunningLive, time.Hour
	if sandbox {
		d, unit = saga.DefaultDunningSandbox, time.Minute
	}
	out := make([]int32, len(d))
	for i, v := range d {
		out[i] = int32(v / unit) //nolint:gosec // the defaults are small
	}
	return out
}
