// Package deadletters serves the dead-letter methods of BillingAdminService: it maps requests to commands and queries.
package deadletters

import (
	"context"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/deadletters/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/deadletters/query"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/dlq"
)

// Handler implements the dead-letter methods of billingv1.BillingAdminServiceServer. Without an admin they answer UNIMPLEMENTED.
type Handler struct {
	dlq *dlq.Admin
}

// New returns the handler.
func New() *Handler { return &Handler{} }

// WithDeadLetters enables the dead-letter operations.
func (h *Handler) WithDeadLetters(a *dlq.Admin) *Handler {
	h.dlq = a
	return h
}

// ListDeadLetters implements billingv1.BillingAdminServiceServer.
func (h *Handler) ListDeadLetters(ctx context.Context, req *billingv1.ListDeadLettersRequest) (*billingv1.ListDeadLettersResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	depths, msgs, err := query.List(ctx, h.dlq, req.GetQueue(), int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	resp := &billingv1.ListDeadLettersResponse{}
	for _, d := range depths {
		resp.Queues = append(resp.Queues, &billingv1.DeadLetterQueue{Queue: d.Queue, Messages: d.Messages})
	}
	for _, m := range msgs {
		resp.Messages = append(resp.Messages, &billingv1.DeadLetter{MessageId: m.ID, DeliveryCount: m.Deliveries, Reason: m.Reason, BodyBytes: int32(m.BodyBytes)}) //nolint:gosec // message sizes are far below 2 GiB
	}
	return resp, nil
}

// RedriveDeadLetters implements billingv1.BillingAdminServiceServer.
func (h *Handler) RedriveDeadLetters(ctx context.Context, req *billingv1.RedriveDeadLettersRequest) (*billingv1.RedriveDeadLettersResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	n, err := command.Redrive(ctx, h.dlq, req.GetQueue(), int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	return &billingv1.RedriveDeadLettersResponse{Redriven: int32(n)}, nil //nolint:gosec // at most 1000
}
