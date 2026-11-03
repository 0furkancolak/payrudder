package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
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

// Notice days: the least a customer must be told before a new price starts (the code's floor, a guess), and the default when nothing is configured. The operator's
// table (domain.NoticeRules) can raise the floor per country and change the default; its rows are UNVERIFIED and not legal advice.
const (
	MinNoticeDays     = domain.MinNoticeDays
	DefaultNoticeDays = domain.DefaultNoticeDays
)

// maxEffectiveFromDays bounds how far ahead a price change may name its date (default, not agreed): two years.
const maxEffectiveFromDays = 730

// pauseCheckEvery is how many subscriptions a run handles between two looks at the kill switch: a pause stops a run within a hundred subscriptions.
const pauseCheckEvery = 100

// errPaused is the answer while the operator has paused every price change (plan 15). A run that was stopped by it has scheduled what it reached; the price change
// is open, and Retry continues it once price changes are resumed.
func errPaused() error {
	return errkey.New(codes.FailedPrecondition, "plans.price_change.paused", "price changes are paused by the operator")
}

// checkNotPaused answers errPaused while the kill switch is on.
func (c *Commands) checkNotPaused(ctx context.Context) error {
	if c.pause == nil {
		return nil
	}
	paused, err := c.pause(ctx)
	if err != nil {
		return convert.Err(err, "price change")
	}
	if paused {
		return errPaused()
	}
	return nil
}

// NoticeSource reads the operator's notice rules (plan 15). They are rows of the pooled database, which a tenant placed in a cell cannot reach from its own
// transaction, so they are read on the system pool.
type NoticeSource func(ctx context.Context) (domain.NoticeRules, error)

// WithNotice makes the commands honour the operator's notice rules; without it the notice is 30 days by default and 14 at least for every country.
func (c *Commands) WithNotice(n NoticeSource) *Commands {
	c.notice = n
	return c
}

// noticeRules is the operator's notice rules, or the defaults when none are wired.
func (c *Commands) noticeRules(ctx context.Context) (domain.NoticeRules, error) {
	if c.notice == nil {
		return domain.DefaultNoticeRules(), nil
	}
	r, err := c.notice(ctx)
	return r, convert.Err(err, "price change notice")
}

// NoticeRulesOf reads the operator's notice rules of price changes (plan 15) from any connection to the pooled database. It is read on the system pool.
func NoticeRulesOf(ctx context.Context, db sqlc.DBTX) (domain.NoticeRules, error) {
	q := sqlc.New(db)
	s, err := q.GetPriceChangeNoticeSettings(ctx)
	if err != nil {
		return domain.NoticeRules{}, err
	}
	rows, err := q.ListPriceChangeNoticeRules(ctx)
	if err != nil {
		return domain.NoticeRules{}, err
	}
	out := domain.NoticeRules{Default: int(s.DefaultNoticeDays), Floors: map[string]int{}}
	for _, r := range rows {
		out.Floors[r.Country] = int(r.MinNoticeDays)
	}
	return out, nil
}

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
	rules, err := c.noticeRules(ctx)
	if err != nil {
		return nil, err
	}
	notice := int(req.GetNoticeDays())
	if notice == 0 {
		notice = rules.Default
	}
	if notice < MinNoticeDays || notice > domain.MaxNoticeDays {
		return nil, domain.Invalid("notice_days", "between 14 and 365 days")
	}
	if req.GetPlanCode() == "" || req.GetFromVersion() < 1 {
		return nil, domain.Invalid("plan_code", "a plan code and the version the subscriptions are on are required")
	}
	from, err := c.effectiveFrom(req.GetEffectiveFrom(), notice)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateNoticeSendDays(int(req.GetNoticeSendDaysBefore()), notice); err != nil {
		return nil, err
	}
	return c.runPriceChange(ctx, priceRun{plan: req.GetPlanCode(), from: req.GetFromVersion(), to: req.GetToVersion(), notice: notice, dry: req.GetDryRun(), actor: actorOf(req.GetActor()), effectiveFrom: from, rules: rules,
		sendDays: int(req.GetNoticeSendDaysBefore())})
}

