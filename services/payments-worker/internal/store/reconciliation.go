package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// The statuses of a discrepancy (reconciliation_discrepancies.status) and the resolution a run gives one it no longer finds.
const (
	DiscOpen        = "open"
	DiscExplained   = "explained"
	DiscResolved    = "resolved"
	ResolvedCleared = "cleared"
)

// ErrNoDiscrepancy: the tenant has no discrepancy with that id. ErrAlreadyResolved: it was resolved, and what was asked cannot be done to a resolved one.
var (
	ErrNoDiscrepancy   = errors.New("store: no such discrepancy")
	ErrAlreadyResolved = errors.New("store: the discrepancy is resolved")
)

// Finding is one thing that does not agree, as a run found it.
type Finding struct {
	Kind          string
	InvoiceID     string
	InvoiceNumber string
	Provider      string
	ProviderRef   string
	Currency      string
	Detail        string
	// BilledMinor is the invoice's total, LedgerMinor what the ledger holds (for a fee: the fee expected), SettledMinor what the provider reported.
	BilledMinor, LedgerMinor, SettledMinor int64
}

// Fingerprint names the thing a finding is about, so that the same thing found again is the same discrepancy: its kind, invoice, provider and provider reference.
func (f Finding) Fingerprint() string {
	h := sha256.Sum256([]byte(strings.Join([]string{f.Kind, f.InvoiceID, f.Provider, f.ProviderRef}, "|")))
	return hex.EncodeToString(h[:])
}

// Discrepancy is a kept discrepancy as the operator API shows it.
type Discrepancy struct {
	ID            int64      `json:"id"`
	Environment   string     `json:"environment"`
	Period        string     `json:"period"`
	Kind          string     `json:"kind"`
	InvoiceID     string     `json:"invoice_id,omitempty"`
	InvoiceNumber string     `json:"number,omitempty"`
	Provider      string     `json:"provider,omitempty"`
	ProviderRef   string     `json:"provider_ref,omitempty"`
	Currency      string     `json:"currency,omitempty"`
	BilledMinor   int64      `json:"billed_minor,omitempty"`
	LedgerMinor   int64      `json:"ledger_minor,omitempty"`
	SettledMinor  int64      `json:"settled_minor,omitempty"`
	Detail        string     `json:"detail,omitempty"`
	Status        string     `json:"status"`
	Owner         string     `json:"owner,omitempty"`
	Note          string     `json:"note,omitempty"`
	Resolution    string     `json:"resolution,omitempty"`
	FirstSeenAt   time.Time  `json:"first_seen_at"`
	LastSeenAt    time.Time  `json:"last_seen_at"`
	ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
}

func discrepancyOf(r sqlc.ReconciliationDiscrepancy) Discrepancy {
	d := Discrepancy{ID: r.ID, Environment: r.Environment, Period: r.Period, Kind: r.Kind, InvoiceNumber: r.InvoiceNumber, Provider: r.Provider, ProviderRef: r.ProviderRef, Currency: r.Currency,
		BilledMinor: r.BilledMinor, LedgerMinor: r.LedgerMinor, SettledMinor: r.SettledMinor, Detail: r.Detail, Status: r.Status, Owner: r.Owner, Note: r.Note,
		Resolution: r.Resolution, FirstSeenAt: r.FirstSeenAt, LastSeenAt: r.LastSeenAt, ResolvedAt: r.ResolvedAt}
	if r.InvoiceID != nil {
		d.InvoiceID = r.InvoiceID.String()
	}
	return d
}

