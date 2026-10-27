// Package command moves a subscription from one payment provider to another.
package command

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/providers"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	provmigqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/providermigrations/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subscriptionscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/command"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
	webhookscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/command"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
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
	eff := convert.SubscriptionState(old.State, old.TrialEndsAt, old.OpenEnded, old.EndTime, now)
	// What the successor pays, and when. A Payrudder-run subscription whose fee is already paid in advance goes on with the period it paid for (no credit, the
	// same renewal, its coupon); a yearly one that a provider ran comes back billed in advance from the cutover, as a year cannot be waited out in arrears.
	timing, anchor, feeDue, creditUnused := subscription.FeeInArrears, old.BillingAnchor, (*time.Time)(nil), old.Owner == domain.OwnerPayrudder
	if owner == domain.OwnerPayrudder {
		switch {
		case old.FeeTiming == subscription.FeeInAdvance && old.Owner == domain.OwnerPayrudder:
			due, err := q.GetSubscriptionFeeDue(ctx, old.ID)
			if err != nil {
				return sqlc.InsertSubscriptionRow{}, err
			}
			if due != nil {
				timing, feeDue, creditUnused = subscription.FeeInAdvance, due, false
			}
		case subscription.Interval(old.Interval) == subscription.Yearly:
			timing, anchor, feeDue = subscription.FeeInAdvance, when, &when
		}
	}
	next := subscriptionscmd.Successor{
		PlanVersionID: old.PlanVersionID, Interval: subscription.Interval(old.Interval), State: eff, TrialEnds: old.TrialEndsAt, At: when,
		Anchor: anchor, FeeTiming: timing, FeeDue: feeDue, Owner: owner, ProviderRef: providerRef, Provider: &toProvider, LeaveOldOpen: when.After(now),
	}
	// A coupon follows only a successor Payrudder invoices in advance. Anything else would drop it without a word, and with it the discount the fee was paid at.
	if !subscriptionscmd.CarriesCoupon(next) {
		if err := requireNoCoupon(ctx, q, old.ID); err != nil {
			return sqlc.InsertSubscriptionRow{}, err
		}
	}
	// A fee paid in advance for the rest of the period the old provider-side subscription will not serve goes to the customer's credit balance.
	if creditUnused {
		if _, err := subscriptionscmd.CreditUnusedFee(ctx, q, now, old, when); err != nil {
			return sqlc.InsertSubscriptionRow{}, err
		}
	}
	// The old subscription is cancelled at once only when the cutover is not in the future.
	nr, err := subscriptionscmd.SwitchTo(ctx, q, now, tenantID, old, next)
	if err != nil {
		return nr, err
	}
	moved, err := q.GetSubscription(ctx, nr.ID)
	if err != nil {
		return nr, err
	}
	return nr, webhookscmd.EmitSubscription(ctx, q, now, webhooksmodel.SubscriptionMovedToProvider, convert.SubscriptionOf(moved, now), map[string]any{"previous_subscription_id": old.ID.String(), "to_provider": toProvider})
}

// requireNoCoupon refuses a move that cannot carry the coupon of the subscription: the operator takes it off first, knowing the discount ends.
func requireNoCoupon(ctx context.Context, q *sqlc.Queries, id uuid.UUID) error {
	_, err := q.GetSubscriptionCoupon(ctx, id)
	switch {
	case err == nil:
		return errkey.New(codes.FailedPrecondition, "plans.migration.coupon_blocks_move", "this subscription has a coupon, which a provider-run or arrears-billed subscription cannot carry; remove the coupon first")
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	}
	return err
}

