package query

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// CommissionMessage maps a commission row to its API message.
func CommissionMessage(r sqlc.ListAffiliateCommissionsRow) *plansv1.AffiliateCommission {
	m := &plansv1.AffiliateCommission{
		Id: r.ID.String(), AffiliateId: r.AffiliateID.String(), AffiliateCode: r.AffiliateCode, CustomerExternalId: r.CustomerExternalID, Kind: r.Kind,
		InvoiceId: r.InvoiceID.String(), InvoiceNumber: r.InvoiceNumber, Currency: r.Currency, InvoiceTotalMinor: r.InvoiceTotalMinor, NetMinor: r.NetMinor,
		AmountMinor: r.AmountMinor, ReversedMinor: r.ReversedMinor, Status: r.Status, EarnTime: timestamppb.New(r.EarnedAt), ApproveAfterTime: timestamppb.New(r.ApproveAfter),
	}
	if r.ParentID != nil {
		m.ParentId = r.ParentID.String()
	}
	if r.ApprovedAt != nil {
		m.ApproveTime = timestamppb.New(*r.ApprovedAt)
	}
	if r.PaidAt != nil {
		m.PayTime = timestamppb.New(*r.PaidAt)
	}
	if r.PayoutID != nil {
		m.PayoutId = r.PayoutID.String()
	}
	return m
}

func optionalUUID(field, s string) (*uuid.UUID, error) {
	if s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil, errkey.Newf(codes.InvalidArgument, "affiliate.bad_id", "%s is not an id", field)
	}
	return &id, nil
}

func optionalTime(t *timestamppb.Timestamp) *time.Time {
	if t == nil {
		return nil
	}
	v := t.AsTime()
	return &v
}

// Commissions lists commissions and reversal rows, newest first, with filters.
func (r *Queries) Commissions(ctx context.Context, req *plansv1.ListAffiliateCommissionsRequest) (*plansv1.ListAffiliateCommissionsResponse, error) {
	page, err := r.CommissionRows(ctx, req)
	if err != nil {
		return nil, err
	}
	out := &plansv1.ListAffiliateCommissionsResponse{Total: page.Total, NextPageToken: page.NextPageToken}
	for _, row := range page.Rows {
		out.Commissions = append(out.Commissions, CommissionMessage(row))
	}
	return out, nil
}

// CommissionPage is a page of commission rows as stored, for a caller that shapes them itself (the partner's portal masks the customer).
type CommissionPage struct {
	Rows          []sqlc.ListAffiliateCommissionsRow
	Total         int32
	NextPageToken string
}

// CommissionRows is Commissions before the rows become messages.
func (r *Queries) CommissionRows(ctx context.Context, req *plansv1.ListAffiliateCommissionsRequest) (*CommissionPage, error) {
	limit, offset, err := Page(req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	switch req.GetStatus() {
	case "", "pending", "approved", "paid", "reversed":
	default:
		return nil, errkey.New(codes.InvalidArgument, "affiliate.bad_status", "status is pending, approved, paid, reversed or empty")
	}
	switch req.GetKind() {
	case "", "commission", "reversal":
	default:
		return nil, errkey.New(codes.InvalidArgument, "affiliate.bad_kind", "kind is commission, reversal or empty")
	}
	p := sqlc.ListAffiliateCommissionsParams{Status: NilIfEmpty(req.GetStatus()), Kind: NilIfEmpty(req.GetKind()), FromTime: optionalTime(req.GetFromTime()),
		ToTime: optionalTime(req.GetToTime()), RowLimit: limit, RowOffset: offset}
	for _, f := range []struct {
		dst   **uuid.UUID
		name  string
		value string
	}{{&p.AffiliateID, "affiliate_id", req.GetAffiliateId()}, {&p.InvoiceID, "invoice_id", req.GetInvoiceId()}, {&p.PayoutID, "payout_id", req.GetPayoutId()}} {
		id, err := optionalUUID(f.name, f.value)
		if err != nil {
			return nil, err
		}
		*f.dst = id
	}
	out := &CommissionPage{}
	var total int64
	err = r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListAffiliateCommissions(ctx, p)
		if err != nil {
			return err
		}
		for _, row := range rows {
			total = row.Count
		}
		out.Rows = rows
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "commission")
	}
	out.Total, out.NextPageToken = int32(total), NextToken(offset, limit, total) //nolint:gosec // a page of at most 200 rows
	return out, nil
}

