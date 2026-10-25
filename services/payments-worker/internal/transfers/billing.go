package transfers

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// BillingClient lets the commands ask billing-svc about an invoice and nudge its saga. The payments-worker is an internal caller: it acts for the tenant
// and, for the nudge, as the operator.
type BillingClient struct {
	Invoices billingv1.BillingServiceClient
	Admin    billingv1.BillingAdminServiceClient
}

// InvoiceStatus implements command.Billing.
func (b BillingClient) InvoiceStatus(ctx context.Context, t, invoice uuid.UUID) (string, bool, error) {
	ctx = tenant.OutgoingFor(ctx, t)
	r, err := b.Invoices.GetInvoice(ctx, &billingv1.GetInvoiceRequest{InvoiceId: invoice.String()})
	if status.Code(err) == codes.NotFound {
		return "", false, nil
	}
	return r.GetInvoice().GetStatus(), err == nil, err
}

// Nudge implements command.Billing: RetryInvoiceSaga re-asks the payment status of a saga that is waiting for it.
func (b BillingClient) Nudge(ctx context.Context, t, invoice uuid.UUID) error {
	ctx = tenant.OutgoingContext(tenant.WithOperator(ctx))
	_, err := b.Admin.RetryInvoiceSaga(ctx, &billingv1.RetryInvoiceSagaRequest{TenantId: t.String(), InvoiceId: invoice.String()})
	return err
}
