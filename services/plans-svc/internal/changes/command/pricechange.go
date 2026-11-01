package command

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	changesqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/changes/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
	webhookscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/command"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
)

// PriceChangeTopic is the Kafka topic of the PriceChangeScheduled fact (docs/04-messaging.md), written to the outbox and relayed like subscription.trial-will-end.v1.
const PriceChangeTopic = "subscription.price-change.v1"

// Notice days: the least a customer must be told before a new price starts, and the default when the request names none. One floor for every country: a table of the
// legal minimum per country needs a reviewer's sign-off (plan 15) and is not built.
const (
	MinNoticeDays     = 14
	DefaultNoticeDays = 30
)

// maxActorRunes bounds the actor kept with a price change, as the table does.
const maxActorRunes = 120

func actorOf(a string) string {
	if r := []rune(a); len(r) > maxActorRunes {
		return string(r[:maxActorRunes])
	}
	return a
}

// SchedulePriceChange moves every Payrudder-run subscription on one plan version to another at its next renewal, when that renewal is at least notice days away,
// and queues the customer's mail. It is recorded as a price change (price_changes): running it again for the same plan and versions continues the open one, trying
// the subscriptions that were skipped again and leaving the ones it scheduled alone (they are neither scheduled nor mailed twice). Each subscription is its own
// transaction: one that cannot be scheduled is listed with the reason and the rest go on. A subscription that has another change waiting is left alone (a price
// change never replaces a change the customer or the seller asked for), and so is one whose change of this price change was withdrawn.
func (c *Commands) SchedulePriceChange(ctx context.Context, req *plansv1.SchedulePriceChangeRequest) (*plansv1.SchedulePriceChangeResponse, error) {
	notice := int(req.GetNoticeDays())
	if notice == 0 {
		notice = DefaultNoticeDays
	}
	if notice < MinNoticeDays || notice > 365 {
		return nil, domain.Invalid("notice_days", "between 14 and 365 days")
	}
	if req.GetPlanCode() == "" || req.GetFromVersion() < 1 {
		return nil, domain.Invalid("plan_code", "a plan code and the version the subscriptions are on are required")
	}
	return c.runPriceChange(ctx, priceRun{plan: req.GetPlanCode(), from: req.GetFromVersion(), to: req.GetToVersion(), notice: notice, dry: req.GetDryRun(), actor: actorOf(req.GetActor())})
}

// RetryPriceChange runs an open price change again with the notice it has: the skipped subscriptions are tried again, and so are the ones that joined the version
// since. A withdrawn price change is closed.
func (c *Commands) RetryPriceChange(ctx context.Context, req *plansv1.RetryPriceChangeRequest) (*plansv1.SchedulePriceChangeResponse, error) {
	id, err := domain.ParseID("id", req.GetId())
	if err != nil {
		return nil, changesqry.PriceChangeNotFound()
	}
	var pc *plansv1.PriceChange
	err = c.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
		pc, err = changesqry.PriceChange(ctx, q, id)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "price change")
	}
	if pc.GetState() != "open" {
		return nil, errkey.New(codes.FailedPrecondition, "plans.price_change.withdrawn", "this price change was withdrawn; make a new one")
	}
	return c.runPriceChange(ctx, priceRun{plan: pc.GetPlanCode(), from: pc.GetFromVersion(), to: pc.GetToVersion(), notice: int(pc.GetNoticeDays())})
}

// WithdrawPriceChange closes a price change and cancels every change of it that is still waiting, subscription by subscription. A change that was applied stays,
// and so does the mail that was sent. Calling it again finishes what an interrupted call left.
func (c *Commands) WithdrawPriceChange(ctx context.Context, req *plansv1.WithdrawPriceChangeRequest) (*plansv1.WithdrawPriceChangeResponse, error) {
	id, err := domain.ParseID("id", req.GetId())
	if err != nil {
		return nil, changesqry.PriceChangeNotFound()
	}
	var waiting []sqlc.ListPendingPriceChangeChangesRow
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		if _, err := changesqry.PriceChange(ctx, q, id); err != nil {
			return err
		}
		if _, err := q.WithdrawPriceChange(ctx, sqlc.WithdrawPriceChangeParams{ID: id, WithdrawnBy: actorOf(req.GetActor())}); err != nil {
			return err
		}
		waiting, err = q.ListPendingPriceChangeChanges(ctx, id)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "price change")
	}
	resp := &plansv1.WithdrawPriceChangeResponse{}
	for _, w := range waiting {
		var n int64
		err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
			// The subscription is locked first, as CancelScheduled does, so this does not race the renewal that applies the change.
			if _, err := q.GetSubscriptionForUpdate(ctx, w.SubscriptionID); err != nil {
				return err
			}
			var err error
			n, err = q.SettleChange(ctx, sqlc.SettleChangeParams{ID: *w.ChangeID, State: "cancelled"})
			return err
		})
		if err != nil {
			return nil, convert.Err(err, "subscription")
		}
		resp.Withdrawn += int32(n) //nolint:gosec // 0 or 1
	}
	err = c.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
		resp.PriceChange, err = changesqry.PriceChange(ctx, q, id)
		return err
	})
	return resp, convert.Err(err, "price change")
}

