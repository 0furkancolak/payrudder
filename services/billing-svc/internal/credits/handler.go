// Package credits serves the prepaid-credit methods of BillingService: it maps requests to commands and queries.
package credits

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/credits/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/credits/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/credits/query"
)

// Handler implements the credit methods of billingv1.BillingServiceServer.
type Handler struct {
	app pgxx.Beginner
}

// New returns the handler over the billing_app pool.
func New(app pgxx.Beginner) *Handler { return &Handler{app: app} }

// ListCreditPacks implements billingv1.BillingServiceServer.
func (h *Handler) ListCreditPacks(ctx context.Context, req *billingv1.ListCreditPacksRequest) (*billingv1.ListCreditPacksResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	packs, err := query.Packs(ctx, h.app, ti.ID, req.GetActiveOnly())
	if err != nil {
		return nil, err
	}
	out := &billingv1.ListCreditPacksResponse{Packs: make([]*billingv1.CreditPack, 0, len(packs))}
	for i := range packs {
		out.Packs = append(out.Packs, packProto(&packs[i]))
	}
	return out, nil
}

// CreateCreditPack implements billingv1.BillingServiceServer.
func (h *Handler) CreateCreditPack(ctx context.Context, req *billingv1.CreateCreditPackRequest) (*billingv1.CreateCreditPackResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	p, err := command.CreatePack(ctx, h.app, ti.ID, req.GetName(), req.GetCredits(), req.GetPriceMinor(), req.GetCurrency(), req.GetValidityDays())
	if err != nil {
		return nil, err
	}
	return &billingv1.CreateCreditPackResponse{Pack: packProto(p)}, nil
}

// SetCreditPackActive implements billingv1.BillingServiceServer.
func (h *Handler) SetCreditPackActive(ctx context.Context, req *billingv1.SetCreditPackActiveRequest) (*billingv1.SetCreditPackActiveResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, errkey.New(codes.NotFound, "billing.credits.pack_not_found", "no such credit pack")
	}
	if err := command.SetPackActive(ctx, h.app, ti.ID, id, req.GetActive()); err != nil {
		return nil, err
	}
	return &billingv1.SetCreditPackActiveResponse{}, nil
}

// GetCreditBalance implements billingv1.BillingServiceServer.
func (h *Handler) GetCreditBalance(ctx context.Context, req *billingv1.GetCreditBalanceRequest) (*billingv1.GetCreditBalanceResponse, error) {
	ti, cid, err := h.scope(ctx, req.GetCustomerId())
	if err != nil {
		return nil, err
	}
	b, err := query.BalanceOf(ctx, h.app, ti.ID, cid)
	if err != nil {
		return nil, err
	}
	out := &billingv1.GetCreditBalanceResponse{Balance: b.Credits, NextExpireTime: tsPtr(b.NextExpiry), Lots: make([]*billingv1.CreditLot, 0, len(b.Lots))}
	for _, l := range b.Lots {
		out.Lots = append(out.Lots, &billingv1.CreditLot{Id: l.ID.String(), Granted: l.Granted, Remaining: l.Remaining, Source: l.Source, ExpireTime: tsPtr(l.ExpiresAt), CreateTime: timestamppb.New(l.CreatedAt)})
	}
	return out, nil
}

// GrantCredits implements billingv1.BillingServiceServer.
func (h *Handler) GrantCredits(ctx context.Context, req *billingv1.GrantCreditsRequest) (*billingv1.GrantCreditsResponse, error) {
	ti, cid, err := h.scope(ctx, req.GetCustomerId())
	if err != nil {
		return nil, err
	}
	var exp *time.Time
	if t := req.GetExpireTime(); t != nil {
		v := t.AsTime()
		exp = &v
	}
	r, err := command.Grant(ctx, h.app, ti.ID, cid, req.GetCredits(), req.GetSource(), exp, req.GetReason(), req.GetIdempotencyKey())
	if err != nil {
		return nil, err
	}
	return &billingv1.GrantCreditsResponse{Balance: r.Balance, Replayed: r.Replayed}, nil
}

// ConsumeCredits implements billingv1.BillingServiceServer.
func (h *Handler) ConsumeCredits(ctx context.Context, req *billingv1.ConsumeCreditsRequest) (*billingv1.ConsumeCreditsResponse, error) {
	ti, cid, err := h.scope(ctx, req.GetCustomerId())
	if err != nil {
		return nil, err
	}
	r, err := command.Consume(ctx, h.app, ti.ID, cid, req.GetCredits(), req.GetReason(), req.GetIdempotencyKey(), req.GetCustomerExternalId())
	if err != nil {
		return nil, err
	}
	return &billingv1.ConsumeCreditsResponse{Balance: r.Balance, Replayed: r.Replayed}, nil
}

// ListCreditLedger implements billingv1.BillingServiceServer.
func (h *Handler) ListCreditLedger(ctx context.Context, req *billingv1.ListCreditLedgerRequest) (*billingv1.ListCreditLedgerResponse, error) {
	ti, cid, err := h.scope(ctx, req.GetCustomerId())
	if err != nil {
		return nil, err
	}
	entries, next, err := query.Ledger(ctx, h.app, ti.ID, cid, req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	out := &billingv1.ListCreditLedgerResponse{Entries: make([]*billingv1.CreditLedgerEntry, 0, len(entries)), NextPageToken: next}
	for _, e := range entries {
		out.Entries = append(out.Entries, &billingv1.CreditLedgerEntry{Id: e.ID.String(), LotId: e.LotID.String(), Kind: e.Kind, Amount: e.Amount, Reason: e.Reason, IdempotencyKey: e.Key, Source: e.Source, CreateTime: timestamppb.New(e.CreatedAt)})
	}
	return out, nil
}

// scope returns the caller's tenant and the customer the request is about.
func (h *Handler) scope(ctx context.Context, customerID string) (tenant.Info, uuid.UUID, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return tenant.Info{}, uuid.Nil, err
	}
	cid, err := uuid.Parse(customerID)
	if err != nil {
		return tenant.Info{}, uuid.Nil, errkey.New(codes.InvalidArgument, "billing.credits.bad_customer", "customer_id is not valid")
	}
	return ti, cid, nil
}

func packProto(p *model.Pack) *billingv1.CreditPack {
	out := &billingv1.CreditPack{Id: p.ID.String(), Name: p.Name, Credits: p.Credits, PriceMinor: p.PriceMinor, Currency: p.Currency, Active: p.Active, CreateTime: timestamppb.New(p.CreatedAt)}
	if p.ValidityDays != nil {
		out.ValidityDays = *p.ValidityDays
	}
	return out
}

// GetCreditSettings implements billingv1.BillingServiceServer.
func (h *Handler) GetCreditSettings(ctx context.Context, _ *billingv1.GetCreditSettingsRequest) (*billingv1.GetCreditSettingsResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	v, err := query.LowThreshold(ctx, h.app, ti.ID)
	if err != nil {
		return nil, err
	}
	return &billingv1.GetCreditSettingsResponse{LowThreshold: v}, nil
}

// SetCreditSettings implements billingv1.BillingServiceServer.
func (h *Handler) SetCreditSettings(ctx context.Context, req *billingv1.SetCreditSettingsRequest) (*billingv1.SetCreditSettingsResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	if err := command.SetLowThreshold(ctx, h.app, ti.ID, req.GetLowThreshold()); err != nil {
		return nil, err
	}
	return &billingv1.SetCreditSettingsResponse{LowThreshold: req.GetLowThreshold()}, nil
}

func tsPtr(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
