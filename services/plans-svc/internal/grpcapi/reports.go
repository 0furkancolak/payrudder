package grpcapi

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

var monthRe = regexp.MustCompile(`^([0-9]{4})-(0[1-9]|1[0-2])$`)

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

// GetSubscriptionReport implements plansv1.PlansServiceServer.
//
// A subscription counts at the month's last instant if its range then contained that instant. What it was then (trialing, paused, past due) is
// only as good as what is stored: a trial that has since ended is known, a pause that has since been resumed is not, so for a past month the
// paused and past due counts are those of the subscriptions still in that state.
func (s *Server) GetSubscriptionReport(ctx context.Context, req *plansv1.GetSubscriptionReportRequest) (*plansv1.GetSubscriptionReportResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
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
	selects, group := []string{}, []string{}
	for _, name := range []string{"provider", "country"} {
		if slices.Contains(splits, name) {
			selects = append(selects, name)
			group = append(group, name)
		} else {
			selects = append(selects, "''")
		}
	}
	groupBy := ""
	if len(group) > 0 {
		groupBy = strings.Join(group, ", ") + ", "
	}
	var t sqlc.CurrentTenantRow
	if err := s.tx(ctx, func(q *sqlc.Queries) (err error) {
		t, err = current(ctx, q)
		return err
	}); err != nil {
		return nil, convert.Err(err, "tenant")
	}
	loc, err := location(t.Timezone)
	if err != nil {
		return nil, err
	}
	label, start, end, err := monthBounds(req.GetMonth(), loc, s.now())
	if err != nil {
		return nil, err
	}
	resp := &plansv1.GetSubscriptionReportResponse{Month: label}
	err = pgxx.InTenantTx(ctx, s.pool, ti.ID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, fmt.Sprintf(subscriptionReportSQL, strings.Join(selects, ", "), groupBy, groupBy), start, end.Add(-time.Microsecond), end, t.Currency)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r plansv1.SubscriptionReportRow
			if err := rows.Scan(&r.Provider, &r.Country, &r.Currency, &r.Active, &r.Trialing, &r.Paused, &r.PastDue, &r.MrrMinor,
				&r.NewSubscriptions, &r.NewMrrMinor, &r.Churned, &r.ChurnedMrrMinor); err != nil {
				return err
			}
			resp.Rows = append(resp.Rows, &r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return resp, nil
}

// subscriptionReportSQL: $1 month start, $2 the month's last instant, $3 month end, $4 the tenant currency. The first %s is the provider and
// country columns (or ” for a dimension that is not split), the others the GROUP BY / ORDER BY prefix.
const subscriptionReportSQL = `
WITH subs AS (
  SELECT s.id, s.currency, lower(s.active) AS starts, upper(s.active) AS ends, s.changed_from_id,
         COALESCE(s.provider, '') AS provider, COALESCE(c.country, '') AS country,
         CASE WHEN s.state = 'paused' THEN 'paused'
              WHEN s.state = 'past_due' THEN 'past_due'
              WHEN s.state = 'trialing' AND s.trial_ends_at > $2 THEN 'trialing'
              ELSE 'active' END AS eff,
         CASE WHEN s.interval = 'year' THEN ROUND(COALESCE(f.base_fee_minor, 0) / 12.0)::bigint
              WHEN s.currency = $4 THEN pv.base_fee_minor ELSE COALESCE(f.base_fee_minor, 0) END AS fee,
         EXISTS (SELECT 1 FROM subscriptions n WHERE n.tenant_id = s.tenant_id AND n.changed_from_id = s.id) AS has_successor
  FROM subscriptions s
  JOIN customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id
  JOIN plan_versions pv ON pv.tenant_id = s.tenant_id AND pv.id = s.plan_version_id
  LEFT JOIN plan_version_fees f ON f.tenant_id = s.tenant_id AND f.plan_version_id = s.plan_version_id AND f.currency = s.currency AND f.interval = s.interval
  WHERE s.tenant_id = app_tenant_id()
), flagged AS (
  SELECT *,
         (starts <= $2 AND (ends IS NULL OR ends > $2)) AS alive,
         (starts >= $1 AND starts < $3 AND changed_from_id IS NULL) AS is_new,
         (ends IS NOT NULL AND ends >= $1 AND ends < $3 AND NOT has_successor) AS is_churned
  FROM subs
)
SELECT %s, currency,
  count(*) FILTER (WHERE alive AND eff = 'active'),
  count(*) FILTER (WHERE alive AND eff = 'trialing'),
  count(*) FILTER (WHERE alive AND eff = 'paused'),
  count(*) FILTER (WHERE alive AND eff = 'past_due'),
  COALESCE(sum(fee) FILTER (WHERE alive AND eff IN ('active', 'past_due')), 0),
  count(*) FILTER (WHERE is_new),
  COALESCE(sum(fee) FILTER (WHERE is_new AND eff IN ('active', 'past_due')), 0),
  count(*) FILTER (WHERE is_churned),
  COALESCE(sum(fee) FILTER (WHERE is_churned), 0)
FROM flagged
WHERE alive OR is_new OR is_churned
GROUP BY %scurrency
ORDER BY %scurrency`
