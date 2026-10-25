package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/model"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
)

// Event types of billing-svc this module reads (the names plans-svc's webhook list has).
const (
	eventInvoicePaid        = "invoice.paid"
	eventInvoiceVoided      = "invoice.voided"
	eventCreditNoteCreated  = "credit_note.created"
	reversalVoid            = "void"
	reversalCreditNote      = "credit_note"
	reversalChargeback      = "chargeback"
	reversalManual          = "manual"
	commissionStatusPending = "pending"
)

// amount reads a minor-unit amount that arrives as a string (like in the REST API) or as a number.
type amount struct {
	n   int64
	set bool
}

func (a *amount) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch x := v.(type) {
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return err
		}
		a.n, a.set = n, true
	case float64:
		a.n, a.set = int64(x), true
	case nil:
	default:
		return fmt.Errorf("not an amount: %v", v)
	}
	return nil
}

// eventData is the part of an invoice or credit note event the program reads.
type eventData struct {
	InvoiceID    string `json:"invoice_id"`
	Number       string `json:"number"`
	CustomerID   string `json:"customer_id"`
	Currency     string `json:"currency"`
	Environment  string `json:"environment"`
	CreditNoteID string `json:"credit_note_id"`
	TotalMinor   amount `json:"total_minor"`
	TaxMinor     amount `json:"tax_minor"`
	NetMinor     amount `json:"net_minor"`
}

// OnTenantEvent is told, in the transaction that records a tenant event, what happened in billing. A paid invoice earns its customer's affiliate a commission;
// a void or a credit note takes some back. It is idempotent per event, because a commission is one per invoice and a reversal is one per key. An event it cannot
// make sense of is logged and left alone: refusing it would only make billing send it again for ever.
func OnTenantEvent(ctx context.Context, q *sqlc.Queries, typ string, _ uuid.UUID, data json.RawMessage, at time.Time) error {
	switch typ {
	case eventInvoicePaid, eventInvoiceVoided, eventCreditNoteCreated:
	default:
		return nil
	}
	var d eventData
	if err := json.Unmarshal(data, &d); err != nil {
		slog.WarnContext(ctx, "affiliates: an event with unreadable data is ignored", "type", typ, "error", err)
		return nil
	}
	invoiceID, err := uuid.Parse(d.InvoiceID)
	if err != nil {
		slog.WarnContext(ctx, "affiliates: an event without an invoice id is ignored", "type", typ)
		return nil
	}
	t, err := tenantsqry.Current(ctx, q)
	if err != nil {
		return err
	}
	// The environment of the event must be the tenant's: the sandbox and live never mix (ADR 0022). An event that carries none comes from a billing-svc
	// that predates the field and is taken as the tenant's own.
	if d.Environment != "" {
		have, perr := env.Parse(d.Environment)
		want, werr := env.Parse(t.Environment)
		if perr != nil || werr != nil || env.Same(have, want, "affiliate event") != nil {
			slog.ErrorContext(ctx, "affiliates: an event of another environment is refused", "type", typ, "event_environment", d.Environment, "tenant_environment", t.Environment)
			return nil
		}
	}
	switch typ {
	case eventInvoicePaid:
		return commissionOnPaid(ctx, q, d, invoiceID, at)
	case eventInvoiceVoided:
		_, err := Reverse(ctx, q, ReverseInput{InvoiceID: invoiceID, Kind: reversalVoid, Key: reversalVoid + ":" + invoiceID.String(), Now: at})
		return err
	default:
		if d.CreditNoteID == "" || !d.TotalMinor.set {
			slog.WarnContext(ctx, "affiliates: a credit note event without an id or an amount is ignored")
			return nil
		}
		_, err := Reverse(ctx, q, ReverseInput{InvoiceID: invoiceID, Kind: reversalCreditNote, Key: reversalCreditNote + ":" + d.CreditNoteID, CreditedMinor: d.TotalMinor.n, Now: at})
		return err
	}
}

