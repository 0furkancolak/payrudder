package command

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/pkg/env"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/hosted/query"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// MockToken is the payment method the mock payment charges: the sandbox provider's "approve" scenario.
const MockToken = "pm_sandbox_success" //nolint:gosec // a sandbox scenario name, not a credential

// StartLinkPayment is the first use of an ad-hoc link: it makes the invoice (numbered, with its saga) and hands out the invoice link the payer continues on. A single-use
// link whose invoice is still unpaid hands out a fresh link to the same invoice (the payer came back); once that invoice is paid the link is consumed.
func StartLinkPayment(ctx context.Context, d Deps, token string) (string, error) {
	link, err := query.ResolveLink(ctx, d.System, token)
	if err != nil {
		return "", err
	}
	return startLink(ctx, d, link.Row)
}

// StartPortalPurchase is the "Buy" button of a customer's portal: it makes the credit-pack link for the portal's customer (or continues the one an earlier press made
// and nobody paid, so pressing twice leads to one invoice), then starts it like a visitor pressing Continue. It returns the token of the invoice link to pay on.
func StartPortalPurchase(ctx context.Context, d Deps, qd query.Deps, portalToken, packID string, quantity int32) (string, error) {
	scope, err := query.Resolve(ctx, qd.System, "", portalToken, "")
	if err != nil {
		return "", err
	}
	if scope.Portal.ExternalID == "" {
		// A portal link made before phase 36 does not know the customer's external id, which the invoice is made out by.
		return "", errkey.New(codes.FailedPrecondition, "billing.credits.portal_cannot_buy", "this portal link cannot buy credits; ask for a new one")
	}
	pid, err := uuid.Parse(packID)
	if err != nil {
		return "", errkey.New(codes.NotFound, "billing.credits.pack_not_found", "no such credit pack")
	}
	if quantity == 0 {
		quantity = 1
	}
	if quantity < 1 || quantity > MaxQuantity {
		return "", errkey.New(codes.InvalidArgument, "billing.credits.bad_quantity", "quantity must be 1 to 1000")
	}
	var open *sqlc.FindActiveInvoiceLinkRow
	err = pgxx.InTx(ctx, d.System, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		row, err := sqlc.New(tx).FindOpenPortalPackLink(ctx, sqlc.FindOpenPortalPackLinkParams{TenantID: scope.TenantID, CustomerID: &scope.Portal.CustomerID, PackID: &pid, Quantity: &quantity})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		r := sqlc.FindActiveInvoiceLinkRow(row)
		open = &r
		return err
	})
	if err != nil {
		return "", internal(err)
	}
	if open != nil {
		return startLink(ctx, d, *open)
	}
	days := int32(ChildDays)
	if left := int32(scope.ExpiresAt.Sub(d.now()).Hours() / 24); left >= 1 && left < days { //nolint:gosec // at most 365
		days = left
	}
	created, err := CreateLink(ctx, d, scope.TenantID, CreateInput{Kind: KindCreditPack, PackID: pid, Quantity: quantity, CustomerExternalID: scope.Portal.ExternalID,
		ExpiresInDays: days, IdempotencyKey: "portal:" + uuid.NewString()})
	if err != nil {
		return "", err
	}
	return StartLinkPayment(ctx, d, created.Token)
}

// startLink makes the invoice of one use of a factory link and the invoice link the payer continues on.
func startLink(ctx context.Context, d Deps, r sqlc.FindActiveInvoiceLinkRow) (string, error) {
	var err error
	if !factory(r.Kind) || r.AmountMinor == nil || r.Currency == nil {
		return "", query.NoLink()
	}
	child, err := NewToken()
	if err != nil {
		return "", err
	}
	err = pgxx.InTx(ctx, d.System, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		// One start at a time per link: a single-use link must not make two invoices when its button is pressed twice.
		cur, err := q.LockPaymentLink(ctx, sqlc.LockPaymentLinkParams{TenantID: r.TenantID, ID: r.ID})
		if err != nil {
			return err
		}
		var invoiceID uuid.UUID
		if cur.SingleUse {
			if cur.PaidCount > 0 {
				return errkey.New(codes.FailedPrecondition, "billing.payment_link.consumed", "this link has already been used")
			}
			switch id, err := q.FindUnpaidChildInvoice(ctx, sqlc.FindUnpaidChildInvoiceParams{TenantID: r.TenantID, ID: r.ID}); {
			case err == nil && id != nil:
				invoiceID = *id
			case err != nil && !errors.Is(err, pgx.ErrNoRows):
				return err
			}
		}
		if invoiceID == uuid.Nil {
			spec := runs.LinkInvoiceSpec{AmountMinor: *r.AmountMinor, Currency: *r.Currency}
			if r.Description != nil {
				spec.Description = *r.Description
			}
			if r.Kind == KindCreditPack && r.Quantity != nil && *r.Quantity > 1 {
				spec.Description += " x" + strconv.Itoa(int(*r.Quantity))
			}
			if r.CustomerExternalID != nil {
				spec.CustomerExternalID = *r.CustomerExternalID
			}
			if invoiceID, err = d.Dir.CreateLinkInvoice(ctx, r.TenantID, spec); err != nil {
				return err
			}
			if err := q.BumpPaymentLinkUse(ctx, sqlc.BumpPaymentLinkUseParams{TenantID: r.TenantID, ID: r.ID}); err != nil {
				return err
			}
		}
		expires := d.now().Add(ChildDays * 24 * time.Hour)
		if r.ExpiresAt.Before(expires) {
			expires = r.ExpiresAt
		}
		parent := r.ID
		return q.InsertPaymentLink(ctx, sqlc.InsertPaymentLinkParams{TenantID: r.TenantID, ID: uuid.New(), Kind: KindInvoice, ParentID: &parent, InvoiceID: &invoiceID,
			TokenHash: query.TokenHash(child), ExpiresAt: expires, CustomerExternalID: r.CustomerExternalID})
	})
	if err != nil {
		return "", internal(err)
	}
	return child, nil
}

