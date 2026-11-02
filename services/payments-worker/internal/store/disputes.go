package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// Disputes is payment_disputes: one row per case a customer opened with their bank, kept as it stands (the ledger holds each step of it).
type Disputes struct{ DB DB }

// DisputeCase is a case as a notification says it.
type DisputeCase struct {
	TenantID    uuid.UUID
	Environment string
	Provider    string
	Ref         string // the provider's reference of the case
	PaymentRef  string // the payment that is disputed, as the ledger holds it
	InvoiceID   uuid.UUID
	AmountMinor int64
	Currency    string
	Outcome     string // open, won or lost
	Formal      bool
	Status      string // the provider's own word
	Reason      string
	EvidenceDue time.Time
	OpenedAt    time.Time
}

// Upsert records the case as it stands now. A case that ended is not reopened by a late notification about an earlier step.
func (d Disputes) Upsert(ctx context.Context, c DisputeCase) error {
	p := sqlc.UpsertDisputeParams{TenantID: c.TenantID, Environment: c.Environment, Provider: c.Provider, DisputeRef: c.Ref, PaymentRef: c.PaymentRef, AmountMinor: c.AmountMinor,
		Currency: c.Currency, Outcome: c.Outcome, Formal: c.Formal, ProviderStatus: c.Status, Reason: c.Reason, OpenedAt: c.OpenedAt}
	if c.InvoiceID != uuid.Nil {
		p.InvoiceID = &c.InvoiceID
	}
	if !c.EvidenceDue.IsZero() {
		p.EvidenceDueAt = &c.EvidenceDue
	}
	if c.Outcome != "open" {
		closed := c.OpenedAt
		p.ClosedAt = &closed
	}
	return at(ctx, d.DB, c.TenantID, func(q *sqlc.Queries) error { return q.UpsertDispute(ctx, p) })
}

// Dispute is a case as the operator and the tenant API read it.
type Dispute struct {
	ID          int64      `json:"id"`
	Environment string     `json:"environment"`
	Provider    string     `json:"provider"`
	DisputeRef  string     `json:"dispute_id"`
	PaymentRef  string     `json:"payment_ref"`
	InvoiceID   *string    `json:"invoice_id,omitempty"`
	AmountMinor int64      `json:"amount_minor"`
	Currency    string     `json:"currency"`
	Outcome     string     `json:"outcome"`
	Formal      bool       `json:"formal"`
	Status      string     `json:"provider_status"`
	Reason      string     `json:"reason,omitempty"`
	EvidenceDue *time.Time `json:"evidence_due_time,omitempty"`
	OpenedAt    time.Time  `json:"opened_time"`
	ClosedAt    *time.Time `json:"closed_time,omitempty"`
}

// List returns the tenant's cases, newest first: of one invoice when it is given, of one outcome when that is not empty.
func (d Disputes) List(ctx context.Context, tenant uuid.UUID, invoice uuid.UUID, outcome string, limit int) ([]Dispute, error) {
	p := sqlc.ListDisputesParams{TenantID: tenant, Outcome: outcome, RowLimit: int32(min(max(limit, 1), 1000))}
	if invoice != uuid.Nil {
		p.InvoiceID = &invoice
	}
	rows, err := get(ctx, d.DB, tenant, func(q *sqlc.Queries) ([]sqlc.ListDisputesRow, error) { return q.ListDisputes(ctx, p) })
	if err != nil {
		return nil, err
	}
	out := make([]Dispute, len(rows))
	for i, r := range rows {
		out[i] = Dispute{ID: r.ID, Environment: r.Environment, Provider: r.Provider, DisputeRef: r.DisputeRef, PaymentRef: r.PaymentRef, AmountMinor: r.AmountMinor, Currency: r.Currency,
			Outcome: r.Outcome, Formal: r.Formal, Status: r.ProviderStatus, Reason: r.Reason, EvidenceDue: r.EvidenceDueAt, OpenedAt: r.OpenedAt, ClosedAt: r.ClosedAt}
		if r.InvoiceID != nil {
			id := r.InvoiceID.String()
			out[i].InvoiceID = &id
		}
	}
	return out, nil
}

// OpenDispute is a case that is undecided and belongs to one of the tenant's invoices.
type OpenDispute struct {
	Ref, PaymentRef, Provider, Currency, Status, Reason string
	InvoiceID                                           uuid.UUID
	AmountMinor                                         int64
	Formal                                              bool
	OpenedAt                                            time.Time
}

// Open lists the cases still undecided that point at an invoice, oldest first: what the reconciliation raises.
func (d Disputes) Open(ctx context.Context, tenant uuid.UUID) ([]OpenDispute, error) {
	rows, err := get(ctx, d.DB, tenant, func(q *sqlc.Queries) ([]sqlc.ListOpenDisputesRow, error) { return q.ListOpenDisputes(ctx, tenant) })
	if err != nil {
		return nil, err
	}
	out := make([]OpenDispute, 0, len(rows))
	for _, r := range rows {
		if r.InvoiceID == nil {
			continue
		}
		out = append(out, OpenDispute{Ref: r.DisputeRef, PaymentRef: r.PaymentRef, Provider: r.Provider, Currency: r.Currency, Status: r.ProviderStatus, Reason: r.Reason,
			InvoiceID: *r.InvoiceID, AmountMinor: r.AmountMinor, Formal: r.Formal, OpenedAt: r.OpenedAt})
	}
	return out, nil
}

// PaymentOf finds the invoice and the amount of the successful charge the ledger holds for a provider's payment reference.
func (l Ledger) PaymentOf(ctx context.Context, tenant uuid.UUID, provider, ref string) (invoice uuid.UUID, amountMinor int64, currency string, found bool, err error) {
	r, ok, err := lookup(ctx, l.DB, tenant, func(q *sqlc.Queries) (sqlc.GetChargeByPaymentRefRow, error) {
		return q.GetChargeByPaymentRef(ctx, sqlc.GetChargeByPaymentRefParams{TenantID: tenant, Provider: provider, ProviderRef: &ref})
	})
	if !ok || r.InvoiceID == nil {
		return uuid.Nil, 0, "", false, err
	}
	return *r.InvoiceID, r.AmountMinor, r.Currency, true, nil
}

// DisputeStepKnown says whether the ledger holds this step of a case already.
func (l Ledger) DisputeStepKnown(ctx context.Context, tenant uuid.UUID, provider, ref, status string) (bool, error) {
	return get(ctx, l.DB, tenant, func(q *sqlc.Queries) (bool, error) {
		return q.DisputeFactExists(ctx, sqlc.DisputeFactExistsParams{TenantID: tenant, Provider: provider, ProviderRef: &ref, Status: status})
	})
}