// commissionOnPaid gives the affiliate who brought the customer a commission on the invoice, if the program and the affiliate are active, the customer's term has
// not run out and the base is more than nothing. A replay finds the commission there and writes nothing.
func commissionOnPaid(ctx context.Context, q *sqlc.Queries, d eventData, invoiceID uuid.UUID, at time.Time) error {
	customerID, err := uuid.Parse(d.CustomerID)
	if err != nil {
		slog.WarnContext(ctx, "affiliates: a paid invoice without a customer id is ignored", "invoice_id", invoiceID)
		return nil
	}
	attr, err := q.GetAttribution(ctx, customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	aff, err := q.GetAffiliate(ctx, attr.AffiliateID)
	if err != nil {
		return err
	}
	if aff.Status != model.AffiliateActive {
		// A suspended affiliate earns nothing new; what was approved is theirs until the owner has looked at it.
		return nil
	}
	prog, err := q.GetAffiliateProgram(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	rules := query.ProgramRules(prog)
	if !rules.Active {
		return nil
	}
	net := d.NetMinor.n
	if !d.NetMinor.set {
		net = max(d.TotalMinor.n-d.TaxMinor.n, 0)
	}
	hist, err := q.CommissionHistory(ctx, customerID)
	if err != nil {
		return err
	}
	var first *time.Time
	if hist.Prior > 0 {
		first = &hist.FirstEarned
	}
	if !model.TermAllows(rules, hist.Prior, first, at) {
		return nil
	}
	cents, ok := model.CommissionAmount(rules, d.Currency, net)
	if !ok {
		return nil
	}
	status := commissionStatusPending
	if rules.HoldDays == 0 {
		status = "approved"
	}
	_, err = q.InsertCommission(ctx, sqlc.InsertCommissionParams{AffiliateID: aff.ID, CustomerID: customerID, InvoiceID: invoiceID, InvoiceNumber: d.Number, Currency: d.Currency,
		InvoiceTotalMinor: d.TotalMinor.n, NetMinor: net, AmountMinor: cents, Status: status, EarnedAt: at, ApproveAfter: model.ApproveAfter(at, rules.HoldDays)})
	return err
}

// ReverseInput takes (some of) a commission back.
type ReverseInput struct {
	InvoiceID uuid.UUID
	// Kind is void, credit_note, chargeback or manual; Key names this reversal, so that the same event takes back once.
	Kind string
	Key  string
	// CreditedMinor is what a credit note credited of the invoice (gross).
	CreditedMinor int64
	// AmountMinor is how much of the commission to take back for a chargeback or by hand; 0 is all that is left.
	AmountMinor int64
	Note        string
	Now         time.Time
}

// ReverseResult says what a reversal did.
type ReverseResult struct {
	// Found is false when the invoice earned no commission (nobody was attributed, or it was not paid).
	Found bool
	// Reversed is how much of the commission was taken back by this call; 0 on a repeat.
	Reversed int64
}

// Reverse takes back what a void, a credit note, a chargeback or the owner's hand says of the commission of an invoice. A commission that is still waiting or
// approved loses the amount where it stands; one that was paid is netted by a negative row of its own, approved at once, which the next payout takes with its
// own commissions. A credit note takes back in proportion to how much of the invoice has been credited in all; a void or chargeback takes back what is left.
// The commission is locked while this runs, so reversals of one commission follow one another and a repeated key takes back nothing.
func Reverse(ctx context.Context, q *sqlc.Queries, in ReverseInput) (ReverseResult, error) {
	c, err := q.LockCommissionOfInvoice(ctx, in.InvoiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReverseResult{}, nil
	}
	if err != nil {
		return ReverseResult{}, err
	}
	if _, err := q.GetReversal(ctx, in.Key); err == nil {
		return ReverseResult{Found: true}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ReverseResult{}, err
	}
	remaining := c.AmountMinor - c.ReversedMinor
	var delta int64
	credited := in.CreditedMinor
	if in.Kind == reversalCreditNote {
		before, err := q.SumCreditedOfCommission(ctx, c.ID)
		if err != nil {
			return ReverseResult{}, err
		}
		delta = model.ReversalTarget(c.AmountMinor, before+credited, c.InvoiceTotalMinor) - c.ReversedMinor
	} else {
		delta = remaining
		if in.AmountMinor > 0 {
			delta = in.AmountMinor
		}
	}
	delta = max(min(delta, remaining), 0)
	if err := q.InsertReversal(ctx, sqlc.InsertReversalParams{Key: in.Key, CommissionID: c.ID, InvoiceID: in.InvoiceID, Kind: in.Kind, CreditedMinor: credited, AmountMinor: delta, Note: in.Note}); err != nil {
		return ReverseResult{}, err
	}
	if delta == 0 {
		return ReverseResult{Found: true}, nil
	}
	if c.Status == "paid" {
		if err := q.NoteReversalOfPaidCommission(ctx, sqlc.NoteReversalOfPaidCommissionParams{ID: c.ID, Delta: delta}); err != nil {
			return ReverseResult{}, err
		}
		if err := q.InsertReversalRow(ctx, sqlc.InsertReversalRowParams{Delta: delta, Key: in.Key, At: in.Now, ParentID: c.ID}); err != nil {
			return ReverseResult{}, err
		}
		return ReverseResult{Found: true, Reversed: delta}, nil
	}
	if err := q.TakeBackFromUnpaidCommission(ctx, sqlc.TakeBackFromUnpaidCommissionParams{ID: c.ID, Delta: delta}); err != nil {
		return ReverseResult{}, err
	}
	return ReverseResult{Found: true, Reversed: delta}, nil
}