type priceRun struct {
	plan     string
	from, to int32
	notice   int
	dry      bool
	actor    string
}

type skippable struct {
	id       uuid.UUID
	customer string
}

// outcome is what a run did to one subscription: scheduled, left as it was (already scheduled by this price change, or withdrawn from it), or skipped with a reason.
type outcome struct {
	scheduled, already bool
	reason             string
}

func (c *Commands) runPriceChange(ctx context.Context, a priceRun) (*plansv1.SchedulePriceChangeResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	resp := &plansv1.SchedulePriceChangeResponse{NoticeDays: int32(a.notice)} //nolint:gosec // bounded by the callers
	var ids []skippable
	var toVersion int32
	var pcID uuid.UUID // the price change this run records into; none for a dry run when nothing is open
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		from, err := q.GetPlanVersionByCode(ctx, sqlc.GetPlanVersionByCodeParams{PlanCode: a.plan, Version: a.from})
		if err != nil {
			return convert.Err(err, "plan version")
		}
		to, err := q.GetPlanVersionByCode(ctx, sqlc.GetPlanVersionByCodeParams{PlanCode: a.plan, Version: a.to})
		if err != nil {
			return convert.Err(err, "plan version")
		}
		if to.ID == from.ID {
			return domain.Invalid("to_version", "the subscriptions are on this version already")
		}
		toVersion = to.Version
		if a.dry {
			// A dry run writes nothing, but reads the open price change so that it counts what is scheduled already as such.
			switch id, err := q.GetOpenPriceChangeID(ctx, sqlc.GetOpenPriceChangeIDParams{PlanCode: a.plan, FromVersion: from.Version, ToVersion: to.Version}); {
			case err == nil:
				pcID = id
			case !errors.Is(err, pgx.ErrNoRows):
				return err
			}
		} else if pcID, err = q.UpsertOpenPriceChange(ctx, sqlc.UpsertOpenPriceChangeParams{ID: idgen.NewUUIDv7(), PlanCode: a.plan, FromVersion: from.Version, ToVersion: to.Version,
			NoticeDays: int32(a.notice), EffectiveAt: c.now().Add(time.Duration(a.notice) * 24 * time.Hour), CreatedBy: a.actor}); err != nil { //nolint:gosec // bounded by the callers
			return err
		}
		rows, err := q.ListLiveSubscriptionsOnVersion(ctx, from.ID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			ids = append(ids, skippable{id: r.ID, customer: r.CustomerExternalID})
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "plan version")
	}
	resp.ToVersion = toVersion
	if pcID != uuid.Nil {
		resp.PriceChangeId = pcID.String()
	}
	earliest := c.now().Add(time.Duration(a.notice) * 24 * time.Hour)
	for _, s := range ids {
		var o outcome
		err := c.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
			o, err = c.priceOne(ctx, q, a, ti.ID, pcID, s.id, toVersion, earliest)
			return err
		})
		if err != nil {
			return nil, convert.Err(err, "subscription")
		}
		switch {
		case o.reason != "":
			resp.Skipped = append(resp.Skipped, &plansv1.PriceChangeSkipped{SubscriptionId: s.id.String(), CustomerExternalId: s.customer, Reason: o.reason})
		case o.already:
			resp.AlreadyScheduled++
		case o.scheduled:
			resp.Scheduled++
			resp.ScheduledSubscriptionIds = append(resp.ScheduledSubscriptionIds, s.id.String())
		}
	}
	if a.dry || pcID == uuid.Nil {
		return resp, nil
	}
	// The record learns the first date a price changes, and the tenant's webhooks learn of a run that scheduled something.
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		if err := q.SetPriceChangeEffective(ctx, pcID); err != nil {
			return err
		}
		if resp.Scheduled == 0 {
			return nil
		}
		pc, err := changesqry.PriceChange(ctx, q, pcID)
		if err != nil {
			return err
		}
		raw, err := webhooksmodel.EventJSON.Marshal(pc)
		if err != nil {
			return err
		}
		data, err := json.Marshal(map[string]any{"price_change": json.RawMessage(raw), "scheduled": resp.Scheduled, "skipped": len(resp.Skipped)})
		if err != nil {
			return err
		}
		_, err = webhookscmd.Emit(ctx, q, idgen.NewUUIDv7(), webhooksmodel.PriceChangeScheduled, data, c.now())
		return err
	})
	return resp, convert.Err(err, "price change")
}