// effectiveFrom checks the date a seller names for the new price (plan 15): at least the notice from now, at most two years. Unset is the old model, each
// subscription on its next renewal.
func (c *Commands) effectiveFrom(ts *timestamppb.Timestamp, notice int) (*time.Time, error) {
	if ts == nil {
		return nil, nil
	}
	if err := ts.CheckValid(); err != nil {
		return nil, domain.Invalid("effective_from", "invalid timestamp")
	}
	// Postgres keeps microseconds: a finer instant would never equal the stored one.
	t := ts.AsTime().Truncate(time.Microsecond)
	now := c.now()
	if t.Before(now.Add(time.Duration(notice) * 24 * time.Hour)) {
		return nil, domain.Invalid("effective_from", "at least notice_days from now: the customer is told before the price starts")
	}
	if t.After(now.Add(maxEffectiveFromDays * 24 * time.Hour)) {
		return nil, domain.Invalid("effective_from", "at most two years from now")
	}
	return &t, nil
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
	rules, err := c.noticeRules(ctx)
	if err != nil {
		return nil, err
	}
	run := priceRun{plan: pc.GetPlanCode(), from: pc.GetFromVersion(), to: pc.GetToVersion(), notice: int(pc.GetNoticeDays()), rules: rules, sendDays: int(pc.GetNoticeSendDaysBefore())}
	if pc.GetEffectiveFrom() != nil {
		t := pc.GetEffectiveFrom().AsTime()
		run.effectiveFrom = &t
	}
	return c.runPriceChange(ctx, run)
}

