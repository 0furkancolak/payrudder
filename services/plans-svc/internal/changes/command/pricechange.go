package command

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	changesqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/changes/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
)

// PriceChangeTopic is the Kafka topic of the PriceChangeScheduled fact (docs/04-messaging.md), written to the outbox and relayed like subscription.trial-will-end.v1.
const PriceChangeTopic = "subscription.price-change.v1"

// Notice days: the least a customer must be told before a new price starts, and the default when the request names none.
const (
	MinNoticeDays     = 14
	DefaultNoticeDays = 30
)

// SchedulePriceChange moves every Payrudder-run subscription on one plan version to another at its next renewal, when that renewal is at least notice days away,
// and queues the customer's mail. Each subscription is its own transaction: one that cannot be scheduled is listed with the reason and the rest go on. A subscription
// that already has a change waiting is left alone (a price change never replaces a change the customer or the seller asked for).
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
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	resp := &plansv1.SchedulePriceChangeResponse{NoticeDays: int32(notice)} //nolint:gosec // bounded above
	var ids []skippable
	var toVersion int32
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		from, err := q.GetPlanVersionByCode(ctx, sqlc.GetPlanVersionByCodeParams{PlanCode: req.GetPlanCode(), Version: req.GetFromVersion()})
		if err != nil {
			return convert.Err(err, "plan version")
		}
		to, err := q.GetPlanVersionByCode(ctx, sqlc.GetPlanVersionByCodeParams{PlanCode: req.GetPlanCode(), Version: req.GetToVersion()})
		if err != nil {
			return convert.Err(err, "plan version")
		}
		if to.ID == from.ID {
			return domain.Invalid("to_version", "the subscriptions are on this version already")
		}
		toVersion = to.Version
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
	earliest := c.now().Add(time.Duration(notice) * 24 * time.Hour)
	for _, s := range ids {
		reason := ""
		err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
			row, err := subsqry.Lock(ctx, q, s.id)
			if err != nil {
				return err
			}
			if row.Owner != domain.OwnerPayrudder {
				reason = "run_by_provider"
				return nil
			}
			if _, err := q.GetPendingChange(ctx, s.id); err == nil {
				reason = "has_pending_change"
				return nil
			} else if err != pgx.ErrNoRows {
				return err
			}
			pc, err := changesqry.Price(ctx, q, c.now(), row, &plansv1.ChangeSubscriptionRequest{SubscriptionId: s.id.String(), PlanCode: req.GetPlanCode(), PlanVersion: toVersion, Timing: plansv1.ChangeTiming_CHANGE_TIMING_PERIOD_END})
			if err != nil {
				reason = "not_changeable"
				return nil
			}
			if pc.Quote.Effective.Before(earliest) {
				reason = "renews_soon"
				return nil
			}
			if req.GetDryRun() {
				return nil
			}
			cr := &plansv1.ChangeSubscriptionResponse{Quote: pc.Message()}
			if err := c.schedule(ctx, q, cr, row, pc); err != nil {
				return err
			}
			// The customer's mail: one fact per scheduled change, deduplicated by its id.
			ev := &eventsv1.PriceChangeScheduled{
				EventId: idgen.Deterministic("price_change", ti.ID.String(), s.id.String(), pc.Quote.Effective.UTC().Format(time.RFC3339Nano)).String(), TenantId: ti.ID.String(),
				CustomerId: row.CustomerID.String(), SubscriptionId: s.id.String(), PlanCode: req.GetPlanCode(), NewPlanVersion: toVersion,
				EffectiveAt: timestamppb.New(pc.Quote.Effective), NewRenewalMinor: pc.Quote.NextRenewalMinor, Currency: row.Currency, ScheduledAt: timestamppb.New(c.now()),
			}
			payload, err := proto.Marshal(ev)
			if err != nil {
				return err
			}
			if err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{Topic: PriceChangeTopic, PartitionKey: ti.ID.String() + "/" + row.CustomerID.String(), Payload: payload}); err != nil {
				return err
			}
			return q.NotifyOutbox(ctx)
		})
		if err != nil {
			return nil, convert.Err(err, "subscription")
		}
		if reason != "" {
			resp.Skipped = append(resp.Skipped, &plansv1.PriceChangeSkipped{SubscriptionId: s.id.String(), CustomerExternalId: s.customer, Reason: reason})
			continue
		}
		resp.Scheduled++
		resp.ScheduledSubscriptionIds = append(resp.ScheduledSubscriptionIds, s.id.String())
	}
	return resp, nil
}

type skippable struct {
	id       uuid.UUID
	customer string
}
