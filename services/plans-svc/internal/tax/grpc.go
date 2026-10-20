// Package tax registers the tax rate handlers of PlansService: request to command or query, result to response.
package tax

import (
	"context"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/tax/command"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/tax/query"
)

// Handler serves the tax rate methods of PlansService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the tax rate handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) GetTaxRates(ctx context.Context, _ *plansv1.GetTaxRatesRequest) (*plansv1.GetTaxRatesResponse, error) {
	rates, err := h.qry.Rates(ctx)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetTaxRatesResponse{Rates: rates}, nil
}

func (h *Handler) SetTaxRates(ctx context.Context, req *plansv1.SetTaxRatesRequest) (*plansv1.SetTaxRatesResponse, error) {
	rates, err := h.cmd.SetRates(ctx, req.GetRates())
	if err != nil {
		return nil, err
	}
	return &plansv1.SetTaxRatesResponse{Rates: rates}, nil
}
