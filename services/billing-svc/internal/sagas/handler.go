// Package sagas serves the invoice-saga methods of BillingAdminService: it maps requests to commands and queries.
package sagas

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/paymentgate"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/query"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
)

// Handler implements the invoice-saga methods of billingv1.BillingAdminServiceServer and the seller's view of a payment that waits (GetInvoicePaymentWait, ReleaseHeldInvoice and
// VoidHeldInvoice of billingv1.BillingServiceServer).
type Handler struct {
	system *tenantdb.DB
	eng    *command.Engine
}

// New returns the handler over the billing_system pool.
func New(system *tenantdb.DB, eng *command.Engine) *Handler {
	return &Handler{system: system, eng: eng}
}

func parseIDs(tenantID, invoiceID string) (tid, inv uuid.UUID, err error) {
	if tid, err = errkey.ParseUUID("billing.tenant.bad_id", "tenant_id", tenantID); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if inv, err = errkey.ParseUUID("billing.invoice.bad_id", "invoice_id", invoiceID); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return tid, inv, nil
}

// RetryInvoiceSaga implements billingv1.BillingAdminServiceServer.
func (h *Handler) RetryInvoiceSaga(ctx context.Context, req *billingv1.RetryInvoiceSagaRequest) (*billingv1.RetryInvoiceSagaResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, inv, err := parseIDs(req.GetTenantId(), req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	row, err := command.Retry(ctx, h.system, h.eng, tid, inv)
	if err != nil {
		return nil, err
	}
	return &billingv1.RetryInvoiceSagaResponse{Saga: sagaProto(row)}, nil
}

// VoidInvoice implements billingv1.BillingAdminServiceServer.
func (h *Handler) VoidInvoice(ctx context.Context, req *billingv1.VoidInvoiceRequest) (*billingv1.VoidInvoiceResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, inv, err := parseIDs(req.GetTenantId(), req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	row, err := command.Void(ctx, h.system, h.eng, tid, inv, req.GetConfirmNotCharged(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &billingv1.VoidInvoiceResponse{Saga: sagaProto(row)}, nil
}

// GetInvoicePaymentWait implements billingv1.BillingServiceServer.
func (h *Handler) GetInvoicePaymentWait(ctx context.Context, req *billingv1.GetInvoicePaymentWaitRequest) (*billingv1.GetInvoicePaymentWaitResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	inv, err := errkey.ParseUUID("billing.invoice.bad_id", "invoice_id", req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	row, err := query.Wait(ctx, h.system, ti.ID, inv)
	if err != nil {
		return nil, err
	}
	w := &billingv1.PaymentWait{State: row.State}
	switch model.State(row.State) {
	case model.PaymentHeld:
		w.HoldReason = h.holdReason(ctx, ti.ID, row.CustomerExternalID)
		fallthrough
	case model.PaymentActionRequired:
		w.SinceTime = timestamppb.New(row.Since)
		if row.NextAttemptAt != nil {
			w.NextCheckTime = timestamppb.New(*row.NextAttemptAt)
		}
	}
	return &billingv1.GetInvoicePaymentWaitResponse{Wait: w}, nil
}

// holdReason asks the payment gate now why the first charge of a customer's invoice is held. An answer it cannot get is "": the reader is told nothing rather than something wrong.
func (h *Handler) holdReason(ctx context.Context, tid uuid.UUID, customer string) string {
	switch v, err := h.eng.Gate(ctx, tid, customer); {
	case err != nil:
		slog.WarnContext(ctx, "the payment gate could not be asked why an invoice is held", "tenant", tid, "error", err)
		return ""
	case v == paymentgate.TenantOnHold:
		return "tenant_on_hold"
	case v == paymentgate.CustomerBlocked:
		return "customer_blocked"
	}
	return "none"
}

// ReleaseHeldInvoice implements billingv1.BillingServiceServer.
func (h *Handler) ReleaseHeldInvoice(ctx context.Context, req *billingv1.ReleaseHeldInvoiceRequest) (*billingv1.ReleaseHeldInvoiceResponse, error) {
	ti, inv, err := h.tenantInvoice(ctx, req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	row, err := command.Release(ctx, h.system, h.eng, ti, inv)
	if err != nil {
		return nil, err
	}
	return &billingv1.ReleaseHeldInvoiceResponse{Saga: sagaProto(row)}, nil
}

// VoidHeldInvoice implements billingv1.BillingServiceServer.
func (h *Handler) VoidHeldInvoice(ctx context.Context, req *billingv1.VoidHeldInvoiceRequest) (*billingv1.VoidHeldInvoiceResponse, error) {
	ti, inv, err := h.tenantInvoice(ctx, req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	row, err := command.VoidHeld(ctx, h.system, h.eng, ti, inv)
	if err != nil {
		return nil, err
	}
	return &billingv1.VoidHeldInvoiceResponse{Saga: sagaProto(row)}, nil
}

// tenantInvoice is the caller's tenant and the invoice id of a tenant-API request: the tenant is never read from the request.
func (h *Handler) tenantInvoice(ctx context.Context, invoiceID string) (tid, inv uuid.UUID, err error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	inv, err = errkey.ParseUUID("billing.invoice.bad_id", "invoice_id", invoiceID)
	return ti.ID, inv, err
}

// GetInvoiceSaga implements billingv1.BillingAdminServiceServer.
func (h *Handler) GetInvoiceSaga(ctx context.Context, req *billingv1.GetInvoiceSagaRequest) (*billingv1.GetInvoiceSagaResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, inv, err := parseIDs(req.GetTenantId(), req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	hist, err := query.Get(ctx, h.system, tid, inv)
	if err != nil {
		return nil, err
	}
	resp := &billingv1.GetInvoiceSagaResponse{Saga: sagaProto(hist.Saga)}
	for _, e := range hist.Events {
		ev := &billingv1.SagaEvent{Seq: e.Seq, FromState: e.FromState, ToState: e.ToState, Cause: e.Cause, At: timestamppb.New(e.At)}
		if e.CommandID != nil {
			ev.CommandId = e.CommandID.String()
		}
		resp.Events = append(resp.Events, ev)
	}
	return resp, nil
}

func sagaProto(row sqlc.GetSagaRow) *billingv1.InvoiceSaga {
	out := &billingv1.InvoiceSaga{Id: row.ID.String(), InvoiceId: row.InvoiceID.String(), State: row.State, Version: row.Version, Attempt: row.Attempt, DunningAttempt: row.DunningAttempt}
	if row.LastError != nil {
		out.LastError = *row.LastError
	}
	return out
}
