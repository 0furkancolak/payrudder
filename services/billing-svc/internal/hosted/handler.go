// Package hosted serves the public links of invoices: payment links and customer portal links (BillingService: creating, listing, revoking) and what a visitor
// sees and does with them (HostedInvoiceService). It maps requests to commands and queries.
package hosted

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/money"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/hosted/command"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/hosted/query"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/invoices"
	invoicequery "github.com/0furkancolak/quotify/services/billing-svc/internal/invoices/query"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// Handler implements the link methods of billingv1.BillingServiceServer and billingv1.HostedInvoiceServiceServer.
type Handler struct {
	d command.Deps
	q query.Deps
}

// New returns the handler over the billing_app and billing_system pools. mgr makes the invoices of ad-hoc links and looks up customers and sellers; eng drives
// their sagas.
func New(app, system *pgxpool.Pool, mgr *runs.Manager, eng *saga.Engine) *Handler {
	h := &Handler{d: command.Deps{App: app, System: system, Engine: eng}, q: query.Deps{App: app, System: system}}
	if mgr != nil {
		h.d.Dir = mgr
		h.q.Seller = func(ctx context.Context, tenantID uuid.UUID) (query.Seller, error) {
			s, err := mgr.Seller(ctx, tenantID)
			return query.Seller{Name: s.Name, LogoKey: s.LogoKey, Environment: s.Environment}, err
		}
	} else {
		h.q.Seller = func(context.Context, uuid.UUID) (query.Seller, error) {
			return query.Seller{}, status.Error(codes.Unavailable, "seller lookup unavailable")
		}
	}
	return h
}

// WithPDFs enables the PDF and logo links of the hosted pages.
func (h *Handler) WithPDFs(p invoicequery.PDFSigner) *Handler {
	h.q.PDFs = p
	return h
}

// WithDemo allows the mock payment for live tenants too (QUOTIFY_DEMO).
func (h *Handler) WithDemo(demo bool) *Handler {
	h.d.Demo = demo
	return h
}

func parseUUID(field, v string) (uuid.UUID, error) {
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.Nil, status.Error(codes.InvalidArgument, field+" must be a UUID")
	}
	return id, nil
}

// CreateInvoiceLink implements billingv1.BillingServiceServer.
func (h *Handler) CreateInvoiceLink(ctx context.Context, req *billingv1.CreateInvoiceLinkRequest) (*billingv1.CreateInvoiceLinkResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseUUID("invoice_id", req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	token, expires, err := command.CreateInvoiceLink(ctx, h.d, ti.ID, id, req.GetExpiresInDays())
	if err != nil {
		return nil, err
	}
	return &billingv1.CreateInvoiceLinkResponse{Token: token, ExpireTime: timestamppb.New(expires)}, nil
}

// RevokeInvoiceLinks implements billingv1.BillingServiceServer.
func (h *Handler) RevokeInvoiceLinks(ctx context.Context, req *billingv1.RevokeInvoiceLinksRequest) (*billingv1.RevokeInvoiceLinksResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseUUID("invoice_id", req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	n, err := command.RevokeLinks(ctx, h.d, ti.ID, id)
	if err != nil {
		return nil, err
	}
	return &billingv1.RevokeInvoiceLinksResponse{Revoked: int32(n)}, nil //nolint:gosec // a handful of links
}

// CreatePaymentLink implements billingv1.BillingServiceServer.
func (h *Handler) CreatePaymentLink(ctx context.Context, req *billingv1.CreatePaymentLinkRequest) (*billingv1.CreatePaymentLinkResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	in := command.CreateInput{
		Kind: req.GetKind(), AmountMinor: req.GetAmountMinor(), Currency: req.GetCurrency(), Description: req.GetDescription(), CustomerExternalID: req.GetCustomerExternalId(),
		SingleUse: req.SingleUse, ExpiresInDays: req.GetExpiresInDays(), IdempotencyKey: req.GetIdempotencyKey(), RequireOpen: true, Quantity: req.GetQuantity(),
	}
	if req.GetPackId() != "" {
		if in.PackID, err = parseUUID("pack_id", req.GetPackId()); err != nil {
			return nil, err
		}
	}
	if req.GetInvoiceId() != "" {
		if in.InvoiceID, err = parseUUID("invoice_id", req.GetInvoiceId()); err != nil {
			return nil, err
		}
	}
	c, err := command.CreateLink(ctx, h.d, ti.ID, in)
	if err != nil {
		return nil, err
	}
	return &billingv1.CreatePaymentLinkResponse{Link: linkProto(c.Link), Token: c.Token, Replayed: c.Replayed}, nil
}

// GetPaymentLink implements billingv1.BillingServiceServer.
func (h *Handler) GetPaymentLink(ctx context.Context, req *billingv1.GetPaymentLinkRequest) (*billingv1.GetPaymentLinkResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetLinkId())
	if err != nil {
		return nil, errkey.New(codes.NotFound, "billing.payment_link.not_found", "no such payment link")
	}
	row, err := query.GetLink(ctx, h.d.App, ti.ID, id)
	if err != nil {
		return nil, err
	}
	return &billingv1.GetPaymentLinkResponse{Link: linkProto(row)}, nil
}

// ListPaymentLinks implements billingv1.BillingServiceServer.
func (h *Handler) ListPaymentLinks(ctx context.Context, req *billingv1.ListPaymentLinksRequest) (*billingv1.ListPaymentLinksResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	page, err := query.ListLinks(ctx, h.d.App, ti.ID, query.ListParams{Kind: req.GetKind(), Status: req.GetStatus(), InvoiceID: req.GetInvoiceId(),
		CustomerExternalID: req.GetCustomerExternalId(), PageSize: int(req.GetPageSize()), PageToken: req.GetPageToken()})
	if err != nil {
		return nil, err
	}
	resp := &billingv1.ListPaymentLinksResponse{NextPageToken: page.NextPageToken}
	for _, r := range page.Links {
		resp.Links = append(resp.Links, linkProto(sqlc.GetPaymentLinkRowRow(r)))
	}
	return resp, nil
}

// RevokePaymentLink implements billingv1.BillingServiceServer.
func (h *Handler) RevokePaymentLink(ctx context.Context, req *billingv1.RevokePaymentLinkRequest) (*billingv1.RevokePaymentLinkResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetLinkId())
	if err != nil {
		return nil, errkey.New(codes.NotFound, "billing.payment_link.not_found", "no such payment link")
	}
	row, err := command.RevokeLink(ctx, h.d, ti.ID, id)
	if err != nil {
		return nil, err
	}
	return &billingv1.RevokePaymentLinkResponse{Link: linkProto(row)}, nil
}

