// Package query builds the subscription report: counts and monthly recurring revenue of a month.
package query

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
	tenantsqry "github.com/0furkancolak/quotify/services/plans-svc/internal/tenants/query"
)

var monthRe = regexp.MustCompile(`^([0-9]{4})-(0[1-9]|1[0-2])$`)

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
		y, m, _ := now.In(loc).Date()
		start = time.Date(y, m, 1, 0, 0, 0, 0, loc)
	} else {
		m := monthRe.FindStringSubmatch(month)
		if m == nil {
			return "", start, end, domain.Invalid("month", "must look like 2026-10")
		}
		var y, mo int
		_, _ = fmt.Sscanf(m[1]+" "+m[2], "%d %d", &y, &mo)
		start = time.Date(y, time.Month(mo), 1, 0, 0, 0, 0, loc)
	}
	end = start.AddDate(0, 1, 0)
	return start.Format("2006-01"), start, end, nil
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
		if g != "provider" && g != "country" {
			return nil, domain.Invalid("group_by", "provider and country only (currency is always a split); got "+g)
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
			ByProvider: slices.Contains(splits, "provider"), ByCountry: slices.Contains(splits, "country"),
		})
		for _, x := range rows {
			resp.Rows = append(resp.Rows, &plansv1.SubscriptionReportRow{
				Provider: x.Provider, Country: x.Country, Currency: x.Currency, Active: x.Active, Trialing: x.Trialing, Paused: x.Paused,
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
