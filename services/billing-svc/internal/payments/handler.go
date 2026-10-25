// Package payments serves RecordInvoicePayment of BillingAdminService: money received outside a payment provider. It maps the request to the command and
// the result to protos.
package payments

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/payments/command"
	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Handler implements RecordInvoicePayment of billingv1.BillingAdminServiceServer.
type Handler struct {
	system *pgxpool.Pool
	eng    *sagacommand.Engine
}

// New returns the handler over the billing_system pool.
func New(system *pgxpool.Pool, eng *sagacommand.Engine) *Handler {
	return &Handler{system: system, eng: eng}
}

// RecordInvoicePayment implements billingv1.BillingAdminServiceServer.
func (h *Handler) RecordInvoicePayment(ctx context.Context, req *billingv1.RecordInvoicePaymentRequest) (*billingv1.RecordInvoicePaymentResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := errkey.ParseUUID("billing.tenant.bad_id", "tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	inv, err := errkey.ParseUUID("billing.invoice.bad_id", "invoice_id", req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	var received time.Time
	if req.GetReceivedAt() != nil {
		received = req.GetReceivedAt().AsTime()
	}
	res, err := command.Record(ctx, h.system, h.eng, command.Payment{Tenant: tid, Invoice: inv, Method: req.GetMethod(), AmountMinor: req.GetAmountMinor(), ReceivedAt: received,
		Reference: req.GetReference(), Note: req.GetNote(), Actor: req.GetActor()})
	if err != nil {
		return nil, err
	}
	return &billingv1.RecordInvoicePaymentResponse{Payment: Proto(res.Payment), Duplicate: res.Duplicate, PaidMinor: res.Paid, OutstandingMinor: res.Outstanding, Settled: res.Settled}, nil
}

// Proto maps a payment row to the API message.
func Proto(r sqlc.ListInvoicePaymentsRow) *billingv1.InvoicePayment {
	return &billingv1.InvoicePayment{
		Id: r.ID.String(), InvoiceId: r.InvoiceID.String(), Method: r.Method, AmountMinor: r.AmountMinor, Currency: r.Currency, ReceivedAt: timestamppb.New(r.ReceivedAt),
		Reference: r.Reference, Note: r.Note, RecordedBy: r.RecordedBy, RecordedAt: timestamppb.New(r.RecordedAt),
	}
}
