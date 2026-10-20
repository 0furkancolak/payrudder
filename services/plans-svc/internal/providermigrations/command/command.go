// Package command moves a subscription from one payment provider to another.
package command

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/payments/providers"
	"github.com/0furkancolak/quotify/pkg/subscription"
	"github.com/0furkancolak/quotify/pkg/tenant"
	changescmd "github.com/0furkancolak/quotify/services/plans-svc/internal/changes/command"
	changesqry "github.com/0furkancolak/quotify/services/plans-svc/internal/changes/query"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	entitlementscmd "github.com/0furkancolak/quotify/services/plans-svc/internal/entitlements/command"
	provmigqry "github.com/0furkancolak/quotify/services/plans-svc/internal/providermigrations/query"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/quotify/services/plans-svc/internal/subscriptions/query"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/webhooks"
)

// Commands changes provider migrations.
type Commands struct {
	db  *store.DB
	now func() time.Time
}

// New returns the migration commands.
func New(db *store.DB, now func() time.Time) *Commands { return &Commands{db: db, now: now} }

// cutover ends the old subscription at when and starts the one that replaces it from then, in one transaction: the chain that billing splits
// the month's fixed fee along. The new subscription is the old one with another provider (and, when the provider runs it, its id there).
func (c *Commands) cutover(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, old sqlc.GetSubscriptionRow, when time.Time,
	toProvider string, providerRef *string, owner string) (sqlc.InsertSubscriptionRow, error) {
	if !when.After(old.StartTime) {
		return sqlc.InsertSubscriptionRow{}, domain.Invalid("cutover_time", "must be after the subscription start")
	}
	now := c.now()
	if n, err := q.EndSubscription(ctx, sqlc.EndSubscriptionParams{ID: old.ID, EndTime: when}); err != nil {
		return sqlc.InsertSubscriptionRow{}, err
	} else if n != 1 {
		return sqlc.InsertSubscriptionRow{}, status.Error(codes.Aborted, "subscription changed concurrently; retry")
	}
	eff := convert.SubscriptionState(old.State, old.TrialEndsAt, old.OpenEnded, old.EndTime, now)
	if !when.After(now) {
		if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{ID: old.ID, State: string(subscription.Cancelled), PausedAt: old.PausedAt}); err != nil {
			return sqlc.InsertSubscriptionRow{}, err
		}
	}
	// A fee paid in advance for the rest of the period the old provider-side subscription will not serve goes to the customer's credit balance.
	// A coupon makes that sum differ from the list fee: such a subscription is left to a credit note.
	if old.Owner == "quotify" {
		if _, err := changescmd.CreditUnusedFee(ctx, q, now, old, when); err != nil && !errors.Is(err, changesqry.ErrCouponOnCredit) {
			return sqlc.InsertSubscriptionRow{}, err
		}
	}
	oldID := old.ID
	nr, err := q.InsertSubscription(ctx, sqlc.InsertSubscriptionParams{
		ID: idgen.NewUUIDv7(), CustomerID: old.CustomerID, PlanVersionID: old.PlanVersionID, StartTime: when,
		State: string(eff), Interval: old.Interval, Currency: old.Currency, TrialEndsAt: old.TrialEndsAt,
		BillingAnchor: old.BillingAnchor, ChangedFromID: &oldID, Provider: &toProvider, ProviderRef: providerRef, Owner: owner, FeeTiming: "arrears",
	})
	if err != nil {
		return nr, err
	}
	if err := entitlementscmd.Publish(ctx, q, tenantID, old.CustomerID, old.PlanVersionID, old.StartTime, &when, now); err != nil {
		return nr, err
	}
	if err := entitlementscmd.Publish(ctx, q, tenantID, nr.CustomerID, nr.PlanVersionID, when, nil, now); err != nil {
		return nr, err
	}
	moved, err := q.GetSubscription(ctx, nr.ID)
	if err != nil {
		return nr, err
	}
	return nr, webhooks.EmitSubscription(ctx, q, now, webhooks.SubscriptionMovedToProvider, subsqry.Message(moved, now), map[string]any{"previous_subscription_id": old.ID.String(), "to_provider": toProvider})
}

