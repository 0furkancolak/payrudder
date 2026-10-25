// Package query reads subscriptions and shapes them as API messages. Other modules use Message and AttachExtras to return a subscription.
package query

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Queries reads subscriptions.
type Queries struct {
	db  *store.DB
	now func() time.Time
}

// New returns the subscription queries.
func New(db *store.DB, now func() time.Time) *Queries { return &Queries{db: db, now: now} }

// ParseID reads a subscription id from a request.
func ParseID(id string) (uuid.UUID, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, domain.Invalid("subscription_id", "must be a UUID")
	}
	return u, nil
}

// Message maps a subscription row to its API message, with the state it has at now.
func Message(r sqlc.GetSubscriptionRow, now time.Time) *plansv1.Subscription {
	return convert.Subscription(convert.SubscriptionFields{
		ID: r.ID.String(), CustomerID: r.CustomerID.String(), CustomerExternalID: r.CustomerExternalID,
		PlanVersionID: r.PlanVersionID.String(), Start: r.StartTime, End: r.EndTime, OpenEnded: r.OpenEnded, Created: r.CreatedAt,
		State: r.State, Interval: r.Interval, Currency: r.Currency, TrialEnds: r.TrialEndsAt, PausedAt: r.PausedAt,
		CancelAtPeriodEnd: r.CancelAtPeriodEnd, ChangedFromID: convert.UUIDStr(r.ChangedFromID), Provider: convert.Str(r.Provider), ProviderRef: convert.Str(r.ProviderRef), Owner: r.Owner, PlanCode: r.PlanCode, PlanVersion: r.PlanVersion, FeeTiming: r.FeeTiming, Now: now,
	})
}

// AttachExtras sets scheduled_change and coupon_code on the subscriptions that have a change waiting or a coupon on.
func AttachExtras(ctx context.Context, q *sqlc.Queries, subs ...*plansv1.Subscription) error {
	var ids []uuid.UUID
	byID := map[string]*plansv1.Subscription{}
	for _, sb := range subs {
		if id, err := uuid.Parse(sb.GetId()); err == nil {
			ids = append(ids, id)
			byID[sb.GetId()] = sb
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.ListPendingChangesFor(ctx, ids)
	if err != nil {
		return err
	}
	for _, c := range rows {
		if sb := byID[c.SubscriptionID.String()]; sb != nil {
			sb.ScheduledChange = &plansv1.ScheduledChange{PlanCode: c.PlanCode, PlanVersion: c.PlanVersion, Interval: convert.IntervalToProto(subscription.Interval(c.Interval)), EffectiveTime: timestamppb.New(c.EffectiveAt)}
		}
	}
	coupons, err := q.ListCouponCodesFor(ctx, ids)
	if err != nil {
		return err
	}
	for _, c := range coupons {
		if sb := byID[c.SubscriptionID.String()]; sb != nil {
			sb.CouponCode = c.CouponCode
		}
	}
	return nil
}

// Get returns a subscription with its scheduled change and coupon.
func (r *Queries) Get(ctx context.Context, id uuid.UUID) (*plansv1.Subscription, error) {
	var out *plansv1.Subscription
	err := r.db.Tx(ctx, func(q *sqlc.Queries) error {
		row, err := q.GetSubscription(ctx, id)
		if err != nil {
			return err
		}
		out = Message(row, r.now())
		return AttachExtras(ctx, q, out)
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return out, nil
}

// List returns a page of subscriptions, newest first, of one customer or of all.
func (r *Queries) List(ctx context.Context, req *plansv1.ListSubscriptionsRequest) (*plansv1.ListSubscriptionsResponse, error) {
	size := req.GetPageSize()
	switch {
	case size == 0:
		size = 50
	case size < 0 || size > 200:
		return nil, domain.Invalid("page_size", "1..200")
	}
	before := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	if tok := req.GetPageToken(); tok != "" {
		id, err := uuid.Parse(tok)
		if err != nil {
			return nil, domain.Invalid("page_token", "not a token this API gave")
		}
		before = id
	}
	resp := &plansv1.ListSubscriptionsResponse{}
	err := r.db.Tx(ctx, func(q *sqlc.Queries) error {
		customer := uuid.Nil
		if ext := req.GetCustomerExternalId(); ext != "" {
			c, err := q.GetCustomerByExternalID(ctx, ext)
			if err != nil {
				return convert.Err(err, "customer")
			}
			customer = c.ID
		}
		rows, err := q.ListSubscriptionsPage(ctx, sqlc.ListSubscriptionsPageParams{Before: before, CustomerID: customer, PageSize: size + 1})
		if err != nil {
			return err
		}
		more := len(rows) > int(size)
		if more {
			rows = rows[:size]
		}
		for _, row := range rows {
			resp.Subscriptions = append(resp.Subscriptions, Message(sqlc.GetSubscriptionRow(row), r.now()))
		}
		if more {
			resp.NextPageToken = rows[len(rows)-1].ID.String()
		}
		return AttachExtras(ctx, q, resp.Subscriptions...)
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return resp, nil
}

// ForPeriod sends every subscription whose range overlaps [period_start, period_end), in pages of 500 read in their own transactions.
func (r *Queries) ForPeriod(ctx context.Context, req *plansv1.ListSubscriptionsForPeriodRequest, send func(*plansv1.Subscription) error) error {
	if req.GetPeriodStart() == nil || req.GetPeriodEnd() == nil || !req.GetPeriodEnd().AsTime().After(req.GetPeriodStart().AsTime()) {
		return domain.Invalid("period_end", "period_start and period_end are required, end after start")
	}
	after := uuid.Nil
	for {
		var page []sqlc.ListSubscriptionsOverlappingRow
		err := r.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
			page, err = q.ListSubscriptionsOverlapping(ctx, sqlc.ListSubscriptionsOverlappingParams{
				PeriodStart: req.GetPeriodStart().AsTime(), PeriodEnd: req.GetPeriodEnd().AsTime(), After: after,
			})
			return err
		})
		if err != nil {
			return convert.Err(err, "subscription")
		}
		for _, row := range page {
			if err := send(Message(sqlc.GetSubscriptionRow(row), r.now())); err != nil {
				return err
			}
		}
		if len(page) < 500 {
			return nil
		}
		after = page[len(page)-1].ID
	}
}
