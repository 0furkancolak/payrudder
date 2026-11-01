package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

type saga struct {
	TenantID, ID, InvoiceID, CustomerID uuid.UUID
	State                               model.State
	Version, Attempt, StepAttempt       int
	DunningAttempt                      int
	Number                              string
	PeriodID, Currency, InvoiceStatus   string
	InvoiceVersion                      int
	TotalMinor                          int64
	FinalizedAt                         time.Time
	Snap                                model.Snapshot
	// RetryHours are the tenant's own dunning delays; nil means the service-wide defaults.
	RetryHours []int32
	// ChargeKeyStart is when the charge under the current idempotency key was first sent; read only for a saga whose payment outcome is unknown, where a "not found"
	// answer is judged by it.
	ChargeKeyStart time.Time
	// CreditedMinor is the sum of the invoice's credit notes and ByHandMinor the money recorded by hand, both read when a void asks what is left to refund.
	CreditedMinor, ByHandMinor int64
}

func (e *Engine) load(ctx context.Context, tx pgx.Tx, tenantID, sagaID uuid.UUID) (*saga, error) {
	q := sqlc.New(tx)
	r, err := q.LockSagaWithInvoice(ctx, sqlc.LockSagaWithInvoiceParams{TenantID: tenantID, ID: sagaID})
	if err != nil {
		return nil, err
	}
	s := saga{
		TenantID: r.TenantID, ID: r.ID, InvoiceID: r.InvoiceID, CustomerID: r.CustomerID, State: model.State(r.State), Version: int(r.Version), Attempt: int(r.Attempt),
		StepAttempt: int(r.StepAttempt), DunningAttempt: int(r.DunningAttempt), PeriodID: r.PeriodID, Currency: r.Currency, InvoiceStatus: r.Status,
		InvoiceVersion: int(r.InvoiceVersion), TotalMinor: r.TotalMinor, FinalizedAt: r.FinalizedAt,
	}
	if r.Number != nil {
		s.Number = *r.Number
	}
	if err := json.Unmarshal(r.Snapshot, &s.Snap); err != nil {
		return nil, fmt.Errorf("invoice %s snapshot: %w", s.InvoiceID, err)
	}
	switch settings, err := q.GetDunningSettings(ctx, tenantID); {
	case errors.Is(err, pgx.ErrNoRows):
		s.RetryHours = nil
	case err != nil:
		return nil, fmt.Errorf("dunning settings: %w", err)
	default:
		s.RetryHours = settings.RetryHours
	}
	if s.State == model.PaymentUnknown {
		switch at, err := q.GetChargeKeyStart(ctx, sqlc.GetChargeKeyStartParams{TenantID: tenantID, SagaID: sagaID}); {
		case errors.Is(err, pgx.ErrNoRows):
			s.ChargeKeyStart = s.FinalizedAt // no record of the charge: the oldest it can be, which errs on the side of not charging again
		case err != nil:
			return nil, fmt.Errorf("charge key start: %w", err)
		default:
			s.ChargeKeyStart = at
		}
	}
	return &s, nil
}
