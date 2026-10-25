// Package query is what a partner reads of their own affiliations (phase 47). It runs for a person, not for a tenant: every call finds its row through the person's own
// partner record (affiliates.user_id), so another partner's affiliation is "not found". The figures themselves are read through the tenant's own queries, in that tenant's
// row-level-security scope, with the partner's id fixed. Nothing here shows the seller anything it did not already tell the partner: the customers are masked e-mail
// addresses, no invoice number or customer id appears.
package query

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/model"
	affq "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Queries reads a partner's own affiliations.
type Queries struct {
	sys *sqlc.Queries
	qry *affq.Queries
	now func() time.Time
}

// New returns the portal queries: pool is the system pool (a person has no tenant), qry reads a tenant's figures.
func New(systemPool *pgxpool.Pool, qry *affq.Queries, now func() time.Time) *Queries {
	return &Queries{sys: sqlc.New(systemPool), qry: qry, now: now}
}

// Affiliation maps a row to the message a partner sees. The link is filled in by the gateway.
func Affiliation(r sqlc.PortalListAffiliationsRow) *plansv1.MyAffiliation {
	return &plansv1.MyAffiliation{
		Id: r.ID.String(), TenantId: r.TenantID.String(), Environment: r.Environment, SellerName: r.SellerName, Code: r.Code, Status: r.Status, Name: r.Name, Email: r.Email,
		PayoutMethod: r.PayoutMethod, PayoutDetails: r.PayoutDetails, CreateTime: timestamppb.New(r.CreatedAt),
		Program: &plansv1.MyProgram{Status: r.ProgramStatus, CommissionKind: r.CommissionKind, CommissionValue: r.CommissionValue, Currency: r.ProgramCurrency, CommissionTerm: r.CommissionTerm,
			CommissionTermValue: r.CommissionTermValue, AttributionWindowDays: r.AttributionWindowDays, HoldDays: r.HoldDays, MinPayoutMinor: r.MinPayoutMinor},
	}
}

// Affiliations lists the person's affiliations, live ones first.
func (p *Queries) Affiliations(ctx context.Context, userID string) ([]*plansv1.MyAffiliation, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, nil
	}
	rows, err := p.sys.PortalListAffiliations(ctx, sqlc.PortalListAffiliationsParams{UserID: &uid})
	if err != nil {
		return nil, convert.Err(err, "affiliation")
	}
	out := make([]*plansv1.MyAffiliation, 0, len(rows))
	for _, r := range rows {
		out = append(out, Affiliation(r))
	}
	return out, nil
}

// own finds one affiliation of the person, and the context to read its tenant's figures in. Anything that is not the person's is the same "not found".
func (p *Queries) own(ctx context.Context, userID, affiliateID string) (sqlc.PortalListAffiliationsRow, context.Context, error) {
	uid, err1 := uuid.Parse(userID)
	aid, err2 := uuid.Parse(affiliateID)
	if err1 != nil || err2 != nil {
		return sqlc.PortalListAffiliationsRow{}, nil, affq.NotFound()
	}
	rows, err := p.sys.PortalListAffiliations(ctx, sqlc.PortalListAffiliationsParams{UserID: &uid, AffiliateID: &aid})
	if err != nil {
		return sqlc.PortalListAffiliationsRow{}, nil, convert.Err(err, "affiliation")
	}
	if len(rows) != 1 {
		return sqlc.PortalListAffiliationsRow{}, nil, affq.NotFound()
	}
	return rows[0], tenant.NewContext(ctx, tenant.Info{ID: rows[0].TenantID, Environment: rows[0].Environment}), nil
}

// Get returns an affiliation with the figures of a period and the earnings by status.
func (p *Queries) Get(ctx context.Context, req *plansv1.GetMyAffiliationRequest) (*plansv1.GetMyAffiliationResponse, error) {
	row, tctx, err := p.own(ctx, req.GetUserId(), req.GetAffiliateId())
	if err != nil {
		return nil, err
	}
	f, err := p.qry.Funnel(tctx, &plansv1.GetAffiliateFunnelRequest{FromDate: req.GetFromDate(), ToDate: req.GetToDate(), AffiliateId: row.ID.String()}, p.now())
	if err != nil {
		return nil, err
	}
	b, err := p.qry.Balances(tctx, row.ID.String())
	if err != nil {
		return nil, err
	}
	return &plansv1.GetMyAffiliationResponse{Affiliation: Affiliation(row), FromDate: f.GetFromDate(), ToDate: f.GetToDate(), Clicks: f.GetClicks(), Referrals: f.GetReferrals(),
		PayingCustomers: f.GetPayingCustomers(), Earned: f.GetCommissions(), Balances: b.GetBalances()}, nil
}