// PaymentRequest is a visitor's request to pay the invoice in scope.
type PaymentRequest struct {
	Scope  *query.Scope
	Method string
}

// Paid is the answer to RequestPayment.
type Paid struct {
	// Started is true when billing took the payment on (the mock payment); otherwise the gateway continues with payments-worker.
	Started bool
	Page    *query.Page
}

// RequestPayment starts a payment the visitor asked for. "mock" (a sandbox invoice, or any with QUOTIFY_DEMO) charges through the saga; any other method leaves
// the page's context to the gateway, which asks payments-worker for a provider checkout or payment instructions. Nothing is marked paid here.
func RequestPayment(ctx context.Context, d Deps, qd query.Deps, req PaymentRequest) (*Paid, error) {
	page, err := query.Get(ctx, qd, req.Scope)
	if err != nil {
		return nil, err
	}
	if page.Kind != "invoice" {
		return nil, query.NoLink()
	}
	if !page.Payable {
		if page.State == "processing" && req.Method == "mock" {
			return &Paid{Started: true, Page: page}, nil // pressed twice: the first payment is on its way
		}
		return nil, errkey.New(codes.FailedPrecondition, "billing.payment.invoice_not_payable", "this invoice cannot be paid now")
	}
	if req.Method != "mock" {
		if req.Method == "" {
			return nil, errkey.New(codes.InvalidArgument, "billing.payment.method_unavailable", "a payment method is required")
		}
		return &Paid{Page: page}, nil
	}
	if page.Environment != env.Sandbox.String() && !d.Demo {
		return nil, errkey.New(codes.FailedPrecondition, "billing.payment.method_unavailable", "the mock payment is only for sandbox tenants and demo stacks")
	}
	id := req.Scope.InvoiceID
	err = pgxx.InTx(ctx, d.System, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := sqlc.New(tx).SetInvoicePaymentMethod(ctx, sqlc.SetInvoicePaymentMethodParams{PaymentMethod: MockToken, TenantID: req.Scope.TenantID, ID: id}); err != nil {
			return err
		}
		sagaID, err := sqlc.New(tx).GetSagaIDByInvoice(ctx, sqlc.GetSagaIDByInvoiceParams{TenantID: req.Scope.TenantID, InvoiceID: id})
		if err != nil {
			return err
		}
		out, err := d.Engine.Handle(ctx, tx, req.Scope.TenantID, sagaID, saga.EvPayRequested, "customer:pay", nil)
		if err != nil {
			return err
		}
		if !out.Applied {
			return errkey.New(codes.FailedPrecondition, "billing.payment.invoice_not_payable", "this invoice cannot be paid now")
		}
		return nil
	})
	if err != nil {
		return nil, internal(err)
	}
	return &Paid{Started: true, Page: page}, nil
}

// SettleInput is a payment a provider confirmed for an invoice.
type SettleInput struct {
	InvoiceID   uuid.UUID
	Provider    string
	ProviderRef string
	AmountMinor int64
	Currency    string
}

// SettleInvoicePayment moves the saga of an invoice to paid because a provider confirmed a payment made on a hosted page. It reports false when the invoice is not
// waiting for one (already paid, voided): the payment then is a discrepancy for the operator. A payment of another amount or currency is refused: it does not pay
// this invoice.
func SettleInvoicePayment(ctx context.Context, d Deps, tenantID uuid.UUID, in SettleInput) (bool, error) {
	var applied bool
	err := pgxx.InTx(ctx, d.System, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		inv, err := q.GetInvoiceTotals(ctx, sqlc.GetInvoiceTotalsParams{TenantID: tenantID, ID: in.InvoiceID})
		if err != nil {
			return err
		}
		if inv.Status == "paid" || inv.Status == "void" {
			return nil
		}
		if inv.TotalMinor != in.AmountMinor || inv.Currency != in.Currency {
			return errkey.New(codes.FailedPrecondition, "billing.payment.amount_mismatch", "the payment is not for the amount of the invoice")
		}
		sagaID, err := q.GetSagaIDByInvoice(ctx, sqlc.GetSagaIDByInvoiceParams{TenantID: tenantID, InvoiceID: in.InvoiceID})
		if err != nil {
			return err
		}
		out, err := d.Engine.Handle(ctx, tx, tenantID, sagaID, saga.EvProviderSettled, "settle:"+in.Provider+":"+in.ProviderRef, nil)
		applied = out.Applied
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, status.Error(codes.NotFound, "invoice not found")
	}
	if err != nil {
		return false, internal(err)
	}
	return applied, nil
}