// Balances is what each affiliate has per currency.
func (r *Queries) Balances(ctx context.Context, affiliateID string) (*plansv1.GetAffiliateBalancesResponse, error) {
	id, err := optionalUUID("affiliate_id", affiliateID)
	if err != nil {
		return nil, err
	}
	out := &plansv1.GetAffiliateBalancesResponse{}
	err = r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.AffiliateBalances(ctx, id)
		if err != nil {
			return err
		}
		for _, row := range rows {
			out.Balances = append(out.Balances, &plansv1.AffiliateBalance{AffiliateId: row.AffiliateID.String(), Currency: row.Currency, PendingMinor: row.PendingMinor,
				ApprovedMinor: row.ApprovedMinor, PaidMinor: row.PaidMinor, ReversedMinor: row.ReversedMinor})
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "balance")
	}
	return out, nil
}

// Funnel counts clicks, referred customers, paying customers and commissions over a period of UTC days (both ends included; the default is the last 30 days).
func (r *Queries) Funnel(ctx context.Context, req *plansv1.GetAffiliateFunnelRequest, now time.Time) (*plansv1.GetAffiliateFunnelResponse, error) {
	to, err := day(req.GetToDate(), now.UTC())
	if err != nil {
		return nil, err
	}
	from, err := day(req.GetFromDate(), to.AddDate(0, 0, -30))
	if err != nil {
		return nil, err
	}
	if from.After(to) || to.Sub(from) > 366*24*time.Hour {
		return nil, errkey.New(codes.InvalidArgument, "affiliate.bad_period", "the period must run forward and be at most a year")
	}
	aff, err := optionalUUID("affiliate_id", req.GetAffiliateId())
	if err != nil {
		return nil, err
	}
	end := to.AddDate(0, 0, 1)
	out := &plansv1.GetAffiliateFunnelResponse{FromDate: from.Format(time.DateOnly), ToDate: to.Format(time.DateOnly)}
	err = r.db.Tx(ctx, func(q *sqlc.Queries) error {
		var err error
		if out.Clicks, err = q.AffiliateFunnelClicks(ctx, sqlc.AffiliateFunnelClicksParams{FromDay: pgDate(from), ToDay: pgDate(to), AffiliateID: aff}); err != nil {
			return err
		}
		if out.Referrals, err = q.AffiliateFunnelReferrals(ctx, sqlc.AffiliateFunnelReferralsParams{FromTime: from, ToTime: end, AffiliateID: aff}); err != nil {
			return err
		}
		if out.PayingCustomers, err = q.AffiliateFunnelPaying(ctx, sqlc.AffiliateFunnelPayingParams{FromTime: from, ToTime: end, AffiliateID: aff}); err != nil {
			return err
		}
		rows, err := q.AffiliateFunnelCommission(ctx, sqlc.AffiliateFunnelCommissionParams{FromTime: from, ToTime: end, AffiliateID: aff})
		if err != nil {
			return err
		}
		for _, row := range rows {
			out.Commissions = append(out.Commissions, &plansv1.AffiliateFunnelAmount{Currency: row.Currency, EarnedMinor: row.EarnedMinor, ReversedMinor: row.ReversedMinor})
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "funnel")
	}
	return out, nil
}

func pgDate(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

// day reads a YYYY-MM-DD date as the start of that UTC day; empty is def truncated to its day.
func day(s string, def time.Time) (time.Time, error) {
	if s == "" {
		return time.Date(def.Year(), def.Month(), def.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, errkey.New(codes.InvalidArgument, "affiliate.bad_period", "dates are written YYYY-MM-DD")
	}
	return t, nil
}

// CommissionOfInvoice returns the commission of an invoice as the owner sees it, or affiliate.commission.not_found.
func CommissionOfInvoice(ctx context.Context, q *sqlc.Queries, invoiceID uuid.UUID) (*plansv1.AffiliateCommission, error) {
	kind := "commission"
	rows, err := q.ListAffiliateCommissions(ctx, sqlc.ListAffiliateCommissionsParams{InvoiceID: &invoiceID, Kind: &kind, RowLimit: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errkey.New(codes.NotFound, "affiliate.commission.not_found", "this invoice earned no commission")
	}
	return CommissionMessage(rows[0]), nil
}
