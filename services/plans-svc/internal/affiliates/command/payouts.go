package command

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/model"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
)

// Journal accounts of a payout.
const (
	accountExpense = "commission_expense"
	accountPayable = "commissions_payable"
	accountCash    = "cash"

	payoutSkipBelowMinimum = "below_minimum"
	payoutSkipNotPositive  = "not_positive"
)

// Payouts follow the live gate of phase 45 (ADR 0022): a live environment that is not active (not activated yet, or suspended) pays nobody. A sandbox payout is
// fake and always allowed, because nothing is paid. It returns whether the tenant is a sandbox.
func payoutGate(ctx context.Context, q *sqlc.Queries) (sandbox bool, err error) {
	t, err := tenantsqry.Current(ctx, q)
	if err != nil {
		return false, err
	}
	e, err := env.Parse(t.Environment)
	if err != nil {
		return false, env.Refuse(err)
	}
	if e.IsLive() && t.Status != domain.StatusActive {
		return false, errkey.New(codes.FailedPrecondition, "affiliate.payout.tenant_not_active", "this live environment is not active: no payout can be made or paid")
	}
	return e.IsSandbox(), nil
}

type group struct {
	affiliate uuid.UUID
	currency  string
	total     int64
	ids       []uuid.UUID
}

// CreatePayouts batches the approved commissions that are in no payout yet. For each affiliate and currency the rows come to a total; at or above the program's
// minimum (and above zero) they become an open payout, or join the one already open; below it they are left for next time and reported. The rows are locked while
// this runs, so two payouts cannot take the same commission.
func (c *Commands) CreatePayouts(ctx context.Context, req *plansv1.CreateAffiliatePayoutsRequest) (*plansv1.CreateAffiliatePayoutsResponse, error) {
	var aff *uuid.UUID
	if req.GetAffiliateId() != "" {
		id, err := uuid.Parse(req.GetAffiliateId())
		if err != nil {
			return nil, query.NotFound()
		}
		aff = &id
	}
	var currency *string
	if cur := strings.ToUpper(strings.TrimSpace(req.GetCurrency())); cur != "" {
		currency = &cur
	}
	out := &plansv1.CreateAffiliatePayoutsResponse{}
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		sandbox, err := payoutGate(ctx, q)
		if err != nil {
			return err
		}
		prog, err := q.GetAffiliateProgram(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return query.NotSet()
		}
		if err != nil {
			return err
		}
		if aff != nil {
			a, err := q.GetAffiliate(ctx, *aff)
			if errors.Is(err, pgx.ErrNoRows) {
				return query.NotFound()
			}
			if err != nil {
				return err
			}
			if a.Status != model.AffiliateActive {
				return errkey.New(codes.FailedPrecondition, "affiliate.payout.affiliate_inactive", "a suspended or pending affiliate is not paid; their balance is kept")
			}
		}
		rows, err := q.LockPayableCommissions(ctx, sqlc.LockPayableCommissionsParams{AffiliateID: aff, Currency: currency})
		if err != nil {
			return err
		}
		var groups []*group
		index := map[string]*group{}
		for _, r := range rows {
			key := r.AffiliateID.String() + "|" + r.Currency
			g, ok := index[key]
			if !ok {
				g = &group{affiliate: r.AffiliateID, currency: r.Currency}
				index[key], groups = g, append(groups, g)
			}
			g.total += r.EffectiveMinor
			g.ids = append(g.ids, r.ID)
		}
		sort.SliceStable(groups, func(i, j int) bool {
			if groups[i].affiliate != groups[j].affiliate {
				return groups[i].affiliate.String() < groups[j].affiliate.String()
			}
			return groups[i].currency < groups[j].currency
		})
		for _, g := range groups {
			// What is approved after a payout was made joins it while it is open, whatever it comes to: the minimum was for the payout, not for each addition.
			payoutID, err := q.GetOpenAffiliatePayout(ctx, sqlc.GetOpenAffiliatePayoutParams{AffiliateID: g.affiliate, Currency: g.currency})
			if errors.Is(err, pgx.ErrNoRows) {
				if g.total <= 0 || g.total < prog.MinPayoutMinor {
					reason := payoutSkipBelowMinimum
					if g.total <= 0 {
						reason = payoutSkipNotPositive
					}
					out.Skipped = append(out.Skipped, &plansv1.SkippedAffiliatePayout{AffiliateId: g.affiliate.String(), Currency: g.currency, TotalMinor: g.total, Reason: reason})
					continue
				}
				a, err := q.GetAffiliate(ctx, g.affiliate)
				if err != nil {
					return err
				}
				payoutID, err = q.InsertAffiliatePayout(ctx, sqlc.InsertAffiliatePayoutParams{AffiliateID: g.affiliate, Currency: g.currency, TotalMinor: g.total,
					CommissionCount: int32(len(g.ids)), Method: a.PayoutMethod, PayoutDetails: a.PayoutDetails, Sandbox: sandbox}) //nolint:gosec // a batch of at most a few thousand rows
				if err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			if _, err := q.AssignCommissionsToPayout(ctx, sqlc.AssignCommissionsToPayoutParams{PayoutID: &payoutID, Ids: g.ids}); err != nil {
				return err
			}
			m, err := query.PayoutOf(ctx, q, payoutID.String())
			if err != nil {
				return err
			}
			out.Payouts = append(out.Payouts, m)
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "payout")
	}
	return out, nil
}