// Event is one step in the life of a discrepancy.
type Event struct {
	ID         int64     `json:"id"`
	Action     string    `json:"action"`
	Actor      string    `json:"actor"`
	FromStatus string    `json:"from_status,omitempty"`
	ToStatus   string    `json:"to_status,omitempty"`
	Resolution string    `json:"resolution,omitempty"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Discrepancies is the kept discrepancies of reconciliation, their events and the runs that found them.
type Discrepancies struct{ DB DB }

// SyncResult is what a run changed.
type SyncResult struct {
	Opened, Reopened, Cleared int
	// Kinds are the kinds of what was opened or reopened, one entry each.
	Kinds []string
}

// SystemActor is the actor of what a run does by itself.
const SystemActor = "system"

// Sync keeps what a run found for a period: a thing found for the first time is opened, one that was resolved and is found again is reopened (resolving it did not
// make it go away), one still there is refreshed, and when complete (the run saw everything, nothing was cut off) one that is no longer found is resolved as cleared.
// Every change but a refresh is an event. Two runs of one tenant and period take turns.
func (d Discrepancies) Sync(ctx context.Context, tenant uuid.UUID, environment, period string, found []Finding, complete bool) (SyncResult, error) {
	var res SyncResult
	err := tx(ctx, d.DB, tenant, func(_ pgx.Tx, q *sqlc.Queries) error {
		res = SyncResult{}
		if err := q.LockReconciliation(ctx, sqlc.LockReconciliationParams{TenantID: tenant.String(), Period: period}); err != nil {
			return err
		}
		rows, err := q.ListDiscrepanciesOfPeriod(ctx, sqlc.ListDiscrepanciesOfPeriodParams{TenantID: tenant, Period: period})
		if err != nil {
			return err
		}
		byPrint := make(map[string]sqlc.ReconciliationDiscrepancy, len(rows))
		for _, r := range rows {
			byPrint[r.Fingerprint] = r
		}
		event := func(id int64, action, from, to, resolution, note string) error {
			return q.InsertReconciliationEvent(ctx, sqlc.InsertReconciliationEventParams{TenantID: tenant, Environment: environment, DiscrepancyID: id, Action: action, Actor: SystemActor,
				FromStatus: from, ToStatus: to, Resolution: resolution, Note: note})
		}
		seen := map[string]bool{}
		for _, f := range found {
			fp := f.Fingerprint()
			if seen[fp] {
				continue
			}
			seen[fp] = true
			row, known := byPrint[fp]
			switch {
			case !known:
				var invoice *uuid.UUID
				if id, err := uuid.Parse(f.InvoiceID); err == nil {
					invoice = &id
				}
				id, err := q.InsertDiscrepancy(ctx, sqlc.InsertDiscrepancyParams{TenantID: tenant, Environment: environment, Period: period, Fingerprint: fp, Kind: f.Kind, InvoiceID: invoice,
					InvoiceNumber: f.InvoiceNumber, Provider: f.Provider, ProviderRef: f.ProviderRef, Currency: f.Currency, BilledMinor: f.BilledMinor, LedgerMinor: f.LedgerMinor,
					SettledMinor: f.SettledMinor, Detail: f.Detail})
				if err != nil {
					return err
				}
				if err := event(id, "detected", "", DiscOpen, "", f.Detail); err != nil {
					return err
				}
				res.Opened++
				res.Kinds = append(res.Kinds, f.Kind)
			case row.Status == DiscResolved:
				if err := q.ReopenDiscrepancy(ctx, sqlc.ReopenDiscrepancyParams{TenantID: tenant, ID: row.ID, InvoiceNumber: f.InvoiceNumber, Currency: f.Currency, BilledMinor: f.BilledMinor,
					LedgerMinor: f.LedgerMinor, SettledMinor: f.SettledMinor, Detail: f.Detail}); err != nil {
					return err
				}
				if err := event(row.ID, "reopened", DiscResolved, DiscOpen, "", "found again after it was resolved as "+row.Resolution); err != nil {
					return err
				}
				res.Reopened++
				res.Kinds = append(res.Kinds, f.Kind)
			default:
				if err := q.SeeDiscrepancy(ctx, sqlc.SeeDiscrepancyParams{TenantID: tenant, ID: row.ID, InvoiceNumber: f.InvoiceNumber, Currency: f.Currency, BilledMinor: f.BilledMinor,
					LedgerMinor: f.LedgerMinor, SettledMinor: f.SettledMinor, Detail: f.Detail}); err != nil {
					return err
				}
			}
		}
		if !complete {
			return nil
		}
		for _, r := range rows {
			if seen[r.Fingerprint] || r.Status == DiscResolved {
				continue
			}
			n, err := q.ClearDiscrepancy(ctx, sqlc.ClearDiscrepancyParams{TenantID: tenant, ID: r.ID})
			if err != nil {
				return err
			}
			if n == 1 {
				if err := event(r.ID, "cleared", r.Status, DiscResolved, ResolvedCleared, "a later run no longer finds it"); err != nil {
					return err
				}
				res.Cleared++
			}
		}
		return nil
	})
	return res, err
}

// Filter picks discrepancies. An empty field matches all; AfterID continues a listing (the ids below it, newest first).
type Filter struct {
	Period   string
	Statuses []string
	Kinds    []string
	Owner    string
	Invoice  string
	AfterID  int64
	Limit    int
}

// List returns a tenant's discrepancies, newest first.
func (d Discrepancies) List(ctx context.Context, tenant uuid.UUID, f Filter) ([]Discrepancy, error) {
	rows, err := get(ctx, d.DB, tenant, func(q *sqlc.Queries) ([]sqlc.ReconciliationDiscrepancy, error) {
		return q.ListDiscrepancies(ctx, sqlc.ListDiscrepanciesParams{TenantID: tenant, Period: f.Period, Statuses: nonNil(f.Statuses), Kinds: nonNil(f.Kinds), Owner: f.Owner,
			Invoice: f.Invoice, AfterID: f.AfterID, RowLimit: int64(f.Limit)})
	})
	if err != nil {
		return nil, err
	}
	out := make([]Discrepancy, len(rows))
	for i, r := range rows {
		out[i] = discrepancyOf(r)
	}
	return out, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Count is how many discrepancies a tenant has of a status and kind.
type Count struct {
	Status string `json:"status"`
	Kind   string `json:"kind"`
	N      int64  `json:"n"`
}

// Counts returns the numbers behind a queue's filters: how many of each status and kind, for a period or (empty) all.
func (d Discrepancies) Counts(ctx context.Context, tenant uuid.UUID, period string) ([]Count, error) {
	rows, err := get(ctx, d.DB, tenant, func(q *sqlc.Queries) ([]sqlc.CountDiscrepanciesRow, error) {
		return q.CountDiscrepancies(ctx, sqlc.CountDiscrepanciesParams{TenantID: tenant, Period: period})
	})
	if err != nil {
		return nil, err
	}
	out := make([]Count, len(rows))
	for i, r := range rows {
		out[i] = Count{Status: r.Status, Kind: r.Kind, N: r.N}
	}
	return out, nil
}

// Get returns one discrepancy with its events, oldest first.
func (d Discrepancies) Get(ctx context.Context, tenant uuid.UUID, id int64) (Discrepancy, []Event, error) {
	var disc Discrepancy
	var events []Event
	err := tx(ctx, d.DB, tenant, func(_ pgx.Tx, q *sqlc.Queries) error {
		r, err := q.GetDiscrepancy(ctx, sqlc.GetDiscrepancyParams{TenantID: tenant, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoDiscrepancy
		}
		if err != nil {
			return err
		}
		disc = discrepancyOf(r)
		evs, err := q.ListReconciliationEvents(ctx, sqlc.ListReconciliationEventsParams{TenantID: tenant, DiscrepancyID: id})
		if err != nil {
			return err
		}
		events = make([]Event, len(evs))
		for i, e := range evs {
			events[i] = Event{ID: e.ID, Action: e.Action, Actor: e.Actor, FromStatus: e.FromStatus, ToStatus: e.ToStatus, Resolution: e.Resolution, Note: e.Note, CreatedAt: e.CreatedAt}
		}
		return nil
	})
	return disc, events, err
}

// Change is what an operator does to a discrepancy: Action is "assign", "explain" or "resolve".
type Change struct {
	Action     string
	Owner      string
	Note       string
	Resolution string
	Actor      string
}

// Apply does a change in one transaction with its event, and returns the discrepancy as it is then and whether anything changed. Resolving what is resolved changes
// nothing (the same request twice is one resolution); explaining a resolved one is refused.
func (d Discrepancies) Apply(ctx context.Context, tenant uuid.UUID, id int64, c Change) (Discrepancy, bool, error) {
	var out Discrepancy
	changed := false
	err := tx(ctx, d.DB, tenant, func(pt pgx.Tx, q *sqlc.Queries) error {
		changed = false
		r, err := q.GetDiscrepancyForUpdate(ctx, sqlc.GetDiscrepancyForUpdateParams{TenantID: tenant, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoDiscrepancy
		}
		if err != nil {
			return err
		}
		out = discrepancyOf(r)
		owner := c.Owner
		if owner == "" {
			owner = r.Owner
		}
		to, action := r.Status, ""
		switch c.Action {
		case "assign":
			action = "assigned"
			err = q.AssignDiscrepancy(ctx, sqlc.AssignDiscrepancyParams{TenantID: tenant, ID: id, Owner: c.Owner})
		case "explain":
			if r.Status == DiscResolved {
				return ErrAlreadyResolved
			}
			action, to = "explained", DiscExplained
			err = q.ExplainDiscrepancy(ctx, sqlc.ExplainDiscrepancyParams{TenantID: tenant, ID: id, Note: c.Note, Owner: owner})
		case "resolve":
			if r.Status == DiscResolved {
				return nil
			}
			action, to = "resolved", DiscResolved
			note := c.Note
			if note == "" {
				note = r.Note // what was said when it was explained stays
			}
			err = q.ResolveDiscrepancy(ctx, sqlc.ResolveDiscrepancyParams{TenantID: tenant, ID: id, Resolution: c.Resolution, Note: note, Owner: owner})
		default:
			return errors.New("store: unknown change " + c.Action)
		}
		if err != nil {
			return err
		}
		note := c.Note
		if c.Action == "assign" {
			note = "owner: " + c.Owner
		}
		if err := q.InsertReconciliationEvent(ctx, sqlc.InsertReconciliationEventParams{TenantID: tenant, Environment: r.Environment, DiscrepancyID: id, Action: action, Actor: c.Actor,
			FromStatus: r.Status, ToStatus: to, Resolution: c.Resolution, Note: note}); err != nil {
			return err
		}
		// What an operator does with a difference between the ledger and the invoices is a money decision: the operator (the actor of the request), the kind, what became of
		// it and who owns it are in the log; the note is free text and stays in reconciliation_events.
		if err := audit.Record(ctx, pt, audit.Event{TenantID: tenant, Action: "reconciliation.discrepancy_" + action, TargetType: "discrepancy", TargetID: strconv.FormatInt(id, 10),
			Summary: action + " a " + strings.ReplaceAll(r.Kind, "_", " ") + " discrepancy",
			Detail:  map[string]any{"kind": r.Kind, "environment": r.Environment, "invoice_number": r.InvoiceNumber, "from": r.Status, "to": to, "resolution": c.Resolution, "owner": owner}}); err != nil {
			return err
		}
		changed = true
		after, err := q.GetDiscrepancy(ctx, sqlc.GetDiscrepancyParams{TenantID: tenant, ID: id})
		out = discrepancyOf(after)
		return err
	})
	return out, changed, err
}

// Run is a finished run, as RecordRun keeps it and Runs lists it.
type Run struct {
	Environment  string    `json:"environment"`
	Period       string    `json:"period"`
	StartedBy    string    `json:"started_by"`
	Actor        string    `json:"actor"`
	Result       string    `json:"result"`
	PaidInvoices int64     `json:"paid_invoices"`
	Charges      int64     `json:"charges"`
	Found        int64     `json:"found"`
	Opened       int64     `json:"opened"`
	Reopened     int64     `json:"reopened"`
	Cleared      int64     `json:"cleared"`
	Error        string    `json:"error,omitempty"`
	FinishedAt   time.Time `json:"finished_at"`
}

// RecordRun appends a finished run.
func (d Discrepancies) RecordRun(ctx context.Context, tenant uuid.UUID, r Run) error {
	return at(ctx, d.DB, tenant, func(q *sqlc.Queries) error {
		return q.InsertReconciliationRun(ctx, sqlc.InsertReconciliationRunParams{TenantID: tenant, Environment: r.Environment, Period: r.Period, StartedBy: r.StartedBy, Actor: r.Actor,
			Result: r.Result, PaidInvoices: r.PaidInvoices, Charges: r.Charges, Found: r.Found, Opened: r.Opened, Reopened: r.Reopened, Cleared: r.Cleared, Error: r.Error})
	})
}

// Runs returns a tenant's latest runs, newest first.
func (d Discrepancies) Runs(ctx context.Context, tenant uuid.UUID, limit int) ([]Run, error) {
	rows, err := get(ctx, d.DB, tenant, func(q *sqlc.Queries) ([]sqlc.ListReconciliationRunsRow, error) {
		return q.ListReconciliationRuns(ctx, sqlc.ListReconciliationRunsParams{TenantID: tenant, RowLimit: int64(limit)})
	})
	if err != nil {
		return nil, err
	}
	out := make([]Run, len(rows))
	for i, r := range rows {
		out[i] = Run{Environment: r.Environment, Period: r.Period, StartedBy: r.StartedBy, Actor: r.Actor, Result: r.Result, PaidInvoices: r.PaidInvoices, Charges: r.Charges, Found: r.Found,
			Opened: r.Opened, Reopened: r.Reopened, Cleared: r.Cleared, Error: r.Error, FinishedAt: r.FinishedAt}
	}
	return out, nil
}

// LiveTenants returns the tenants that have taken a live payment through a provider: the pooled database's, and each serving tenant of a cell that has one. The
// schedule visits these. A tenant that is moving is skipped (the next pass finds it).
func (d Discrepancies) LiveTenants(ctx context.Context) ([]uuid.UUID, error) {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	add := func(ids []uuid.UUID) {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	ids, err := get(ctx, d.DB, uuid.Nil, func(q *sqlc.Queries) ([]uuid.UUID, error) { return q.LiveChargeTenants(ctx) })
	if err != nil {
		return nil, err
	}
	add(ids)
	if r, ok := d.DB.(*pgxx.Router); ok {
		err = r.EachReady(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
			ids, err := get(ctx, d.DB, t.TenantID, func(q *sqlc.Queries) ([]uuid.UUID, error) { return q.LiveChargeTenants(ctx) })
			add(ids)
			return err
		})
	}
	return out, err
}

// OpenCounts is how many live discrepancies wait for an operator (status open), by kind, over every place a tenant's rows can be: the pooled database, and each serving
// tenant of a cell. What could be counted is returned with the error of what could not.
func (d Discrepancies) OpenCounts(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	add := func(rows []sqlc.CountOpenLiveDiscrepanciesRow) {
		for _, r := range rows {
			out[r.Kind] += r.N
		}
	}
	rows, err := get(ctx, d.DB, uuid.Nil, func(q *sqlc.Queries) ([]sqlc.CountOpenLiveDiscrepanciesRow, error) {
		return q.CountOpenLiveDiscrepancies(ctx)
	})
	add(rows)
	if err != nil {
		return out, err
	}
	if r, ok := d.DB.(*pgxx.Router); ok {
		err = r.EachReady(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
			rows, err := get(ctx, d.DB, t.TenantID, func(q *sqlc.Queries) ([]sqlc.CountOpenLiveDiscrepanciesRow, error) {
				return q.CountOpenLiveDiscrepancies(ctx)
			})
			add(rows)
			return err
		})
	}
	return out, err
}