// movable says why a subscription cannot be moved to the provider now: it has ended or is ending, it is not active, it is there already, or the provider
// cannot charge in its currency.
func movable(row sqlc.GetSubscriptionRow, toProvider string, target payments.Capabilities, now time.Time) error {
	eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, now)
	switch {
	case !row.OpenEnded || row.CancelAtPeriodEnd:
		return errkey.New(codes.FailedPrecondition, "plans.migration.subscription_ended", "an ended or ending subscription cannot be moved")
	case eff != subscription.Active && eff != subscription.Trialing:
		return errkey.Newf(codes.FailedPrecondition, "plans.migration.state_not_movable", "a %s subscription cannot be moved", eff)
	case convert.Str(row.Provider) == toProvider:
		return domain.Invalid("to_provider", "the subscription is already with "+toProvider)
	case !target.CanCharge(row.Currency):
		return errkey.Newf(codes.FailedPrecondition, "plans.subscription.provider_currency", "%s cannot charge in %s", toProvider, row.Currency)
	}
	return nil
}

// Start begins moving a subscription to another provider: at once when Payrudder runs it there, else when the customer has subscribed there.
func (c *Commands) Start(ctx context.Context, req *plansv1.StartProviderMigrationRequest) (*plansv1.ProviderMigration, error) {
	id, err := domain.ParseID("subscription_id", req.GetSubscriptionId())
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
		row, err := subsqry.Lock(ctx, q, id)
		if err != nil {
			return err
		}
		now := c.now()
		if err := movable(row, req.GetToProvider(), target, now); err != nil {
			return err
		}
		mig, err := q.InsertProviderMigration(ctx, sqlc.InsertProviderMigrationParams{
			ID: idgen.NewUUIDv7(), SubscriptionID: id, FromProvider: convert.Str(row.Provider), ToProvider: req.GetToProvider(), State: "started", CutoverAt: cutoverAt,
		})
		if err != nil {
			return err
		}
		if target.Owner() == payments.OwnerProvider {
			// The coupon would be dropped when the customer has subscribed there, which is too late to refuse: say so now.
			if err := requireNoCoupon(ctx, q, id); err != nil {
				return err
			}
			// The target provider runs it: wait for the customer to subscribe there. Nothing about the old subscription changes yet.
			out = provmigqry.Message(mig)
			return nil
		}
		// Payrudder runs it at the target: take over now, or at the end of the paid period the old provider has been paid for.
		when := now
		if cutoverAt != nil {
			when = *cutoverAt
		} else if row.Owner == domain.OwnerProvider {
			return domain.Invalid("cutover_time", "required when the subscription is run by its provider: its paid period must not be cut short")
		}
		nr, err := c.cutover(ctx, q, ti.ID, row, when, req.GetToProvider(), nil, domain.OwnerPayrudder)
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
	id, err := domain.ParseID("migration_id", req.GetMigrationId())
	if err != nil {
		return nil, err
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
		if old.Owner == domain.OwnerProvider {
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
				return errkey.Newf(codes.FailedPrecondition, "plans.migration.already_completed", "migration already completed with %s", convert.Str(nr.ProviderRef))
			}
			resp.Migration, resp.Subscription = provmigqry.Message(mig), convert.SubscriptionOf(nr, c.now())
			return nil
		case "aborted":
			return errkey.New(codes.FailedPrecondition, "plans.migration.aborted", "migration was aborted")
		}
		when := c.now()
		if req.GetCutoverTime() != nil {
			when = req.GetCutoverTime().AsTime()
		}
		if !old.OpenEnded {
			return errkey.New(codes.FailedPrecondition, "plans.migration.subscription_ended", "the subscription ended while the migration was waiting")
		}
		nr, err := c.cutover(ctx, q, ti.ID, old, when, mig.ToProvider, new(req.GetProviderRef()), domain.OwnerProvider)
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
		resp.Migration, resp.Subscription = provmigqry.Message(done), convert.SubscriptionOf(nrow, c.now())
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "migration")
	}
	return &resp, nil
}

// Abort gives up a migration that has not completed. Aborting twice is fine.
func (c *Commands) Abort(ctx context.Context, migrationID string) (*plansv1.ProviderMigration, error) {
	id, err := domain.ParseID("migration_id", migrationID)
	if err != nil {
		return nil, err
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
			return errkey.New(codes.FailedPrecondition, "plans.migration.cannot_abort", "the migration has completed; it cannot be aborted")
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
