// Package command changes the dunning settings of a tenant. The rules for the delays live here.
package command

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

const (
	maxDunningRetries = 6
	maxRetryHours     = 720
)

// Set replaces the tenant's retry delays (hours) and returns them with the time of the change.
func Set(ctx context.Context, app pgxx.Beginner, tenantID uuid.UUID, hours []int32) ([]int32, time.Time, error) {
	if len(hours) > maxDunningRetries {
		return nil, time.Time{}, errkey.New(codes.InvalidArgument, "billing.dunning.too_many_retries", "retry_hours has at most 6 entries")
	}
	for _, h := range hours {
		if h < 1 || h > maxRetryHours {
			return nil, time.Time{}, errkey.New(codes.InvalidArgument, "billing.dunning.bad_delay", "each retry delay is 1..720")
		}
	}
	if hours == nil {
		hours = []int32{}
	}
	var at time.Time
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		at, err = sqlc.New(tx).UpsertDunningSettings(ctx, sqlc.UpsertDunningSettingsParams{TenantID: tenantID, RetryHours: hours})
		return err
	})
	if err != nil {
		return nil, time.Time{}, apierr.Internal(ctx, err)
	}
	return hours, at, nil
}
