// Package sagas serves the invoice-saga methods of BillingAdminService: it maps requests to commands and queries.
package sagas

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/sagas/command"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/sagas/query"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// Handler implements the invoice-saga methods of billingv1.BillingAdminServiceServer.
type Handler struct {
	system *pgxpool.Pool
	eng    *saga.Engine
}

// New returns the handler over the billing_system pool.
func New(system *pgxpool.Pool, eng *saga.Engine) *Handler { return &Handler{system: system, eng: eng} }

func parseIDs(tenantID, invoiceID string) (tid, inv uuid.UUID, err error) {
	if tid, err = uuid.Parse(tenantID); err != nil {
		return uuid.Nil, uuid.Nil, status.Error(codes.InvalidArgument, "tenant_id must be a UUID")
	}
	if inv, err = uuid.Parse(invoiceID); err != nil {
		return uuid.Nil, uuid.Nil, status.Error(codes.InvalidArgument, "invoice_id must be a UUID")
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
	row, err := command.Void(ctx, h.system, h.eng, tid, inv)
	if err != nil {
		return nil, err
	}
	return &billingv1.VoidInvoiceResponse{Saga: sagaProto(row)}, nil
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