// MarkPaid records that the owner paid an open payout and posts it. What it comes to is worked out again here, from its rows as they are now: a refund since the
// payout was made is already out of it. The commissions become paid; the journal gets the commission expense against the payable, then the payable against cash.
// Marking a payout that is paid already changes nothing, so a repeated click or request posts once.
func (c *Commands) MarkPaid(ctx context.Context, req *plansv1.MarkAffiliatePayoutPaidRequest) (*plansv1.AffiliatePayout, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, query.PayoutNotFound()
	}
	ref := strings.TrimSpace(req.GetReference())
	if len(ref) > 200 {
		return nil, domain.Invalid("reference", "at most 200 characters")
	}
	var out *plansv1.AffiliatePayout
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		sandbox, err := payoutGate(ctx, q)
		if err != nil {
			return err
		}
		p, err := q.LockAffiliatePayout(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return query.PayoutNotFound()
		}
		if err != nil {
			return err
		}
		switch p.Status {
		case "paid":
			out, err = query.PayoutOf(ctx, q, id.String())
			return err
		case "cancelled":
			return errkey.New(codes.FailedPrecondition, "affiliate.payout.not_open", "this payout was cancelled")
		}
		a, err := q.GetAffiliate(ctx, p.AffiliateID)
		if err != nil {
			return err
		}
		if a.Status != model.AffiliateActive {
			return errkey.New(codes.FailedPrecondition, "affiliate.payout.affiliate_inactive", "a suspended or pending affiliate is not paid; their balance is kept")
		}
		if _, err := q.LockPayoutRows(ctx, &id); err != nil {
			return err
		}
		sum, err := q.SumPayoutRows(ctx, &id)
		if err != nil {
			return err
		}
		if sum.TotalMinor <= 0 {
			return errkey.New(codes.FailedPrecondition, "affiliate.payout.nothing_to_pay", "after refunds this payout comes to nothing: cancel it; the balance rolls forward")
		}
		now := c.now()
		if _, err := q.MarkPayoutCommissionsPaid(ctx, sqlc.MarkPayoutCommissionsPaidParams{PayoutID: &id, Now: now}); err != nil {
			return err
		}
		if err := q.MarkAffiliatePayoutPaid(ctx, sqlc.MarkAffiliatePayoutPaidParams{ID: id, Now: now, TotalMinor: sum.TotalMinor, CommissionCount: sum.RowsCount, Reference: ref}); err != nil {
			return err
		}
		if err := post(ctx, q, id, a, p.Currency, sum.TotalMinor, now, p.Sandbox || sandbox); err != nil {
			return err
		}
		out, err = query.PayoutOf(ctx, q, id.String())
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "payout")
	}
	return out, nil
}

// post writes the two sets of lines of a paid payout, each balanced in the payout's currency: the expense and what is now owed, then what is owed against cash.
// The database refuses to commit lines that do not balance.
func post(ctx context.Context, q *sqlc.Queries, payout uuid.UUID, a sqlc.GetAffiliateRow, currency string, total int64, at time.Time, fake bool) error {
	label := fmt.Sprintf("Commission payout to %s (%s)", a.Code, payout.String()[:8])
	if fake {
		label = "SANDBOX (fake): " + label
	}
	for _, l := range []struct {
		account       string
		debit, credit int64
		memo          string
	}{
		{accountExpense, total, 0, label + ": commissions earned"},
		{accountPayable, 0, total, label + ": owed to the affiliate"},
		{accountPayable, total, 0, label + ": settled"},
		{accountCash, 0, total, label + ": paid out"},
	} {
		if err := q.InsertJournalLine(ctx, sqlc.InsertJournalLineParams{PayoutID: payout, AffiliateID: a.ID, PostedAt: at, Currency: currency, Account: l.account,
			DebitMinor: l.debit, CreditMinor: l.credit, Memo: l.memo, Sandbox: fake}); err != nil {
			return err
		}
	}
	return nil
}

// CancelPayout drops an open payout; its commissions go back to being payable in another.
func (c *Commands) CancelPayout(ctx context.Context, req *plansv1.CancelAffiliatePayoutRequest) (*plansv1.AffiliatePayout, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, query.PayoutNotFound()
	}
	var out *plansv1.AffiliatePayout
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		p, err := q.LockAffiliatePayout(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return query.PayoutNotFound()
		}
		if err != nil {
			return err
		}
		switch p.Status {
		case "cancelled":
			out, err = query.PayoutOf(ctx, q, id.String())
			return err
		case "paid":
			return errkey.New(codes.FailedPrecondition, "affiliate.payout.not_open", "this payout was paid already")
		}
		if err := q.ReleasePayoutCommissions(ctx, &id); err != nil {
			return err
		}
		if err := q.CancelAffiliatePayout(ctx, sqlc.CancelAffiliatePayoutParams{ID: id, Now: c.now()}); err != nil {
			return err
		}
		out, err = query.PayoutOf(ctx, q, id.String())
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "payout")
	}
	return out, nil
}
