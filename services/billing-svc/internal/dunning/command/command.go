// Package command changes the dunning settings of a tenant. The rules for the delays live here.
package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

const (
	maxDunningRetries = 6
	maxRetryHours     = 720
)

// Saved is what Set kept.
type Saved struct {
	RetryHours        []int32
	BlockOnChargeback bool
	At                time.Time
}

// Set replaces the tenant's retry delays (hours) and, when block names it, whether a chargeback blocks the customer; it returns what is kept, with the time of the change.
func Set(ctx context.Context, app pgxx.Beginner, tenantID uuid.UUID, hours []int32, block *bool) (Saved, error) {
	if len(hours) > maxDunningRetries {
		return Saved{}, errkey.New(codes.InvalidArgument, "billing.dunning.too_many_retries", "retry_hours has at most 6 entries")
	}
	for _, h := range hours {
		if h < 1 || h > maxRetryHours {
			return Saved{}, errkey.New(codes.InvalidArgument, "billing.dunning.bad_delay", "each retry delay is 1..720")
		}
	}
	if hours == nil {
		hours = []int32{}
	}
	var out Saved
	out.RetryHours = hours
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		before, err := q.GetDunningSettings(ctx, tenantID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		saved, err := q.UpsertDunningSettings(ctx, sqlc.UpsertDunningSettingsParams{TenantID: tenantID, RetryHours: hours, BlockOnChargeback: block})
		if err != nil {
			return err
		}
		out.At, out.BlockOnChargeback = saved.UpdatedAt, saved.BlockOnChargeback
		return audit.Record(ctx, tx, audit.Event{TenantID: tenantID, Action: "dunning.settings_changed", TargetType: "dunning_settings",
			Before: map[string]any{"retry_hours": before.RetryHours, "block_on_chargeback": before.BlockOnChargeback}, After: map[string]any{"retry_hours": hours, "block_on_chargeback": saved.BlockOnChargeback},
			Summary: fmt.Sprintf("set %d dunning retries, a chargeback %s the customer", len(hours), map[bool]string{true: "blocks", false: "does not block"}[saved.BlockOnChargeback]),
			Detail:  map[string]any{"retry_hours": hours, "previous_retry_hours": before.RetryHours, "block_on_chargeback": saved.BlockOnChargeback, "previous_block_on_chargeback": before.BlockOnChargeback}})
	})
	if err != nil {
		return Saved{}, apierr.Internal(ctx, err)
	}
	return out, nil
}
