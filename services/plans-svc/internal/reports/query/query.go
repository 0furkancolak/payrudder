// Package query builds the subscription report: counts and monthly recurring revenue of a month.
package query

import (
	"context"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
)

// Queries builds reports.
type Queries struct {
	db  *store.DB
	now func() time.Time
}

// New returns the report queries.
func New(db *store.DB, now func() time.Time) *Queries { return &Queries{db: db, now: now} }

// monthBounds returns the label and the [start, end) of a month in loc; empty means the month now falls in.
func monthBounds(month string, loc *time.Location, now time.Time) (label string, start, end time.Time, err error) {
	if month == "" {
		month = period.PeriodOf(now, loc)
	}
	start, end, err = period.Bounds(month, loc)
	if err != nil {
		return "", start, end, domain.Invalid("month", "must look like 2026-10")
	}
	return month, start, end, nil
}

// Subscriptions reports the month's subscriptions, split by currency and, when asked, by provider and country.
//
// A subscription counts at the month's last instant if its range then contained that instant. What it was then (trialing, paused, past due) is
// only as good as what is stored: a trial that has since ended is known, a pause that has since been resumed is not, so for a past month the
// paused and past due counts are those of the subscriptions still in that state.
func (r *Queries) Subscriptions(ctx context.Context, req *plansv1.GetSubscriptionReportRequest) (*plansv1.GetSubscriptionReportResponse, error) {
	if _, err := tenant.Require(ctx); err != nil {
		return nil, err
	}
	var splits []string
	for _, g := range req.GetGroupBy() {
		if g != "provider" && g != "country" && g != "plan" {
			return nil, domain.Invalid("group_by", "provider, country and plan only (currency is always a split); got "+g)
		}
		if !slices.Contains(splits, g) {
			splits = append(splits, g)
		}
	}
	var t sqlc.CurrentTenantRow
	if err := r.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
		t, err = tenantsqry.Current(ctx, q)
		return err
	}); err != nil {
		return nil, convert.Err(err, "tenant")
	}
	loc, err := tenantsqry.Location(t.Timezone)
	if err != nil {
		return nil, err
	}
	label, start, end, err := monthBounds(req.GetMonth(), loc, r.now())
	if err != nil {
		return nil, err
	}
	resp := &plansv1.GetSubscriptionReportResponse{Month: label}
	err = r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.SubscriptionReport(ctx, sqlc.SubscriptionReportParams{
			MonthStart: start, MonthLast: end.Add(-time.Microsecond), MonthEnd: end, TenantCurrency: t.Currency,
			ByProvider: slices.Contains(splits, "provider"), ByCountry: slices.Contains(splits, "country"), ByPlan: slices.Contains(splits, "plan"),
		})
		for _, x := range rows {
			resp.Rows = append(resp.Rows, &plansv1.SubscriptionReportRow{
				Provider: x.Provider, Country: x.Country, PlanCode: x.PlanCode, Currency: x.Currency, Active: x.Active, Trialing: x.Trialing, Paused: x.Paused,
				PastDue: x.PastDue, MrrMinor: x.MrrMinor, NewSubscriptions: x.NewSubscriptions, NewMrrMinor: x.NewMrrMinor,
				Churned: x.Churned, ChurnedMrrMinor: x.ChurnedMrrMinor,
			})
		}
		return err
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return resp, nil
}

// Customers counts the tenant's customers, and those created in each of two ranges. A range is given as a pair of instants; an unset pair is empty.
func (r *Queries) Customers(ctx context.Context, req *plansv1.GetCustomerStatsRequest) (*plansv1.GetCustomerStatsResponse, error) {
	if _, err := tenant.Require(ctx); err != nil {
		return nil, err
	}
	zero := time.Unix(0, 0).UTC()
	bounds := func(from, to *timestamppb.Timestamp, field string) (time.Time, time.Time, error) {
		if from == nil && to == nil {
			return zero, zero, nil
		}
		if from == nil || to == nil || from.CheckValid() != nil || to.CheckValid() != nil || to.AsTime().Before(from.AsTime()) {
			return zero, zero, domain.Invalid(field, "both instants, the second not before the first")
		}
		return from.AsTime(), to.AsTime(), nil
	}
	from, to, err := bounds(req.GetFromTime(), req.GetToTime(), "from_time")
	if err != nil {
		return nil, err
	}
	pfrom, pto, err := bounds(req.GetPrevFromTime(), req.GetPrevToTime(), "prev_from_time")
	if err != nil {
		return nil, err
	}
	resp := &plansv1.GetCustomerStatsResponse{}
	err = r.db.Tx(ctx, func(q *sqlc.Queries) error {
		row, err := q.CustomerStats(ctx, sqlc.CustomerStatsParams{FromAt: from, ToAt: to, PrevFromAt: pfrom, PrevToAt: pto})
		resp.Total, resp.NewInRange, resp.NewInPrevious = row.Total, row.NewInRange, row.NewInPrevious
		return err
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return resp, nil
}