// WithdrawPriceChange closes a price change and cancels every change of it that is still waiting, subscription by subscription. A change that was applied stays,
// and so does the mail that was sent. Calling it again finishes what an interrupted call left.
func (c *Commands) WithdrawPriceChange(ctx context.Context, req *plansv1.WithdrawPriceChangeRequest) (*plansv1.WithdrawPriceChangeResponse, error) {
	id, err := domain.ParseID("id", req.GetId())
	if err != nil {
		return nil, changesqry.PriceChangeNotFound()
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var waiting []sqlc.ListPendingPriceChangeChangesRow
	err = c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		pc, err := changesqry.PriceChange(ctx, q, id)
		if err != nil {
			return err
		}
		n, err := q.WithdrawPriceChange(ctx, sqlc.WithdrawPriceChangeParams{ID: id, WithdrawnBy: actorOf(req.GetActor())})
		if err != nil {
			return err
		}
		// Calling it again to finish an interrupted withdrawal withdraws nothing and records nothing.
		if n > 0 {
			if err := audit.Record(ctx, tx, audit.Event{TenantID: ti.ID, Action: "price_change.withdrawn", TargetType: "price_change", TargetID: id.String(),
				Summary: fmt.Sprintf("withdrew the move of %s from version %d to %d", pc.GetPlanCode(), pc.GetFromVersion(), pc.GetToVersion()),
				Detail:  map[string]any{"plan": pc.GetPlanCode(), "from_version": pc.GetFromVersion(), "to_version": pc.GetToVersion()}}); err != nil {
				return err
			}
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
	// effectiveFrom is the date the seller named for the new price (nil: each subscription's next renewal); rules are the operator's notice rules.
	effectiveFrom *time.Time
	rules         domain.NoticeRules
	// sendDays is how many days before its new price each customer's mail goes out (0: when the run schedules the change).
	sendDays int
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
	// A dry run changes nothing and mails nobody, so it still counts while the switch is on.
	if !a.dry {
		if err := c.checkNotPaused(ctx); err != nil {
			return nil, err
		}
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
		} else {
			up, err := q.UpsertOpenPriceChange(ctx, sqlc.UpsertOpenPriceChangeParams{ID: idgen.NewUUIDv7(), PlanCode: a.plan, FromVersion: from.Version, ToVersion: to.Version,
				NoticeDays: int32(a.notice), EffectiveAt: c.now().Add(time.Duration(a.notice) * 24 * time.Hour), EffectiveFrom: a.effectiveFrom, NoticeSendDaysBefore: sendDaysPtr(a.sendDays), CreatedBy: a.actor}) //nolint:gosec // bounded by the callers
			if err != nil {
				return err
			}
			// Subscriptions already scheduled keep their dates, so a run cannot move the open price change to another date: withdraw it and make a new one.
			if !sameInstant(up.EffectiveFrom, a.effectiveFrom) || int(ptrOr(up.NoticeSendDaysBefore, 0)) != a.sendDays {
				return errkey.New(codes.FailedPrecondition, "plans.price_change.date_differs", "an open price change for these versions starts on another date or sends its notices on another day; withdraw it to make a new one")
			}
			pcID = up.ID
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
	runAt := c.now()
	for i, s := range ids {
		if i > 0 && i%pauseCheckEvery == 0 && !a.dry {
			if err := c.checkNotPaused(ctx); err != nil {
				return nil, err
			}
		}
		var o outcome
		err := c.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
			o, err = c.priceOne(ctx, q, a, ti.ID, pcID, s.id, toVersion, runAt)
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
	err = c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		if err := q.SetPriceChangeEffective(ctx, pcID); err != nil {
			return err
		}
		// One entry per run (a retry is a run): what was asked, and how many subscriptions it moved, found moved already and skipped.
		if err := audit.Record(ctx, tx, audit.Event{TenantID: ti.ID, Action: "price_change.scheduled", TargetType: "price_change", TargetID: pcID.String(),
			Summary: fmt.Sprintf("moved %s from version %d to %d %s, %d days notice", a.plan, a.from, toVersion, whenText(a.effectiveFrom), a.notice),
			Detail: map[string]any{"plan": a.plan, "from_version": a.from, "to_version": toVersion, "notice_days": a.notice, "effective_from": a.effectiveFrom, "notice_send_days_before": a.sendDays, "scheduled": resp.Scheduled,
				"already_scheduled": resp.AlreadyScheduled, "skipped": len(resp.Skipped)}}); err != nil {
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
func (c *Commands) priceOne(ctx context.Context, q *sqlc.Queries, a priceRun, tenantID, pcID, subID uuid.UUID, toVersion int32, runAt time.Time) (outcome, error) {
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
	// A date the seller named: the subscription moves on its first period start from it (a monthly one on the first renewal from the date, a yearly one perhaps months later).
	if a.effectiveFrom != nil {
		if err := pc.NotBefore(*a.effectiveFrom); err != nil {
			return skip("not_changeable")
		}
	}
	// The customer is told at least this long before the price starts: the run's notice, or the operator's minimum for the customer's country when that is longer.
	notice := max(a.notice, a.rules.Floor(ptrOr(cust.Country, "")))
	if pc.Quote.Effective.Before(runAt.Add(time.Duration(notice) * 24 * time.Hour)) {
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
	// The mail goes now, or is held until sendDays before the new price (never later than the customer's notice): then it waits in the item for the sweep.
	sendAt, held := domain.NoticeSendAt(c.now(), pc.Quote.Effective, a.sendDays, notice)
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
	item := sqlc.UpsertPriceChangeItemParams{PriceChangeID: pcID, SubscriptionID: subID, ChangeID: &changeID, EffectiveAt: &pc.Quote.Effective, NotifiedAt: ptr(c.now())}
	if held {
		item.NotifiedAt, item.NoticeSendAt, item.NoticeEvent = nil, &sendAt, payload
	}
	if err := q.UpsertPriceChangeItem(ctx, item); err != nil {
		return outcome{}, err
	}
	if held {
		return outcome{scheduled: true}, nil
	}
	if err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{Topic: PriceChangeTopic, PartitionKey: tenantID.String() + "/" + row.CustomerID.String(), Payload: payload}); err != nil {
		return outcome{}, err
	}
	return outcome{scheduled: true}, q.NotifyOutbox(ctx)
}

func ptr[T any](v T) *T { return &v }

// sendDaysPtr is the column value of notice_send_days_before (null for 0).
func sendDaysPtr(d int) *int32 {
	if d == 0 {
		return nil
	}
	return ptr(int32(d)) //nolint:gosec // validated to 14..365
}

// whenText says in an audit summary when the new price starts.
func whenText(from *time.Time) string {
	if from == nil {
		return "at the next renewal"
	}
	return "from " + from.UTC().Format("2006-01-02")
}

func ptrOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// sameInstant reports whether two optional instants are the same (both unset counts).
func sameInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