// Referrals lists who the partner brought, newest first, as masked e-mail addresses.
func (p *Queries) Referrals(ctx context.Context, req *plansv1.ListMyReferralsRequest) (*plansv1.ListMyReferralsResponse, error) {
	row, tctx, err := p.own(ctx, req.GetUserId(), req.GetAffiliateId())
	if err != nil {
		return nil, err
	}
	r, err := p.qry.Referrals(tctx, &plansv1.ListAffiliateReferralsRequest{AffiliateId: row.ID.String(), PageSize: req.GetPageSize(), PageToken: req.GetPageToken()})
	if err != nil {
		return nil, err
	}
	out := &plansv1.ListMyReferralsResponse{NextPageToken: r.GetNextPageToken(), Total: r.GetTotal()}
	for _, x := range r.GetReferrals() {
		out.Referrals = append(out.Referrals, &plansv1.MyReferral{CustomerEmailMasked: x.GetCustomerEmailMasked(), Source: x.GetSource(), AttributeTime: x.GetAttributeTime()})
	}
	return out, nil
}

// Commissions lists the partner's commissions and the negative rows that net them, newest first. The customer is a masked e-mail address; the seller's invoice and
// customer ids stay with the seller.
func (p *Queries) Commissions(ctx context.Context, req *plansv1.ListMyCommissionsRequest) (*plansv1.ListMyCommissionsResponse, error) {
	row, tctx, err := p.own(ctx, req.GetUserId(), req.GetAffiliateId())
	if err != nil {
		return nil, err
	}
	r, err := p.qry.CommissionRows(tctx, &plansv1.ListAffiliateCommissionsRequest{AffiliateId: row.ID.String(), Status: req.GetStatus(), PageSize: req.GetPageSize(), PageToken: req.GetPageToken()})
	if err != nil {
		return nil, err
	}
	out := &plansv1.ListMyCommissionsResponse{NextPageToken: r.NextPageToken, Total: r.Total}
	for _, c := range r.Rows {
		m := &plansv1.MyCommission{Id: c.ID.String(), Kind: c.Kind, CustomerEmailMasked: model.MaskEmail(c.CustomerEmail), Currency: c.Currency, AmountMinor: c.AmountMinor, ReversedMinor: c.ReversedMinor,
			Status: c.Status, EarnTime: timestamppb.New(c.EarnedAt), ApproveAfterTime: timestamppb.New(c.ApproveAfter)}
		if c.PaidAt != nil {
			m.PayTime = timestamppb.New(*c.PaidAt)
		}
		if c.PayoutID != nil {
			m.PayoutId = c.PayoutID.String()
		}
		out.Commissions = append(out.Commissions, m)
	}
	return out, nil
}

// Payouts lists the payouts made to the partner, newest first.
func (p *Queries) Payouts(ctx context.Context, req *plansv1.ListMyPayoutsRequest) (*plansv1.ListMyPayoutsResponse, error) {
	row, tctx, err := p.own(ctx, req.GetUserId(), req.GetAffiliateId())
	if err != nil {
		return nil, err
	}
	r, err := p.qry.Payouts(tctx, &plansv1.ListAffiliatePayoutsRequest{AffiliateId: row.ID.String(), PageSize: req.GetPageSize(), PageToken: req.GetPageToken()})
	if err != nil {
		return nil, err
	}
	out := &plansv1.ListMyPayoutsResponse{NextPageToken: r.GetNextPageToken(), Total: r.GetTotal()}
	for _, x := range r.GetPayouts() {
		out.Payouts = append(out.Payouts, &plansv1.MyPayout{Id: x.GetId(), Currency: x.GetCurrency(), TotalMinor: x.GetTotalMinor(), Status: x.GetStatus(), Reference: x.GetReference(), Fake: x.GetFake(),
			CreateTime: x.GetCreateTime(), PayTime: x.GetPayTime()})
	}
	return out, nil
}

// Program shows the terms of a seller's program to a person about to apply. A seller without an open program (none set, draft, paused, or a live environment that is not
// active) is "not found": a tenant id alone reveals nothing about whether it exists.
func (p *Queries) Program(ctx context.Context, userID, tenantID string) (*plansv1.LookupAffiliateProgramResponse, error) {
	uid, err1 := uuid.Parse(userID)
	tid, err2 := uuid.Parse(tenantID)
	if err1 != nil || err2 != nil {
		return nil, Unavailable()
	}
	r, err := p.sys.PortalProgram(ctx, sqlc.PortalProgramParams{UserID: &uid, TenantID: tid})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !Open(r)) {
		return nil, Unavailable()
	}
	if err != nil {
		return nil, convert.Err(err, "program")
	}
	return &plansv1.LookupAffiliateProgramResponse{SellerName: r.SellerName, Environment: r.Environment, Applied: r.Applied, Program: ProgramOf(r)}, nil
}
