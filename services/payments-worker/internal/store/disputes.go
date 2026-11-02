package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/audit"
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
	// FeeMinor and FeeCurrency are the fee the notification says the provider took; zero and empty when it does not say (a later notification that does not say leaves the
	// fee an earlier one did).
	FeeMinor    int64
	FeeCurrency string
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
	if c.FeeMinor > 0 && c.FeeCurrency != "" {
		p.FeeMinor, p.FeeCurrency = &c.FeeMinor, &c.FeeCurrency
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
	// FeeMinor and FeeCurrency: what the provider took for the case, when a notification said; HandledAt and HandledBy: the seller's own mark; NoteCount: the notes the seller wrote.
	FeeMinor    *int64     `json:"fee_minor,omitempty"`
	FeeCurrency string     `json:"fee_currency,omitempty"`
	HandledAt   *time.Time `json:"handled_time,omitempty"`
	HandledBy   string     `json:"handled_by,omitempty"`
	NoteCount   int64      `json:"note_count"`
}

// List returns the tenant's cases, newest first: of one invoice when it is given, of one outcome when that is not empty.
func (d Disputes) List(ctx context.Context, tenant uuid.UUID, invoice uuid.UUID, outcome string, limit int) ([]Dispute, error) {
	return d.list(ctx, tenant, invoice, outcome, nil, limit)
}

// One returns the tenant's case with that id: ErrNoDispute for another tenant's, or none.
func (d Disputes) One(ctx context.Context, tenant uuid.UUID, id int64) (Dispute, error) {
	rows, err := d.list(ctx, tenant, uuid.Nil, "", &id, 1)
	if err != nil {
		return Dispute{}, err
	}
	if len(rows) == 0 {
		return Dispute{}, ErrNoDispute
	}
	return rows[0], nil
}

func (d Disputes) list(ctx context.Context, tenant uuid.UUID, invoice uuid.UUID, outcome string, id *int64, limit int) ([]Dispute, error) {
	p := sqlc.ListDisputesParams{TenantID: tenant, Outcome: outcome, ID: id, RowLimit: int32(min(max(limit, 1), 1000))}
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
			Outcome: r.Outcome, Formal: r.Formal, Status: r.ProviderStatus, Reason: r.Reason, EvidenceDue: r.EvidenceDueAt, OpenedAt: r.OpenedAt, ClosedAt: r.ClosedAt,
			FeeMinor: r.FeeMinor, HandledAt: r.HandledAt, HandledBy: r.HandledBy, NoteCount: r.NoteCount}
		if r.FeeCurrency != nil {
			out[i].FeeCurrency = *r.FeeCurrency
		}
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

// ErrNoDispute: the tenant has no case with that id.
var ErrNoDispute = errors.New("store: no such dispute")

// DisputeEvent is one thing the seller did with a case: a note, the mark that it was handled, or the mark lifted.
type DisputeEvent struct {
	ID      int64     `json:"id"`
	Action  string    `json:"action"`
	Note    string    `json:"note,omitempty"`
	Actor   string    `json:"actor"`
	Created time.Time `json:"create_time"`
}

// ActorOf is who the gateway says is acting, as the events and the marks keep it: the person's address, else the kind and id of the API key, else "system".
func ActorOf(ctx context.Context) string {
	a := audit.FromContext(ctx)
	switch {
	case a.Email != "":
		return a.Email
	case a.ID != "":
		return a.Type + ":" + a.ID
	}
	return "system"
}

// Annotate writes what the seller does with a case, in one transaction with its audit entry: a note (note is its text), or the mark that the case was handled
// (action "handled", or "unhandled" to lift it). Marking a case that is marked, or lifting what is not, changes nothing and writes nothing: it reports false. The audit entry
// names the case and the action and keeps the note's text out (it is free text, and stays in payment_dispute_events).
func (d Disputes) Annotate(ctx context.Context, tenant uuid.UUID, id int64, action, note, actor string) (event DisputeEvent, changed bool, err error) {
	err = tx(ctx, d.DB, tenant, func(pt pgx.Tx, q *sqlc.Queries) error {
		changed = false
		r, err := q.GetDisputeForUpdate(ctx, sqlc.GetDisputeForUpdateParams{TenantID: tenant, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoDispute
		}
		if err != nil {
			return err
		}
		switch {
		case action == "handled" && r.HandledAt != nil, action == "unhandled" && r.HandledAt == nil:
			return nil
		case action == "handled":
			now := time.Now().UTC()
			err = q.SetDisputeHandled(ctx, sqlc.SetDisputeHandledParams{TenantID: tenant, ID: id, HandledAt: &now, HandledBy: actor})
		case action == "unhandled":
			err = q.SetDisputeHandled(ctx, sqlc.SetDisputeHandledParams{TenantID: tenant, ID: id})
		}
		if err != nil {
			return err
		}
		row, err := q.InsertDisputeEvent(ctx, sqlc.InsertDisputeEventParams{TenantID: tenant, Environment: r.Environment, DisputeID: id, Action: action, Note: note, Actor: actor})
		if err != nil {
			return err
		}
		detail := map[string]any{"provider": r.Provider, "dispute_ref": r.DisputeRef, "environment": r.Environment}
		if action == "note" {
			detail["note_length"] = len([]rune(note))
		}
		if r.InvoiceID != nil {
			detail["invoice_id"] = r.InvoiceID.String()
		}
		if err := audit.Record(ctx, pt, audit.Event{TenantID: tenant, Action: "dispute." + map[string]string{"note": "note_added", "handled": "handled", "unhandled": "unhandled"}[action],
			TargetType: "dispute", TargetID: strconv.FormatInt(id, 10), Summary: map[string]string{"note": "added a note to", "handled": "marked as handled", "unhandled": "lifted the handled mark of"}[action] + " the dispute " + r.DisputeRef, Detail: detail}); err != nil {
			return err
		}
		event, changed = DisputeEvent{ID: row.ID, Action: action, Note: note, Actor: actor, Created: row.CreatedAt}, true
		return nil
	})
	return event, changed, err
}

// Events lists what the seller did with a case, newest first. A case of another tenant is no case: ErrNoDispute.
func (d Disputes) Events(ctx context.Context, tenant uuid.UUID, id int64) ([]DisputeEvent, error) {
	var out []DisputeEvent
	err := tx(ctx, d.DB, tenant, func(_ pgx.Tx, q *sqlc.Queries) error {
		if _, err := q.GetDisputeForUpdate(ctx, sqlc.GetDisputeForUpdateParams{TenantID: tenant, ID: id}); errors.Is(err, pgx.ErrNoRows) {
			return ErrNoDispute
		} else if err != nil {
			return err
		}
		rows, err := q.ListDisputeEvents(ctx, sqlc.ListDisputeEventsParams{TenantID: tenant, DisputeID: id, RowLimit: 200})
		if err != nil {
			return err
		}
		out = make([]DisputeEvent, len(rows))
		for i, r := range rows {
			out[i] = DisputeEvent{ID: r.ID, Action: r.Action, Note: r.Note, Actor: r.Actor, Created: r.CreatedAt}
		}
		return nil
	})
	return out, err
}
