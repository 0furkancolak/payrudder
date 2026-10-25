package webhook

import (
	"context"

	"github.com/google/uuid"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
)

// BillingAPI is what payments-worker asks of billing-svc, as the tenant: its invoices (for the reconciliation) and the settling of a payment made on a hosted page.
type BillingAPI interface {
	ListInvoices(ctx context.Context, tenant uuid.UUID, req *billingv1.ListInvoicesRequest) (*billingv1.ListInvoicesResponse, error)
	GetInvoice(ctx context.Context, tenant uuid.UUID, id string) (*billingv1.Invoice, error)
	// SettleInvoicePayment tells billing that a provider confirmed a payment made on a hosted page.
	SettleInvoicePayment(ctx context.Context, tenant uuid.UUID, req *billingv1.SettleInvoicePaymentRequest) (*billingv1.SettleInvoicePaymentResponse, error)
}

// BillingClient talks to billing-svc's tenant API.
type BillingClient struct {
	Client billingv1.BillingServiceClient
}

func (b BillingClient) ListInvoices(ctx context.Context, id uuid.UUID, req *billingv1.ListInvoicesRequest) (*billingv1.ListInvoicesResponse, error) {
	return b.Client.ListInvoices(as(ctx, id), req)
}

func (b BillingClient) GetInvoice(ctx context.Context, id uuid.UUID, invoice string) (*billingv1.Invoice, error) {
	r, err := b.Client.GetInvoice(as(ctx, id), &billingv1.GetInvoiceRequest{InvoiceId: invoice})
	return r.GetInvoice(), err
}

func (b BillingClient) SettleInvoicePayment(ctx context.Context, id uuid.UUID, req *billingv1.SettleInvoicePaymentRequest) (*billingv1.SettleInvoicePaymentResponse, error) {
	return b.Client.SettleInvoicePayment(as(ctx, id), req)
}