// Start begins moving a subscription to another provider: at once when Quotify runs it there, else when the customer has subscribed there.
func (c *Commands) Start(ctx context.Context, req *plansv1.StartProviderMigrationRequest) (*plansv1.ProviderMigration, error) {
	id, err := subsqry.ParseID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	target, ok := providers.Lookup(req.GetToProvider())
	if !ok {
		return nil, domain.Invalid("to_provider", "unknown provider "+req.GetToProvider())
	}
	var cutoverAt *time.Time
	if req.GetCutoverTime() != nil {
		if err := req.GetCutoverTime().CheckValid(); err != nil {
			return nil, domain.Invalid("cutover_time", "invalid timestamp")
		}
		t := req.GetCutoverTime().AsTime()
		cutoverAt = &t
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var out *plansv1.ProviderMigration
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.GetSubscriptionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		row := sqlc.GetSubscriptionRow(cur)
		now := c.now()
		eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, now)
		switch {
		case !row.OpenEnded || row.CancelAtPeriodEnd:
			return status.Error(codes.FailedPrecondition, "an ended or ending subscription cannot be moved")
		case eff != subscription.Active && eff != subscription.Trialing:
			return status.Errorf(codes.FailedPrecondition, "a %s subscription cannot be moved", eff)
		case convert.Str(row.Provider) == req.GetToProvider():
			return domain.Invalid("to_provider", "the subscription is already with "+req.GetToProvider())
		case !target.CanCharge(row.Currency):
			return status.Errorf(codes.FailedPrecondition, "%s cannot charge in %s", req.GetToProvider(), row.Currency)
		}
		mig, err := q.InsertProviderMigration(ctx, sqlc.InsertProviderMigrationParams{
			ID: idgen.NewUUIDv7(), SubscriptionID: id, FromProvider: convert.Str(row.Provider), ToProvider: req.GetToProvider(), State: "started", CutoverAt: cutoverAt,
		})
		if err != nil {
			return err
		}
		if target.Owner() == "provider" {
			// The target provider runs it: wait for the customer to subscribe there. Nothing about the old subscription changes yet.
			out = provmigqry.Message(mig)
			return nil
		}
		// Quotify runs it at the target: take over now, or at the end of the paid period the old provider has been paid for.
		when := now
		if cutoverAt != nil {
			when = *cutoverAt
		} else if row.Owner == "provider" {
			return domain.Invalid("cutover_time", "required when the subscription is run by its provider: its paid period must not be cut short")
		}
		nr, err := c.cutover(ctx, q, ti.ID, row, when, req.GetToProvider(), nil, "quotify")
		if err != nil {
			return err
		}
		done, err := q.FinishProviderMigration(ctx, sqlc.FinishProviderMigrationParams{ID: mig.ID, State: "completed", NewSubscriptionID: &nr.ID, CutoverAt: &when})
		if err != nil {
			return err
		}
		out = provmigqry.Message(done)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "migration")
	}
	return out, nil
}

// Complete finishes a migration to a provider that runs the subscription, once the customer has subscribed there. Idempotent.
func (c *Commands) Complete(ctx context.Context, req *plansv1.CompleteProviderMigrationRequest) (*plansv1.CompleteProviderMigrationResponse, error) {
	id, err := uuid.Parse(req.GetMigrationId())
	if err != nil {
		return nil, domain.Invalid("migration_id", "must be a UUID")
	}
	if req.GetProviderRef() == "" {
		return nil, domain.Invalid("provider_ref", "required")
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var resp plansv1.CompleteProviderMigrationResponse
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		mig, err := q.GetProviderMigrationForUpdate(ctx, id)
		if err != nil {
			return err
		}
		old, err := q.GetSubscription(ctx, mig.SubscriptionID)
		if err != nil {
			return err
		}
		resp.SourceProvider = convert.Str(old.Provider)
		if old.Owner == "provider" {
			resp.SourceProviderRef = convert.Str(old.ProviderRef)
		}
		switch mig.State {
		case "completed":
			// Idempotent: the provider's notifications are delivered more than once.
			if mig.NewSubscriptionID == nil {
				return status.Error(codes.Internal, "completed migration without a subscription")
			}
			nr, err := q.GetSubscription(ctx, *mig.NewSubscriptionID)
			if err != nil {
				return err
			}
			if convert.Str(nr.ProviderRef) != req.GetProviderRef() {
				return status.Errorf(codes.FailedPrecondition, "migration already completed with %s", convert.Str(nr.ProviderRef))
			}
			resp.Migration, resp.Subscription = provmigqry.Message(mig), subsqry.Message(nr, c.now())
			return nil
		case "aborted":
			return status.Error(codes.FailedPrecondition, "migration was aborted")
		}
		when := c.now()
		if req.GetCutoverTime() != nil {
			when = req.GetCutoverTime().AsTime()
		}
		if !old.OpenEnded {
			return status.Error(codes.FailedPrecondition, "the subscription ended while the migration was waiting")
		}
		nr, err := c.cutover(ctx, q, ti.ID, old, when, mig.ToProvider, new(req.GetProviderRef()), "provider")
		if err != nil {
			return err
		}
		done, err := q.FinishProviderMigration(ctx, sqlc.FinishProviderMigrationParams{ID: id, State: "completed", NewSubscriptionID: &nr.ID, CutoverAt: &when})
		if err != nil {
			return err
		}
		nrow, err := q.GetSubscription(ctx, nr.ID)
		if err != nil {
			return err
		}
		resp.Migration, resp.Subscription = provmigqry.Message(done), subsqry.Message(nrow, c.now())
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "migration")
	}
	return &resp, nil
}

// Abort gives up a migration that has not completed. Aborting twice is fine.
func (c *Commands) Abort(ctx context.Context, migrationID string) (*plansv1.ProviderMigration, error) {
	id, err := uuid.Parse(migrationID)
	if err != nil {
		return nil, domain.Invalid("migration_id", "must be a UUID")
	}
	var out *plansv1.ProviderMigration
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		mig, err := q.GetProviderMigrationForUpdate(ctx, id)
		if err != nil {
			return err
		}
		switch mig.State {
		case "aborted":
			out = provmigqry.Message(mig)
			return nil
		case "completed":
			return status.Error(codes.FailedPrecondition, "the migration has completed; it cannot be aborted")
		}
		done, err := q.FinishProviderMigration(ctx, sqlc.FinishProviderMigrationParams{ID: id, State: "aborted", NewSubscriptionID: nil, CutoverAt: mig.CutoverAt})
		if err != nil {
			return err
		}
		out = provmigqry.Message(done)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "migration")
	}
	return out, nil
}
