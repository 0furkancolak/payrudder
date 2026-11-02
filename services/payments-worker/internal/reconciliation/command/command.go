// Package command keeps what a reconciliation finds and what an operator does about it (docs/plans/22 G7): a run compares billing, the ledger and the settlement lines and
// keeps the discrepancies; an operator assigns, explains or resolves them. A resolution can write one fact to the payment ledger (the existing append, source
// "reconciliation") and nothing else: it never calls a provider and never changes an invoice; a credit note is issued through the credit note API and named in the note.
package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/reconciliation/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// Deps is what the commands use.
type Deps struct {
	Query         query.Deps
	Discrepancies store.Discrepancies
	Ledger        store.Ledger
	Settlements   store.Settlements
	// Resolver builds the adapter of a tenant's provider account, for reading its settlement.
	Resolver target.Resolver
	Log      *slog.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// MaxKept is how many discrepancies one run keeps. A run that found more keeps these and clears nothing: it cannot tell what it did not look at from what is gone.
const MaxKept = 5000

// Who started a run.
const (
	ByOperator = "operator"
	BySchedule = "schedule"
)

// RunInput names a run: the tenant's environment is the caller's word (the schedule knows it, an operator picks it) and decides what a sandbox reset may delete.
type RunInput struct {
	Tenant      uuid.UUID
	Environment string
	Period      string
	StartedBy   string
	Actor       string
}

// RunResult is what a run saw and changed.
type RunResult struct {
	Period       string         `json:"period"`
	PaidInvoices int            `json:"paid_invoices"`
	Charges      int            `json:"successful_charges"`
	Found        int            `json:"found"`
	Opened       int            `json:"opened"`
	Reopened     int            `json:"reopened"`
	Cleared      int            `json:"cleared"`
	Counts       map[string]int `json:"discrepancy_counts"`
	// Truncated: more was found than a run keeps, so nothing was cleared.
	Truncated bool `json:"truncated,omitempty"`
}

var (
	meter    = otel.Meter("github.com/0furkancolak/payrudder/services/payments-worker/reconciliation")
	runs     = counter("reconciliation_runs", "Reconciliation runs, by who started them and how they ended")
	opened   = counter("reconciliation_discrepancies_opened", "Discrepancies a reconciliation run found for the first time or found again, by kind")
	resolved = counter("reconciliation_discrepancies_resolved", "Discrepancies an operator resolved, by resolution")
)

func counter(name, desc string) metric.Int64Counter {
	c, err := meter.Int64Counter(name, metric.WithDescription(desc))
	if err != nil {
		panic(err)
	}
	return c
}

// Run compares one period of one tenant and keeps what it finds (store.Discrepancies.Sync): the same run twice changes nothing the second time.
func Run(ctx context.Context, d Deps, in RunInput) (*RunResult, error) {
	if _, err := env.Parse(in.Environment); err != nil {
		return nil, refuse.New(http.StatusBadRequest, "payments.request.bad_environment", "environment must be sandbox or live")
	}
	q := d.Query
	q.MaxListed = MaxKept
	rec, err := query.Get(ctx, q, in.Tenant, in.Period)
	if err != nil {
		d.recordRun(ctx, in, store.Run{Result: "failed", Error: err.Error()})
		runs.Add(ctx, 1, metric.WithAttributes(attribute.String("started_by", in.StartedBy), attribute.String("result", "failed")))
		return nil, err
	}
	found := make([]store.Finding, len(rec.Discrepancies))
	for i, x := range rec.Discrepancies {
		found[i] = x.Finding()
	}
	res, err := d.Discrepancies.Sync(ctx, in.Tenant, in.Environment, in.Period, found, !rec.Truncated)
	if err != nil {
		d.recordRun(ctx, in, store.Run{Result: "failed", Error: err.Error()})
		runs.Add(ctx, 1, metric.WithAttributes(attribute.String("started_by", in.StartedBy), attribute.String("result", "failed")))
		return nil, fmt.Errorf("keep the discrepancies: %w", err)
	}
	result := "ok"
	if rec.Truncated {
		result = "truncated"
	}
	d.recordRun(ctx, in, store.Run{Result: result, PaidInvoices: int64(rec.PaidInvoices), Charges: int64(rec.Charges), Found: int64(len(found)), Opened: int64(res.Opened),
		Reopened: int64(res.Reopened), Cleared: int64(res.Cleared)})
	runs.Add(ctx, 1, metric.WithAttributes(attribute.String("started_by", in.StartedBy), attribute.String("result", result)))
	for _, kind := range res.Kinds {
		opened.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", kind)))
	}
	return &RunResult{Period: in.Period, PaidInvoices: rec.PaidInvoices, Charges: rec.Charges, Found: len(found), Opened: res.Opened, Reopened: res.Reopened, Cleared: res.Cleared,
		Counts: rec.Counts, Truncated: rec.Truncated}, nil
}

// recordRun appends the run; a failure to is logged and does not fail the run (the discrepancies are what matter).
func (d Deps) recordRun(ctx context.Context, in RunInput, r store.Run) {
	r.Environment, r.Period, r.StartedBy, r.Actor = in.Environment, in.Period, in.StartedBy, in.Actor
	if err := d.Discrepancies.RecordRun(ctx, in.Tenant, r); err != nil && d.Log != nil {
		d.Log.WarnContext(ctx, "reconciliation run not recorded", "tenant", in.Tenant, "period", in.Period, "err", err)
	}
}

// The ways a discrepancy ends by hand.
const (
	// LedgerFact: the payment is real and the ledger lacks it; the operator names the provider and its reference and one ledger fact is written (paid_without_charge only).
	LedgerFact = "ledger_fact"
	// CreditNote: a credit note was issued for it; the note says which.
	CreditNote = "credit_note"
	// RefundedAtProvider: the money was given back at the provider, outside Payrudder; the note says how it was checked.
	RefundedAtProvider = "refunded_at_provider"
	// CorrectedInBilling: billing's side was corrected (an invoice voided, a payment recorded).
	CorrectedInBilling = "corrected_in_billing"
	// NotAnError: looked at, and the difference is right; the note says why.
	NotAnError = "not_an_error"
	Other      = "other"
)

// Resolutions are the resolutions an operator may give. A run gives "cleared" to what it no longer finds; an operator cannot.
var Resolutions = []string{LedgerFact, CreditNote, RefundedAtProvider, CorrectedInBilling, NotAnError, Other}

const (
	maxNote  = 1000
	maxOwner = 120
	// MaxBulk is how many discrepancies one bulk resolution takes.
	MaxBulk = 100
)

func notFound() error {
	return refuse.New(http.StatusNotFound, "payments.reconcile.not_found", "no such discrepancy")
}

func clean(s string, max int, what string) (string, error) {
	s = strings.TrimSpace(s)
	if len([]rune(s)) > max {
		return "", refuse.New(http.StatusBadRequest, "payments.reconcile.too_long", fmt.Sprintf("%s is at most %d characters", what, max))
	}
	return s, nil
}

// Assign gives a discrepancy an owner (empty clears it).
func Assign(ctx context.Context, d Deps, tenant uuid.UUID, id int64, owner, actor string) (store.Discrepancy, error) {
	owner, err := clean(owner, maxOwner, "owner")
	if err != nil {
		return store.Discrepancy{}, err
	}
	out, _, err := d.apply(ctx, tenant, id, store.Change{Action: "assign", Owner: owner, Actor: actor})
	return out, err
}

// Explain marks a discrepancy as known: it stays in the queue, with the reason. A note is required.
func Explain(ctx context.Context, d Deps, tenant uuid.UUID, id int64, note, owner, actor string) (store.Discrepancy, error) {
	note, owner, err := noteAndOwner(note, owner)
	if err != nil {
		return store.Discrepancy{}, err
	}
	out, _, err := d.apply(ctx, tenant, id, store.Change{Action: "explain", Note: note, Owner: owner, Actor: actor})
	return out, err
}

func noteAndOwner(note, owner string) (string, string, error) {
	note, err := clean(note, maxNote, "note")
	if err != nil {
		return "", "", err
	}
	if note == "" {
		return "", "", refuse.New(http.StatusBadRequest, "payments.reconcile.note_required", "say what you found out: a note is required")
	}
	owner, err = clean(owner, maxOwner, "owner")
	return note, owner, err
}

func (d Deps) apply(ctx context.Context, tenant uuid.UUID, id int64, c store.Change) (store.Discrepancy, bool, error) {
	out, changed, err := d.Discrepancies.Apply(ctx, tenant, id, c)
	switch {
	case errors.Is(err, store.ErrNoDiscrepancy):
		return out, false, notFound()
	case errors.Is(err, store.ErrAlreadyResolved):
		return out, false, refuse.New(http.StatusConflict, "payments.reconcile.resolved", "the discrepancy is resolved; a run that finds it again reopens it")
	}
	return out, changed, err
}

// Fact is the part of a ledger-fact resolution the operator supplies: who the provider is and what it calls the payment. The amount, currency and invoice are the
// discrepancy's own (what billing calls paid), never the request's.
type Fact struct {
	Provider    string `json:"provider"`
	ProviderRef string `json:"provider_ref"`
}

// ResolveInput is a resolution.
type ResolveInput struct {
	Resolution string
	Note       string
	Owner      string
	Actor      string
	Fact       *Fact
}

func checkResolution(in ResolveInput) (ResolveInput, error) {
	if !slices.Contains(Resolutions, in.Resolution) {
		return in, refuse.New(http.StatusBadRequest, "payments.reconcile.bad_resolution", "resolution must be one of "+strings.Join(Resolutions, ", "))
	}
	var err error
	in.Note, in.Owner, err = noteAndOwner(in.Note, in.Owner)
	return in, err
}

// Resolve ends a discrepancy. The same resolution twice is one resolution.
func Resolve(ctx context.Context, d Deps, tenant uuid.UUID, id int64, in ResolveInput) (store.Discrepancy, error) {
	in, err := checkResolution(in)
	if err != nil {
		return store.Discrepancy{}, err
	}
	if in.Resolution == LedgerFact {
		if err := d.writeFact(ctx, tenant, id, in); err != nil {
			return store.Discrepancy{}, err
		}
	} else if in.Fact != nil {
		return store.Discrepancy{}, refuse.New(http.StatusBadRequest, "payments.reconcile.bad_ledger_fact", "ledger_fact belongs to the ledger_fact resolution only")
	}
	out, changed, err := d.apply(ctx, tenant, id, store.Change{Action: "resolve", Resolution: in.Resolution, Note: in.Note, Owner: in.Owner, Actor: in.Actor})
	if err == nil && changed {
		resolved.Add(ctx, 1, metric.WithAttributes(attribute.String("resolution", in.Resolution)))
	}
	return out, err
}

// writeFact writes the ledger fact of a ledger_fact resolution: a successful charge of the invoice's total, source reconciliation, for the provider and reference the
// operator names. It is the existing append (a fact reported twice is one row), and only for a payment billing calls paid that the ledger lacks.
func (d Deps) writeFact(ctx context.Context, tenant uuid.UUID, id int64, in ResolveInput) error {
	bad := func(msg string) error {
		return refuse.New(http.StatusBadRequest, "payments.reconcile.bad_ledger_fact", msg)
	}
	disc, _, err := d.Discrepancies.Get(ctx, tenant, id)
	if errors.Is(err, store.ErrNoDiscrepancy) {
		return notFound()
	}
	if err != nil {
		return err
	}
	if disc.Status == store.DiscResolved {
		return nil // nothing to write for what is already resolved; Apply answers it
	}
	invoice, perr := uuid.Parse(disc.InvoiceID)
	switch {
	case disc.Kind != query.PaidWithoutCharge:
		return bad("a ledger fact can resolve paid_without_charge only: the others disagree about a payment that is in the ledger")
	case in.Fact == nil || !payments.ValidAccountKey(in.Fact.Provider) || strings.TrimSpace(in.Fact.ProviderRef) == "":
		return bad("ledger_fact needs the provider (an account key such as stripe or stripe~eu) and the provider's reference of the payment")
	case perr != nil || disc.BilledMinor <= 0 || disc.Currency == "":
		return bad("the discrepancy names no invoice amount to record")
	}
	detail := fmt.Sprintf("written by reconciliation discrepancy %d, resolved by %s: %s", id, in.Actor, in.Note)
	_, err = d.Ledger.Append(ctx, store.LedgerEntry{TenantID: tenant, InvoiceID: invoice, Provider: in.Fact.Provider, Environment: disc.Environment, Kind: store.KindCharge, Status: store.StatusSucceeded,
		AmountMinor: disc.BilledMinor, Currency: disc.Currency, ProviderRef: strings.TrimSpace(in.Fact.ProviderRef), Source: store.SourceReconciliation, Detail: detail})
	return err
}

// Outcome is what a bulk resolution did to one discrepancy.
type Outcome struct {
	ID int64 `json:"id"`
	// Result is "resolved", "unchanged" (it was resolved already), "not_found" or "failed".
	Result string `json:"result"`
	Key    string `json:"error_key,omitempty"`
}

// ResolveMany resolves up to MaxBulk discrepancies with one resolution. Each is its own transaction and its own event: one that fails does not undo the others, and the
// answer says which. The ledger_fact resolution needs a reference per payment and is not offered in bulk.
func ResolveMany(ctx context.Context, d Deps, tenant uuid.UUID, ids []int64, in ResolveInput) ([]Outcome, error) {
	if len(ids) == 0 || len(ids) > MaxBulk {
		return nil, refuse.New(http.StatusBadRequest, "payments.reconcile.too_many", fmt.Sprintf("name 1 to %d discrepancies", MaxBulk))
	}
	if in.Resolution == LedgerFact || in.Fact != nil {
		return nil, refuse.New(http.StatusBadRequest, "payments.reconcile.bad_resolution", "ledger_fact needs a reference per payment: resolve those one by one")
	}
	in, err := checkResolution(in)
	if err != nil {
		return nil, err
	}
	out := make([]Outcome, 0, len(ids))
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		before, _, err := d.Discrepancies.Get(ctx, tenant, id)
		o := Outcome{ID: id, Result: "resolved"}
		switch {
		case errors.Is(err, store.ErrNoDiscrepancy):
			o.Result, o.Key = "not_found", "payments.reconcile.not_found"
		case err != nil:
			return out, err
		case before.Status == store.DiscResolved:
			o.Result = "unchanged"
		default:
			if _, err := Resolve(ctx, d, tenant, id, in); err != nil {
				var refusal *refuse.Error
				if !errors.As(err, &refusal) {
					return out, err
				}
				o.Result, o.Key = "failed", refusal.Key
			}
		}
		out = append(out, o)
	}
	return out, nil
}