// CreatePortalLink implements billingv1.BillingServiceServer.
func (h *Handler) CreatePortalLink(ctx context.Context, req *billingv1.CreatePortalLinkRequest) (*billingv1.CreatePortalLinkResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	token, expires, customer, err := command.CreatePortalLink(ctx, h.d, ti.ID, req.GetCustomerExternalId(), req.GetExpiresInDays())
	if err != nil {
		return nil, err
	}
	return &billingv1.CreatePortalLinkResponse{Token: token, ExpireTime: timestamppb.New(expires), CustomerId: customer.String()}, nil
}

// RevokePortalLinks implements billingv1.BillingServiceServer.
func (h *Handler) RevokePortalLinks(ctx context.Context, req *billingv1.RevokePortalLinksRequest) (*billingv1.RevokePortalLinksResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	n, err := command.RevokePortalLinks(ctx, h.d, ti.ID, req.GetCustomerExternalId())
	if err != nil {
		return nil, err
	}
	return &billingv1.RevokePortalLinksResponse{Revoked: int32(n)}, nil //nolint:gosec // a handful of links
}

// SettleInvoicePayment implements billingv1.BillingServiceServer.
func (h *Handler) SettleInvoicePayment(ctx context.Context, req *billingv1.SettleInvoicePaymentRequest) (*billingv1.SettleInvoicePaymentResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseUUID("invoice_id", req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	settled, err := command.SettleInvoicePayment(ctx, h.d, ti.ID, command.SettleInput{
		InvoiceID: id, Provider: req.GetProvider(), ProviderRef: req.GetProviderRef(), AmountMinor: req.GetAmountMinor(), Currency: req.GetCurrency()})
	if err != nil {
		return nil, err
	}
	return &billingv1.SettleInvoicePaymentResponse{Settled: settled}, nil
}

// GetHostedInvoice implements billingv1.HostedInvoiceServiceServer.
func (h *Handler) GetHostedInvoice(ctx context.Context, req *billingv1.GetHostedInvoiceRequest) (*billingv1.GetHostedInvoiceResponse, error) {
	scope, err := query.Resolve(ctx, h.q.System, req.GetToken(), req.GetPortalToken(), req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	page, err := query.Get(ctx, h.q, scope)
	if err != nil {
		return nil, err
	}
	return pageProto(scope, page), nil
}

// GetHostedInvoicePdfUrl implements billingv1.HostedInvoiceServiceServer.
func (h *Handler) GetHostedInvoicePdfUrl(ctx context.Context, req *billingv1.GetHostedInvoicePdfUrlRequest) (*billingv1.GetHostedInvoicePdfUrlResponse, error) {
	scope, err := query.Resolve(ctx, h.q.System, req.GetToken(), req.GetPortalToken(), req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	url, expires, err := query.PDFURL(ctx, h.q, scope)
	if err != nil {
		return nil, err
	}
	return &billingv1.GetHostedInvoicePdfUrlResponse{Url: url, ExpireTime: timestamppb.New(expires)}, nil
}

// GetHostedLogoUrl implements billingv1.HostedInvoiceServiceServer.
func (h *Handler) GetHostedLogoUrl(ctx context.Context, req *billingv1.GetHostedLogoUrlRequest) (*billingv1.GetHostedLogoUrlResponse, error) {
	scope, err := query.Resolve(ctx, h.q.System, req.GetToken(), req.GetPortalToken(), req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	url, err := query.LogoURL(ctx, h.q, scope)
	if err != nil {
		return nil, err
	}
	return &billingv1.GetHostedLogoUrlResponse{Url: url}, nil
}

// StartLinkPayment implements billingv1.HostedInvoiceServiceServer.
func (h *Handler) StartLinkPayment(ctx context.Context, req *billingv1.StartLinkPaymentRequest) (*billingv1.StartLinkPaymentResponse, error) {
	token, err := command.StartLinkPayment(ctx, h.d, req.GetToken())
	if err != nil {
		return nil, err
	}
	return &billingv1.StartLinkPaymentResponse{InvoiceToken: token}, nil
}

// RequestHostedPayment implements billingv1.HostedInvoiceServiceServer.
func (h *Handler) RequestHostedPayment(ctx context.Context, req *billingv1.RequestHostedPaymentRequest) (*billingv1.RequestHostedPaymentResponse, error) {
	scope, err := query.Resolve(ctx, h.q.System, req.GetToken(), req.GetPortalToken(), req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	paid, err := command.RequestPayment(ctx, h.d, h.q, command.PaymentRequest{Scope: scope, Method: req.GetMethod()})
	if err != nil {
		return nil, err
	}
	if paid.Started {
		return &billingv1.RequestHostedPaymentResponse{Action: "started"}, nil
	}
	return &billingv1.RequestHostedPaymentResponse{Action: "provider", Payment: paymentContext(scope.TenantID, paid.Page)}, nil
}

// GetPortal implements billingv1.HostedInvoiceServiceServer.
func (h *Handler) GetPortal(ctx context.Context, req *billingv1.GetPortalRequest) (*billingv1.GetPortalResponse, error) {
	scope, err := query.Resolve(ctx, h.q.System, "", req.GetPortalToken(), "")
	if err != nil {
		return nil, err
	}
	page, err := query.GetPortal(ctx, h.q, scope)
	if err != nil {
		return nil, err
	}
	resp := &billingv1.GetPortalResponse{
		CustomerName: page.CustomerName, SellerName: page.SellerName, LogoAvailable: page.LogoAvailable, Environment: page.Environment,
		ExpireTime: timestamppb.New(scope.ExpiresAt), TenantId: scope.TenantID.String(), CustomerId: scope.Portal.CustomerID.String(),
	}
	for _, i := range page.Invoices {
		r := i.Row
		resp.Invoices = append(resp.Invoices, &billingv1.PortalInvoice{
			Id: r.ID.String(), Number: r.Number, Status: r.Status, PaymentState: i.State, Payable: i.Pay, TotalMinor: r.TotalMinor, Currency: r.Currency,
			CurrencyExponent: r.CurrencyExponent, IssueTime: timestamppb.New(r.IssuedAt), Kind: r.Kind, PdfAvailable: r.PdfReady && h.q.PDFs != nil,
		})
	}
	resp.CreditBalance, resp.CanBuyCredits = page.CreditBalance, page.CanBuy
	for _, l := range page.CreditLots {
		lot := &billingv1.PortalCreditLot{Granted: l.Granted, Remaining: l.Remaining}
		if l.ExpiresAt != nil {
			lot.ExpireTime = timestamppb.New(*l.ExpiresAt)
		}
		resp.CreditLots = append(resp.CreditLots, lot)
	}
	for _, p := range page.CreditPacks {
		exp := int32(2)
		if c, ok := money.LookupCurrency(p.Currency); ok {
			exp = int32(c.Exponent) //nolint:gosec // 0..3
		}
		pack := &billingv1.PortalCreditPack{Id: p.ID.String(), Name: p.Name, Credits: p.Credits, PriceMinor: p.PriceMinor, Currency: p.Currency, CurrencyExponent: exp}
		if p.ValidityDays != nil {
			pack.ValidityDays = *p.ValidityDays
		}
		resp.CreditPacks = append(resp.CreditPacks, pack)
	}
	return resp, nil
}

// StartPortalPurchase implements billingv1.HostedInvoiceServiceServer.
func (h *Handler) StartPortalPurchase(ctx context.Context, req *billingv1.StartPortalPurchaseRequest) (*billingv1.StartPortalPurchaseResponse, error) {
	token, err := command.StartPortalPurchase(ctx, h.d, h.q, req.GetPortalToken(), req.GetPackId(), req.GetQuantity())
	if err != nil {
		return nil, err
	}
	return &billingv1.StartPortalPurchaseResponse{InvoiceToken: token}, nil
}

func pageProto(scope *query.Scope, p *query.Page) *billingv1.GetHostedInvoiceResponse {
	resp := &billingv1.GetHostedInvoiceResponse{
		Kind: p.Kind, ExpireTime: timestamppb.New(p.ExpiresAt), SellerName: p.SellerName, LogoAvailable: p.LogoAvailable, Environment: p.Environment,
	}
	if p.Adhoc != nil {
		resp.Adhoc = &billingv1.HostedAdhoc{Description: p.Adhoc.Description, AmountMinor: p.Adhoc.AmountMinor, Currency: p.Adhoc.Currency,
			CurrencyExponent: p.Adhoc.CurrencyExponent, Consumed: p.Adhoc.Consumed, SellerName: p.SellerName, LogoAvailable: p.LogoAvailable, Environment: p.Environment}
		if k := p.Adhoc.Pack; k != nil {
			resp.Adhoc.Pack = &billingv1.HostedCreditPack{Name: k.Name, Credits: k.Credits, Quantity: k.Quantity, ValidityDays: k.ValidityDays}
		}
		resp.PaymentState = "open"
		return resp
	}
	resp.Invoice = invoices.Proto(p.Invoice)
	resp.Invoice.PdfObjectKey = "" // the visitor gets a link, not a storage key
	resp.Invoice.Payments = nil    // what an operator recorded is the tenant's business
	if in := p.PaymentInstructions; in != nil {
		resp.PaymentInstructions = &billingv1.BankTransferInstructions{BankName: in.BankName, AccountHolder: in.AccountHolder, Iban: in.IBAN, Bic: in.BIC, Reference: in.Reference}
	}
	resp.CustomerName, resp.CurrencyExponent, resp.PdfAvailable = p.CustomerName, p.CurrencyExponent, p.PDFAvailable
	resp.SellerAddressLines, resp.CustomerAddressLines = p.SellerAddressLines, p.CustomerAddressLines
	resp.PaymentState, resp.Payable = p.State, p.Payable
	resp.Payment = paymentContext(scope.TenantID, p)
	return resp
}

func paymentContext(tenantID uuid.UUID, p *query.Page) *billingv1.HostedPaymentContext {
	r := p.Invoice.Row
	desc := p.Snapshot.Description
	if desc == "" && len(p.Invoice.Lines) > 0 {
		desc = p.Invoice.Lines[0].Description
	}
	return &billingv1.HostedPaymentContext{
		TenantId: tenantID.String(), InvoiceId: r.ID.String(), SagaId: p.SagaID.String(), Number: r.Number, AmountMinor: r.TotalMinor, Currency: r.Currency, Provider: p.Provider,
		Country: p.Country, CustomerEmail: p.Snapshot.CustomerEmail, CustomerName: p.CustomerName, Description: desc, Environment: p.Environment,
	}
}

func linkProto(l sqlc.GetPaymentLinkRowRow) *billingv1.PaymentLink {
	out := &billingv1.PaymentLink{
		Id: l.ID.String(), Kind: l.Kind, Status: l.Status, SingleUse: l.SingleUse, CreateTime: timestamppb.New(l.CreatedAt), ExpireTime: timestamppb.New(l.ExpiresAt),
		PaidCount: l.PaidCount, UseCount: l.UseCount, IdempotencyKey: l.IdempotencyKey,
	}
	if l.InvoiceID != nil {
		out.InvoiceId = l.InvoiceID.String()
	}
	if l.CustomerExternalID != nil {
		out.CustomerExternalId = *l.CustomerExternalID
	}
	if l.AmountMinor != nil {
		out.AmountMinor = *l.AmountMinor
	}
	if l.Currency != nil {
		out.Currency = *l.Currency
	}
	if l.Description != nil {
		out.Description = *l.Description
	}
	if l.RevokedAt != nil {
		out.RevokeTime = timestamppb.New(*l.RevokedAt)
	}
	if l.LastPaidAt != nil {
		out.LastPaidTime = timestamppb.New(*l.LastPaidAt)
	}
	if l.PackID != nil {
		out.PackId = l.PackID.String()
	}
	if l.Quantity != nil {
		out.Quantity = *l.Quantity
	}
	if l.Credits != nil {
		out.Credits = *l.Credits
	}
	return out
}