// priceOne decides and does what the run does to one subscription, in its own transaction.
func (c *Commands) priceOne(ctx context.Context, q *sqlc.Queries, a priceRun, tenantID, pcID, subID uuid.UUID, toVersion int32, earliest time.Time) (outcome, error) {
	row, err := subsqry.Lock(ctx, q, subID)
	if err != nil {
		return outcome{}, err
	}
	if pcID != uuid.Nil {
		// This price change has scheduled the subscription already: a change that is waiting is counted and not made again; one that was withdrawn (by the seller or
		// because the customer moved on) is not made again either.
		switch it, err := q.GetPriceChangeItemChange(ctx, sqlc.GetPriceChangeItemChangeParams{PriceChangeID: pcID, SubscriptionID: subID}); {
		case err == nil && it.ChangeID != nil:
			return outcome{already: it.ChangeState == "pending"}, nil
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return outcome{}, err
		}
	}
	skip := func(reason string) (outcome, error) {
		if !a.dry {
			if err := q.UpsertPriceChangeItem(ctx, sqlc.UpsertPriceChangeItemParams{PriceChangeID: pcID, SubscriptionID: subID, Reason: reason}); err != nil {
				return outcome{}, err
			}
		}
		return outcome{reason: reason}, nil
	}
	if row.Owner != domain.OwnerPayrudder {
		return skip("run_by_provider")
	}
	// A blocked customer is not offered a new price: the change would be a purchase the block refuses (pkg/paymentgate), and the mail would reach them.
	cust, err := q.GetCustomerByID(ctx, row.CustomerID)
	if err != nil {
		return outcome{}, err
	}
	if convert.BlockOf(cust).InForce(c.now()) {
		return skip("customer_blocked")
	}
	if _, err := q.GetPendingChange(ctx, subID); err == nil {
		return skip("has_pending_change")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return outcome{}, err
	}
	pc, err := changesqry.Price(ctx, q, c.now(), row, &plansv1.ChangeSubscriptionRequest{SubscriptionId: subID.String(), PlanCode: a.plan, PlanVersion: toVersion, Timing: plansv1.ChangeTiming_CHANGE_TIMING_PERIOD_END})
	if err != nil {
		return skip("not_changeable")
	}
	if pc.Quote.Effective.Before(earliest) {
		return skip("renews_soon")
	}
	if a.dry {
		return outcome{scheduled: true}, nil
	}
	cr := &plansv1.ChangeSubscriptionResponse{Quote: pc.Message()}
	changeID, err := c.schedule(ctx, q, cr, row, pc)
	if err != nil {
		return outcome{}, err
	}
	if err := q.UpsertPriceChangeItem(ctx, sqlc.UpsertPriceChangeItemParams{PriceChangeID: pcID, SubscriptionID: subID, ChangeID: &changeID, EffectiveAt: &pc.Quote.Effective, NotifiedAt: ptr(c.now())}); err != nil {
		return outcome{}, err
	}
	// The customer's mail: one fact per scheduled change, deduplicated by its id.
	ev := &eventsv1.PriceChangeScheduled{
		EventId: idgen.Deterministic("price_change", tenantID.String(), subID.String(), pc.Quote.Effective.UTC().Format(time.RFC3339Nano)).String(), TenantId: tenantID.String(),
		CustomerId: row.CustomerID.String(), SubscriptionId: subID.String(), PlanCode: a.plan, NewPlanVersion: toVersion,
		EffectiveAt: timestamppb.New(pc.Quote.Effective), NewRenewalMinor: pc.Quote.NextRenewalMinor, Currency: row.Currency, ScheduledAt: timestamppb.New(c.now()),
	}
	payload, err := proto.Marshal(ev)
	if err != nil {
		return outcome{}, err
	}
	if err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{Topic: PriceChangeTopic, PartitionKey: tenantID.String() + "/" + row.CustomerID.String(), Payload: payload}); err != nil {
		return outcome{}, err
	}
	return outcome{scheduled: true}, q.NotifyOutbox(ctx)
}

func ptr[T any](v T) *T { return &v }
