package query

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// PayoutMessage maps a payout row to its API message.
func PayoutMessage(r sqlc.ListAffiliatePayoutsRow) *plansv1.AffiliatePayout {
	p := &plansv1.AffiliatePayout{
		Id: r.ID.String(), AffiliateId: r.AffiliateID.String(), AffiliateCode: r.AffiliateCode, Currency: r.Currency, TotalMinor: r.TotalMinor, CommissionCount: r.CommissionCount,
		Method: r.Method, PayoutDetails: r.PayoutDetails, Reference: r.Reference, Status: r.Status, Fake: r.Sandbox, CreateTime: timestamppb.New(r.CreatedAt),
	}
	if r.PaidAt != nil {
		p.PayTime = timestamppb.New(*r.PaidAt)
	}
	if r.CancelledAt != nil {
		p.CancelTime = timestamppb.New(*r.CancelledAt)
	}
	return p
}

// PayoutNotFound is the error for a payout that does not exist (or is another tenant's).
func PayoutNotFound() error {
	return errkey.New(codes.NotFound, "affiliate.payout.not_found", "no such payout")
}

// Payouts lists payouts, newest first. An open one is worth what its commissions are worth now.
func (r *Queries) Payouts(ctx context.Context, req *plansv1.ListAffiliatePayoutsRequest) (*plansv1.ListAffiliatePayoutsResponse, error) {
	limit, offset, err := Page(req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	switch req.GetStatus() {
	case "", "open", "paid", "cancelled":
	default:
		return nil, errkey.New(codes.InvalidArgument, "affiliate.bad_status", "status is open, paid, cancelled or empty")
	}
	p := sqlc.ListAffiliatePayoutsParams{Status: NilIfEmpty(req.GetStatus()), RowLimit: limit, RowOffset: offset}
	aff, err := optionalUUID("affiliate_id", req.GetAffiliateId())
	if err != nil {
		return nil, err
	}
	id, err := optionalUUID("id", req.GetId())
	if err != nil {
		return nil, err
	}
	p.AffiliateID, p.PayoutID = aff, id
	out := &plansv1.ListAffiliatePayoutsResponse{}
	var total int64
	err = r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListAffiliatePayouts(ctx, p)
		if err != nil {
			return err
		}
		for _, row := range rows {
			total = row.Count
			out.Payouts = append(out.Payouts, PayoutMessage(row))
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "payout")
	}
	out.Total, out.NextPageToken = int32(total), NextToken(offset, limit, total) //nolint:gosec // a page of at most 200 rows
	return out, nil
}

// PayoutOf returns one payout as the list shows it.
func PayoutOf(ctx context.Context, q *sqlc.Queries, id string) (*plansv1.AffiliatePayout, error) {
	pid, err := optionalUUID("id", id)
	if err != nil || pid == nil {
		return nil, PayoutNotFound()
	}
	rows, err := q.ListAffiliatePayouts(ctx, sqlc.ListAffiliatePayoutsParams{PayoutID: pid, RowLimit: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, PayoutNotFound()
	}
	return PayoutMessage(rows[0]), nil
}

// Journal lists the postings of paid payouts and the debit and credit totals per currency over the same filter.
func (r *Queries) Journal(ctx context.Context, req *plansv1.ListAffiliateJournalRequest) (*plansv1.ListAffiliateJournalResponse, error) {
	limit, offset, err := Page(req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	payout, err := optionalUUID("payout_id", req.GetPayoutId())
	if err != nil {
		return nil, err
	}
	currency := NilIfEmpty(req.GetCurrency())
	from, to := optionalTime(req.GetFromTime()), optionalTime(req.GetToTime())
	out := &plansv1.ListAffiliateJournalResponse{}
	var total int64
	err = r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListAffiliateJournal(ctx, sqlc.ListAffiliateJournalParams{PayoutID: payout, Currency: currency, FromTime: from, ToTime: to, RowLimit: limit, RowOffset: offset})
		if err != nil {
			return err
		}
		for _, row := range rows {
			total = row.Count
			out.Entries = append(out.Entries, &plansv1.AffiliateJournalEntry{Id: row.ID.String(), PayoutId: row.PayoutID.String(), AffiliateId: row.AffiliateID.String(),
				PostTime: timestamppb.New(row.PostedAt), Currency: row.Currency, Account: row.Account, DebitMinor: row.DebitMinor, CreditMinor: row.CreditMinor, Memo: row.Memo, Fake: row.Sandbox})
		}
		totals, err := q.AffiliateJournalTotals(ctx, sqlc.AffiliateJournalTotalsParams{PayoutID: payout, Currency: currency, FromTime: from, ToTime: to})
		if err != nil {
			return err
		}
		for _, t := range totals {
			out.Totals = append(out.Totals, &plansv1.AffiliateJournalTotal{Currency: t.Currency, DebitMinor: t.DebitMinor, CreditMinor: t.CreditMinor})
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "journal")
	}
	out.Total, out.NextPageToken = int32(total), NextToken(offset, limit, total) //nolint:gosec // a page of at most 200 rows
	return out, nil
}
