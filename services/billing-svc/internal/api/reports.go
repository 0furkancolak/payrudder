package api

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
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
)

var periodRe = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

// revenueColumns maps what a report can be split by to the invoice column. Currency is always split.
var revenueColumns = map[string]string{"period": "i.period_id", "provider": "i.provider", "country": "COALESCE(i.country, '')"}

// GetRevenueReport implements billingv1.BillingServiceServer.
func (s *Server) GetRevenueReport(ctx context.Context, req *billingv1.GetRevenueReportRequest) (*billingv1.GetRevenueReportResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range []string{req.GetFromPeriod(), req.GetToPeriod()} {
		if p != "" && !periodRe.MatchString(p) {
			return nil, invalid("from_period and to_period must look like 2026-10")
		}
	}
	if req.GetFromPeriod() != "" && req.GetToPeriod() != "" && req.GetFromPeriod() > req.GetToPeriod() {
		return nil, invalid("from_period is after to_period")
	}
	groups := req.GetGroupBy()
	if len(groups) == 0 {
		groups = []string{"period"}
	}
	var splits []string
	for _, g := range groups {
		if _, ok := revenueColumns[g]; !ok {
			return nil, invalid("group_by may name period, provider and country; got " + g)
		}
		if !slices.Contains(splits, g) {
			splits = append(splits, g)
		}
	}
	slices.Sort(splits)
	selects, groupBy := []string{}, []string{}
	for _, name := range []string{"period", "provider", "country"} {
		if slices.Contains(splits, name) {
			selects = append(selects, revenueColumns[name])
			groupBy = append(groupBy, revenueColumns[name])
		} else {
			selects = append(selects, "''")
		}
	}
	// A refunded invoice is one whose saga passed through REFUNDED, whatever its status is now (voiding a paid invoice refunds it first).
	q := fmt.Sprintf(`
		SELECT %s, i.currency,
		  count(*) FILTER (WHERE i.status <> 'draft'),
		  COALESCE(sum(i.total_minor) FILTER (WHERE i.status IN ('open','paid','uncollectible')), 0),
		  COALESCE(sum(i.total_minor) FILTER (WHERE i.status = 'paid'), 0),
		  COALESCE(sum(i.total_minor) FILTER (WHERE r.invoice_id IS NOT NULL), 0),
		  COALESCE(sum(i.total_minor) FILTER (WHERE i.status = 'open'), 0),
		  COALESCE(sum(i.total_minor) FILTER (WHERE i.status = 'uncollectible'), 0),
		  COALESCE(sum(i.total_minor) FILTER (WHERE i.status = 'void'), 0),
		  COALESCE(sum(cn.credited), 0)
		FROM invoices i
		LEFT JOIN (SELECT invoice_id, sum(total_minor) AS credited FROM credit_notes GROUP BY invoice_id) cn ON cn.invoice_id = i.id
		LEFT JOIN (SELECT DISTINCT s.invoice_id FROM invoice_sagas s JOIN saga_events e ON e.tenant_id = s.tenant_id AND e.saga_id = s.id AND e.to_state = 'REFUNDED') r
		  ON r.invoice_id = i.id
		WHERE i.status <> 'draft' AND ($1 = '' OR i.period_id >= $1) AND ($2 = '' OR i.period_id <= $2)
		GROUP BY %s i.currency
		ORDER BY %s i.currency`, strings.Join(selects, ", "), prefix(groupBy), prefix(groupBy))
	resp := &billingv1.GetRevenueReportResponse{}
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, req.GetFromPeriod(), req.GetToPeriod())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r billingv1.RevenueRow
			if err := rows.Scan(&r.Period, &r.Provider, &r.Country, &r.Currency, &r.Invoices, &r.BilledMinor, &r.CollectedMinor, &r.RefundedMinor,
				&r.OutstandingMinor, &r.UncollectibleMinor, &r.VoidMinor, &r.CreditedMinor); err != nil {
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

// prefix renders the group-by expressions followed by a comma, or nothing.
func prefix(exprs []string) string {
	if len(exprs) == 0 {
		return ""
	}
	return strings.Join(exprs, ", ") + ","
}

// GetDunningReport implements billingv1.BillingServiceServer.
func (s *Server) GetDunningReport(ctx context.Context, req *billingv1.GetDunningReportRequest) (*billingv1.GetDunningReportResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	limit := int(req.GetLimit())
	switch {
	case limit == 0:
		limit = 100
	case limit < 0 || limit > 500:
		return nil, invalid("limit must be 1..500")
	}
	resp := &billingv1.GetDunningReportResponse{}
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		sum, err := tx.Query(ctx, `
			SELECT sg.state, i.provider, i.currency, count(*), COALESCE(sum(i.total_minor), 0)
			FROM invoice_sagas sg JOIN invoices i ON i.tenant_id = sg.tenant_id AND i.id = sg.invoice_id
			WHERE sg.state IN ('PAYMENT_DECLINED', 'PAYMENT_UNKNOWN', 'UNCOLLECTIBLE')
			GROUP BY 1, 2, 3 ORDER BY 1, 2, 3`)
		if err != nil {
			return err
		}
		for sum.Next() {
			var d billingv1.DunningSummary
			if err := sum.Scan(&d.State, &d.Provider, &d.Currency, &d.Invoices, &d.TotalMinor); err != nil {
				sum.Close()
				return err
			}
			resp.Summary = append(resp.Summary, &d)
		}
		sum.Close()
		if err := sum.Err(); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT i.id::text, COALESCE(i.number, ''), COALESCE(i.snapshot->>'customer_external_id', ''), i.period_id, i.provider, i.currency, i.total_minor,
			       sg.state, sg.dunning_attempt, sg.next_attempt_at, COALESCE(sg.last_error, '')
			FROM invoice_sagas sg JOIN invoices i ON i.tenant_id = sg.tenant_id AND i.id = sg.invoice_id
			WHERE sg.state IN ('PAYMENT_DECLINED', 'PAYMENT_UNKNOWN', 'UNCOLLECTIBLE')
			ORDER BY i.created_at, i.id LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d billingv1.DunningInvoice
			var next *time.Time
			if err := rows.Scan(&d.InvoiceId, &d.Number, &d.CustomerExternalId, &d.Period, &d.Provider, &d.Currency, &d.TotalMinor, &d.State, &d.DunningAttempt, &next, &d.LastError); err != nil {
				return err
			}
			if next != nil {
				d.NextAttemptTime = timestamppb.New(*next)
			}
			resp.Invoices = append(resp.Invoices, &d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return resp, nil
}
