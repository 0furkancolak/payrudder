// Package customers registers the customer handlers of PlansService: request to command or query, result to response.
package customers

import (
	"context"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/customers/command"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/customers/query"
)

// Handler serves the customer methods of PlansService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the customer handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) CreateCustomer(ctx context.Context, req *plansv1.CreateCustomerRequest) (*plansv1.CreateCustomerResponse, error) {
	c, err := h.cmd.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.CreateCustomerResponse{Customer: c}, nil
}

func (h *Handler) SetCustomerAddress(ctx context.Context, req *plansv1.SetCustomerAddressRequest) (*plansv1.SetCustomerAddressResponse, error) {
	c, err := h.cmd.SetAddress(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.SetCustomerAddressResponse{Customer: c}, nil
}

func (h *Handler) SetPaymentMethod(ctx context.Context, req *plansv1.SetPaymentMethodRequest) (*plansv1.SetPaymentMethodResponse, error) {
	c, err := h.cmd.SetPaymentMethod(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.SetPaymentMethodResponse{Customer: c}, nil
}

func (h *Handler) GetCustomer(ctx context.Context, req *plansv1.GetCustomerRequest) (*plansv1.GetCustomerResponse, error) {
	var (
		c   *plansv1.Customer
		err error
	)
	if req.GetId() != "" {
		c, err = h.qry.ByID(ctx, req.GetId())
	} else {
		c, err = h.qry.ByExternalID(ctx, req.GetExternalId())
	}
	if err != nil {
		return nil, err
	}
	return &plansv1.GetCustomerResponse{Customer: c}, nil
}

func (h *Handler) ResolveCustomer(ctx context.Context, req *plansv1.ResolveCustomerRequest) (*plansv1.ResolveCustomerResponse, error) {
	c, err := h.qry.ByExternalID(ctx, req.GetExternalId())
	if err != nil {
		return nil, err
	}
	return &plansv1.ResolveCustomerResponse{Customer: c}, nil
}

func (h *Handler) ListCustomers(ctx context.Context, req *plansv1.ListCustomersRequest) (*plansv1.ListCustomersResponse, error) {
	return h.qry.List(ctx, req)
}
