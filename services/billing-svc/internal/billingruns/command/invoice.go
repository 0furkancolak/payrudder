package command

import (
	"context"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// invoice finalizes every draft of the run (number, open, saga started) and reports whether the run is complete:
// all its invoices' sagas reached COMPLETED or VOIDED.
func (m *Manager) invoice(ctx context.Context, run *model.Run) (bool, error) {
	for {
		ids, err := m.on(run.TenantID).ListDraftInvoiceIDsOfRun(ctx, sqlc.ListDraftInvoiceIDsOfRunParams{TenantID: run.TenantID, PeriodID: run.PeriodID, PageSize: int32(m.cfg.PageSize)}) //nolint:gosec // small
		if err != nil {
			return false, err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err := m.cfg.Invoices.Finalize(ctx, run.TenantID, id); err != nil {
				return false, err
			}
		}
	}
	open, err := m.on(run.TenantID).CountUnsettledInvoicesOfRun(ctx, sqlc.CountUnsettledInvoicesOfRunParams{TenantID: run.TenantID, PeriodID: run.PeriodID})
	if err != nil {
		return false, err
	}
	return open == 0, nil
}
