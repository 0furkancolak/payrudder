//go:build integration

package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/stripe"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/reconciliation/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/reconciliation/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/reconciliation/schedule"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
)

// workflow is a tenant whose invoices and ledger disagree in every way the comparison knows, behind the operator API.
type workflow struct {
	t       *testing.T
	w       *world
	tenant  uuid.UUID
	billing fakeBilling
	ledger  store.Ledger
	disc    store.Discrepancies
	deps    command.Deps
	api     *httptest.Server
	inv     map[string]*billingv1.Invoice // a..f, other
	// accounts holds the tenant's provider accounts; registry builds their adapters.
	accounts store.Accounts
	registry *payments.Registry
}

func newWorkflow(t *testing.T, environment string) *workflow {
	t.Helper()
	pg := testkit.StartPostgres(t)
	w := openDB(t, pg)
	f := &workflow{t: t, w: w, tenant: uuid.New(), billing: fakeBilling{invoices: map[string]*billingv1.Invoice{}}, ledger: store.Ledger{DB: w.DB}, disc: store.Discrepancies{DB: w.DB}, inv: map[string]*billingv1.Invoice{}}
	mk := func(name, status string, total int64, period string) {
		inv := &billingv1.Invoice{Id: uuid.NewString(), Number: "INV-" + name, PeriodId: period, Currency: "USD", TotalMinor: total, Status: status}
		f.inv[name], f.billing.invoices[inv.Id] = inv, inv
	}
	mk("a", "paid", 1000, "2026-10") // agrees
	mk("b", "paid", 500, "2026-10")  // no charge: paid_without_charge
	mk("c", "paid", 700, "2026-10")  // charged 650: amount_mismatch
	mk("d", "paid", 900, "2026-10")  // charged twice: duplicate_charge
	mk("e", "open", 400, "2026-10")  // charged, not paid: charge_without_paid
	mk("other", "open", 300, "2026-09")
	charge := func(name, ref string, amount int64) {
		t.Helper()
		if _, err := f.ledger.Append(context.Background(), store.LedgerEntry{TenantID: f.tenant, InvoiceID: uuid.MustParse(f.inv[name].Id), Provider: "paddle", Environment: environment,
			Kind: "charge", Status: "succeeded", AmountMinor: amount, Currency: "USD", ProviderRef: ref, Source: "webhook", OccurredAt: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)}); err != nil {
			t.Fatal(err)
		}
	}
	charge("a", "txn_a", 1000)
	charge("c", "txn_c", 650)
	charge("d", "txn_d1", 900)
	charge("d", "txn_d2", 900)
	charge("e", "txn_e", 400)
	charge("other", "txn_o", 300)

	box, _ := secretbox.New(bytes.Repeat([]byte{1}, 32))
	registry := payments.NewRegistry()
	stripe.Register(registry)
	f.registry, f.accounts = registry, store.Accounts{DB: w.DB, Box: box}
	srv := &webhook.Server{Registry: registry, Resolver: &handler.Resolver{Registry: registry, Accounts: f.accounts}, Ledger: f.ledger, Billing: f.billing,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Token: "internal"}
	f.deps = srv.Reconcile()
	f.api = httptest.NewServer(srv.Handler())
	t.Cleanup(f.api.Close)
	f.deps.Query.Now = func() time.Time { return time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) }
	return f
}

func (f *workflow) run(by string) *command.RunResult {
	f.t.Helper()
	res, err := command.Run(context.Background(), f.deps, command.RunInput{Tenant: f.tenant, Environment: "live", Period: "2026-10", StartedBy: by, Actor: "tester"})
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

func (f *workflow) list(f2 store.Filter) []store.Discrepancy {
	f.t.Helper()
	f2.Limit = 100
	rows, err := f.disc.List(context.Background(), f.tenant, f2)
	if err != nil {
		f.t.Fatal(err)
	}
	return rows
}

func (f *workflow) byKind(kind string) store.Discrepancy {
	f.t.Helper()
	for _, d := range f.list(store.Filter{Kinds: []string{kind}}) {
		return d
	}
	f.t.Fatalf("no %s discrepancy", kind)
	return store.Discrepancy{}
}

func (f *workflow) events(id int64) []string {
	f.t.Helper()
	_, evs, err := f.disc.Get(context.Background(), f.tenant, id)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		out = append(out, e.Action)
	}
	return out
}

func (f *workflow) call(method, path, body string) (int, map[string]any) {
	f.t.Helper()
	req, _ := http.NewRequest(method, f.api.URL+"/admin/tenants/"+f.tenant.String()+path, strings.NewReader(body))
	req.Header.Set("X-Internal-Token", "internal")
	req.Header.Set("X-Payrudder-Actor", "ops@example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func errorKey(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	k, _ := e["key"].(string)
	return k
}

// A run keeps what it finds, once: the same run again changes nothing (no new row, no new event), a payment that is fixed is cleared by the next run, and one that
// was resolved and is found again is reopened, because resolving it did not make it go away.
// wantOpen checks the counts the gauge of the alerts reads.
func (f *workflow) wantOpen(want map[string]int64) {
	f.t.Helper()
	got, err := f.disc.OpenCounts(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if len(got) != len(want) {
		f.t.Fatalf("open discrepancies = %v, want %v", got, want)
	}
	for k, n := range want {
		if got[k] != n {
			f.t.Fatalf("open discrepancies = %v, want %v", got, want)
		}
	}
}

func TestReconciliationRunKeepsEachDiscrepancyOnce(t *testing.T) {
	f := newWorkflow(t, "live")
	first := f.run(command.ByOperator)
	if first.Opened != 4 || first.Reopened != 0 || first.Cleared != 0 || first.Found != 4 {
		t.Fatalf("first run = %+v, want the 4 kinds opened (b, c, d, e)", first)
	}
	again := f.run(command.BySchedule)
	if again.Opened != 0 || again.Reopened != 0 || again.Cleared != 0 || again.Found != 4 {
		t.Fatalf("a second run = %+v, want nothing changed", again)
	}
	// The gauge of the alerts counts the live ones waiting for an operator, by kind: one tenant's four, and not a sandbox tenant's.
	if _, err := f.disc.Sync(context.Background(), uuid.New(), "sandbox", "2026-10", []store.Finding{{Kind: query.PaidWithoutCharge, InvoiceID: uuid.NewString(), Provider: "mockpsp", ProviderRef: "x"}}, false); err != nil {
		t.Fatal(err)
	}
	f.wantOpen(map[string]int64{query.PaidWithoutCharge: 1, query.AmountMismatch: 1, query.DuplicateCharge: 1, query.ChargeWithoutPaid: 1})
	rows := f.list(store.Filter{Period: "2026-10"})
	if len(rows) != 4 {
		t.Fatalf("%d rows kept, want 4", len(rows))
	}
	for _, d := range rows {
		if d.Status != store.DiscOpen || d.Environment != "live" || d.Period != "2026-10" {
			t.Errorf("row = %+v", d)
		}
		if ev := f.events(d.ID); len(ev) != 1 || ev[0] != "detected" {
			t.Errorf("%s: events = %v, want one detected (a repeated run writes none)", d.Kind, ev)
		}
	}
	runs, err := f.disc.Runs(context.Background(), f.tenant, 10)
	if err != nil || len(runs) != 2 || runs[0].StartedBy != "schedule" || runs[1].Opened != 4 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}

	// Fix b at the provider: the payment arrives in the ledger. The next run clears it by itself.
	b := f.byKind(query.PaidWithoutCharge)
	if _, err := f.ledger.Append(context.Background(), store.LedgerEntry{TenantID: f.tenant, InvoiceID: uuid.MustParse(f.inv["b"].Id), Provider: "paddle", Environment: "live", Kind: "charge",
		Status: "succeeded", AmountMinor: 500, Currency: "USD", ProviderRef: "txn_b", Source: "webhook"}); err != nil {
		t.Fatal(err)
	}
	if res := f.run(command.BySchedule); res.Cleared != 1 || res.Found != 3 {
		t.Fatalf("after the fix: %+v, want one cleared", res)
	}
	f.wantOpen(map[string]int64{query.AmountMismatch: 1, query.DuplicateCharge: 1, query.ChargeWithoutPaid: 1}) // a cleared one is not waiting for anybody
	got, _, _ := f.disc.Get(context.Background(), f.tenant, b.ID)
	if got.Status != store.DiscResolved || got.Resolution != store.ResolvedCleared || got.ResolvedAt == nil {
		t.Errorf("cleared row = %+v", got)
	}
	if ev := f.events(b.ID); strings.Join(ev, ",") != "detected,cleared" {
		t.Errorf("events = %v", ev)
	}

	// An operator resolves d ("duplicate") as not an error, but the second charge is still there: the next run finds it again.
	d := f.byKind(query.DuplicateCharge)
	if _, err := command.Resolve(context.Background(), f.deps, f.tenant, d.ID, command.ResolveInput{Resolution: command.NotAnError, Note: "believed fine", Actor: "ops"}); err != nil {
		t.Fatal(err)
	}
	f.wantOpen(map[string]int64{query.AmountMismatch: 1, query.ChargeWithoutPaid: 1}) // resolved
	if res := f.run(command.BySchedule); res.Reopened != 1 || res.Opened != 0 {
		t.Fatalf("after a resolution that did not hold: %+v", res)
	}
	f.wantOpen(map[string]int64{query.AmountMismatch: 1, query.DuplicateCharge: 1, query.ChargeWithoutPaid: 1}) // and open again
	if ev := f.events(d.ID); strings.Join(ev, ",") != "detected,resolved,reopened" {
		t.Errorf("events = %v", ev)
	}
}

// Explaining keeps a discrepancy in the queue with the reason; resolving is idempotent and records who; a resolved one cannot be explained; bulk resolution says what it did
// to each; a ledger fact resolves only a payment billing calls paid that the ledger lacks, writes one row of the invoice's own amount, and the next run agrees.
func TestResolvingDiscrepancies(t *testing.T) {
	ctx := context.Background()
	f := newWorkflow(t, "live")
	f.run(command.ByOperator)

	c := f.byKind(query.AmountMismatch)
	if _, err := command.Explain(ctx, f.deps, f.tenant, c.ID, "", "", "ops"); key(err) != "payments.reconcile.note_required" {
		t.Errorf("explain without a note: %v", err)
	}
	ex, err := command.Explain(ctx, f.deps, f.tenant, c.ID, "provider took a 50 cent fee on this one", "ayse", "ops")
	if err != nil || ex.Status != store.DiscExplained || ex.Owner != "ayse" || ex.Note == "" {
		t.Fatalf("explained = %+v, %v", ex, err)
	}
	if res := f.run(command.BySchedule); res.Opened+res.Reopened+res.Cleared != 0 {
		t.Errorf("an explained discrepancy still found stays explained: %+v", res)
	}
	if got, _, _ := f.disc.Get(ctx, f.tenant, c.ID); got.Status != store.DiscExplained {
		t.Errorf("status = %s", got.Status)
	}

	// Resolving twice is one resolution and one event.
	e := f.byKind(query.ChargeWithoutPaid)
	in := command.ResolveInput{Resolution: command.CorrectedInBilling, Note: "invoice marked paid", Actor: "ops"}
	for i := 0; i < 2; i++ {
		if got, err := command.Resolve(ctx, f.deps, f.tenant, e.ID, in); err != nil || got.Status != store.DiscResolved || got.Resolution != command.CorrectedInBilling {
			t.Fatalf("resolve %d: %+v, %v", i, got, err)
		}
	}
	if ev := f.events(e.ID); strings.Join(ev, ",") != "detected,resolved" {
		t.Errorf("events = %v, want one resolved", ev)
	}
	if _, err := command.Explain(ctx, f.deps, f.tenant, e.ID, "never mind", "", "ops"); key(err) != "payments.reconcile.resolved" {
		t.Errorf("explaining a resolved one: %v", err)
	}
	if _, err := command.Assign(ctx, f.deps, f.tenant, 424242, "x", "ops"); key(err) != "payments.reconcile.not_found" {
		t.Errorf("assigning a stranger's: %v", err)
	}

	// Bulk: d is resolved, e already was, 99999 does not exist.
	d := f.byKind(query.DuplicateCharge)
	out, err := command.ResolveMany(ctx, f.deps, f.tenant, []int64{d.ID, e.ID, 99999, d.ID}, command.ResolveInput{Resolution: command.RefundedAtProvider, Note: "refunded txn_d2", Actor: "ops"})
	if err != nil || len(out) != 3 || out[0].Result != "resolved" || out[1].Result != "unchanged" || out[2].Result != "not_found" {
		t.Fatalf("bulk = %+v, %v", out, err)
	}

	// A ledger fact: only for paid_without_charge, and the amount is the invoice's.
	b := f.byKind(query.PaidWithoutCharge)
	if _, err := command.Resolve(ctx, f.deps, f.tenant, c.ID, command.ResolveInput{Resolution: command.LedgerFact, Note: "n", Actor: "ops", Fact: &command.Fact{Provider: "paddle", ProviderRef: "txn_x"}}); key(err) != "payments.reconcile.bad_ledger_fact" {
		t.Errorf("a ledger fact for a mismatch: %v", err)
	}
	if _, err := command.Resolve(ctx, f.deps, f.tenant, b.ID, command.ResolveInput{Resolution: command.LedgerFact, Note: "n", Actor: "ops"}); key(err) != "payments.reconcile.bad_ledger_fact" {
		t.Errorf("a ledger fact with no reference: %v", err)
	}
	for i := 0; i < 2; i++ {
		got, err := command.Resolve(ctx, f.deps, f.tenant, b.ID, command.ResolveInput{Resolution: command.LedgerFact, Note: "seen in the paddle dashboard", Actor: "ops", Fact: &command.Fact{Provider: "paddle", ProviderRef: "txn_b"}})
		if err != nil || got.Status != store.DiscResolved {
			t.Fatalf("ledger fact %d: %+v, %v", i, got, err)
		}
	}
	rows, err := f.ledger.List(ctx, f.tenant, 100)
	if err != nil {
		t.Fatal(err)
	}
	var facts int
	for _, r := range rows {
		if r.Source == store.SourceReconciliation {
			facts++
			if r.AmountMinor != 500 || r.Currency != "USD" || r.Provider != "paddle" || r.Kind != "charge" || r.Status != "succeeded" || *r.ProviderRef != "txn_b" || r.Environment != "live" ||
				r.InvoiceID == nil || *r.InvoiceID != f.inv["b"].Id || !strings.Contains(*r.Detail, "ops") {
				t.Errorf("ledger fact = %+v", r)
			}
		}
	}
	if facts != 1 {
		t.Errorf("%d ledger facts written, want exactly 1 (a fact twice is one row)", facts)
	}
	if res := f.run(command.BySchedule); res.Reopened != 2 || res.Found != 3 {
		// d is still a duplicate in the ledger (resolved as refunded at the provider, which a run cannot see) and e is still charged on an open invoice (the fake billing
		// was not changed), so both are found again; b, which the ledger fact fixed, is not.
		t.Errorf("run after the resolutions: %+v, want the duplicate and the unpaid charge reopened and b left resolved", res)
	}
	if got, _, _ := f.disc.Get(ctx, f.tenant, b.ID); got.Status != store.DiscResolved {
		t.Errorf("the discrepancy the ledger fact resolved is %s after the next run, want resolved", got.Status)
	}

	// Plan 22 G4: what the operator decided is in the audit log, once for each change (resolving twice is one entry), with the kind and the owner and without the note.
	for action, want := range map[string]int{"reconciliation.discrepancy_explained": 1, "reconciliation.discrepancy_resolved": 3} {
		var n int
		if err := f.w.At(f.tenant).QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id = $1 AND action = $2`, f.tenant, action).Scan(&n); err != nil || n != want {
			t.Errorf("%s: %d entries, %v; want %d", action, n, err, want)
		}
	}
	var target, detail string
	if err := f.w.At(f.tenant).QueryRow(ctx, `SELECT target_id, detail::text FROM audit_events WHERE tenant_id = $1 AND action = 'reconciliation.discrepancy_explained'`, f.tenant).Scan(&target, &detail); err != nil {
		t.Fatal(err)
	}
	if target != strconv.FormatInt(c.ID, 10) || !strings.Contains(detail, `"kind": "amount_mismatch"`) || !strings.Contains(detail, `"owner": "ayse"`) || strings.Contains(detail, "50 cent") {
		t.Errorf("discrepancy_explained = target %s, detail %s", target, detail)
	}
}

// The API: the queue is filtered and paged on the server, a bad filter or id is a keyed 400, an explanation needs a note, the actor comes from the gateway's header.
func TestReconciliationQueueAPI(t *testing.T) {
	f := newWorkflow(t, "live")
	if code, m := f.call("POST", "/reconciliation/runs?environment=live", `{"period":"2026-10"}`); code != 200 || m["opened"] != float64(4) {
		t.Fatalf("run: %d %v", code, m)
	}
	if code, m := f.call("POST", "/reconciliation/runs", `{"period":"2026-10"}`); code != 400 || errorKey(m) != "payments.request.bad_environment" {
		t.Errorf("run without an environment: %d %v", code, m)
	}
	if code, m := f.call("POST", "/reconciliation/runs?environment=live", `{"period":"2026-13"}`); code != 400 || errorKey(m) != "payments.reconcile.bad_period" {
		t.Errorf("run with a bad period: %d %v", code, m)
	}

	code, m := f.call("GET", "/reconciliation/discrepancies?status=open&kind=duplicate_charge,amount_mismatch", "")
	rows, _ := m["discrepancies"].([]any)
	if code != 200 || len(rows) != 2 {
		t.Fatalf("filtered list: %d %v", code, m)
	}
	if counts, _ := m["counts"].([]any); len(counts) != 4 {
		t.Errorf("counts = %v, want a count per status and kind (not narrowed by the filter)", m["counts"])
	}
	if _, ok := m["last_run"].(map[string]any); !ok {
		t.Errorf("last_run = %v", m["last_run"])
	}
	code, m = f.call("GET", "/reconciliation/discrepancies?limit=3", "")
	rows, _ = m["discrepancies"].([]any)
	next, _ := m["next_page_token"].(string)
	if code != 200 || len(rows) != 3 || next == "" {
		t.Fatalf("first page: %d %d rows, token %q", code, len(rows), next)
	}
	if _, m2 := f.call("GET", "/reconciliation/discrepancies?limit=3&page_token="+next, ""); len(m2["discrepancies"].([]any)) != 1 || m2["next_page_token"] != "" {
		t.Errorf("second page = %v", m2)
	}
	for _, q := range []string{"status=done", "kind=nope", "limit=0", "limit=900", "page_token=abc", "period=2026-1"} {
		if code, m := f.call("GET", "/reconciliation/discrepancies?"+q, ""); code != 400 || errorKey(m) != "payments.reconcile.bad_filter" {
			t.Errorf("%s: %d %v", q, code, m)
		}
	}

	id := int64(rows[0].(map[string]any)["id"].(float64))
	idPath := "/reconciliation/discrepancies/" + itoa(id)
	if code, m := f.call("GET", idPath, ""); code != 200 || len(m["events"].([]any)) != 1 {
		t.Errorf("one: %d %v", code, m)
	}
	if code, m := f.call("POST", idPath+"/explain", `{"note":""}`); code != 400 || errorKey(m) != "payments.reconcile.note_required" {
		t.Errorf("explain without a note: %d %v", code, m)
	}
	if code, m := f.call("POST", idPath+"/assign", `{"owner":"ayse"}`); code != 200 || m["discrepancy"].(map[string]any)["owner"] != "ayse" {
		t.Errorf("assign: %d %v", code, m)
	}
	if code, m := f.call("POST", idPath+"/resolve", `{"resolution":"other","note":"checked"}`); code != 200 || m["discrepancy"].(map[string]any)["status"] != "resolved" {
		t.Errorf("resolve: %d %v", code, m)
	}
	if _, m := f.call("GET", idPath, ""); m["events"].([]any)[2].(map[string]any)["actor"] != "ops@example.com" {
		t.Errorf("the actor is the gateway's header: %v", m["events"])
	}
	if code, m := f.call("GET", "/reconciliation/discrepancies/777777", ""); code != 404 || errorKey(m) != "payments.reconcile.not_found" {
		t.Errorf("missing: %d %v", code, m)
	}
	if code, m := f.call("GET", "/reconciliation/discrepancies/x", ""); code != 404 || errorKey(m) != "payments.reconcile.not_found" {
		t.Errorf("bad id: %d %v", code, m)
	}
	code, m = f.call("POST", "/reconciliation/discrepancies/resolve", `{"ids":[`+itoa(id)+`,424242],"resolution":"other","note":"bulk"}`)
	if out, _ := m["outcomes"].([]any); code != 200 || len(out) != 2 || out[0].(map[string]any)["result"] != "unchanged" || out[1].(map[string]any)["result"] != "not_found" {
		t.Errorf("bulk: %d %v", code, m)
	}
	// The comparison alone, nothing kept, still answers as it did.
	if code, m := f.call("GET", "/reconciliation?period=2026-10", ""); code != 200 || m["paid_invoices"] != float64(4) {
		t.Errorf("get: %d %v", code, m)
	}
}

func key(err error) string {
	var r *refuse.Error
	if errors.As(err, &r) {
		return r.Key
	}
	return ""
}

// The schedule visits the tenants that took a live payment, for the current and the previous period, and keeps what it finds; a second pass changes nothing.
func TestScheduledReconciliationVisitsLiveTenants(t *testing.T) {
	f := newWorkflow(t, "live")
	// A sandbox tenant with a payment is not visited.
	sandbox := uuid.New()
	if _, err := f.ledger.Append(context.Background(), store.LedgerEntry{TenantID: sandbox, InvoiceID: uuid.New(), Provider: "mockpsp", Environment: "sandbox", Kind: "charge", Status: "succeeded",
		AmountMinor: 5, Currency: "USD", ProviderRef: "ch_s", Source: "command"}); err != nil {
		t.Fatal(err)
	}
	job := &schedule.Job{Deps: f.deps, Interval: time.Hour, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) }}
	job.Pass(context.Background())
	job.Pass(context.Background())
	// The gauge ReconciliationRunStalled reads: when the last pass finished (the job's clock here).
	if v, ok := gauged(t, "reconciliation_last_pass_timestamp_seconds"); !ok || v != time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("reconciliation_last_pass_timestamp_seconds = %d (present %v), want the end of the pass", v, ok)
	}
	kinds := map[string]int{}
	for _, d := range f.list(store.Filter{}) {
		kinds[d.Period+" "+d.Kind]++
	}
	want := map[string]int{"2026-10 " + query.PaidWithoutCharge: 1, "2026-10 " + query.AmountMismatch: 1, "2026-10 " + query.DuplicateCharge: 1, "2026-10 " + query.ChargeWithoutPaid: 1,
		"2026-09 " + query.ChargeWithoutPaid: 1}
	if len(kinds) != len(want) {
		t.Fatalf("kept = %v, want %v", kinds, want)
	}
	for k, n := range want {
		if kinds[k] != n {
			t.Errorf("%s = %d, want %d", k, kinds[k], n)
		}
	}
	runs, _ := f.disc.Runs(context.Background(), f.tenant, 10)
	if len(runs) != 4 || runs[0].StartedBy != "schedule" || runs[0].Actor != "system" {
		t.Errorf("runs = %+v, want 2 periods x 2 passes by the schedule", runs)
	}
	if rows, _ := f.disc.List(context.Background(), sandbox, store.Filter{Limit: 10}); len(rows) != 0 {
		t.Errorf("the sandbox tenant was visited: %+v", rows)
	}
}

// What is kept is protected like the ledger: events and runs cannot be changed or deleted, a sandbox tenant's rows go with its reset, and a live tenant's make the reset refuse.
func TestKeptDiscrepanciesAreAppendOnlyAndResetWithTheSandbox(t *testing.T) {
	ctx := context.Background()
	f := newWorkflow(t, "live")
	f.run(command.ByOperator)
	at := f.w.At(f.tenant)
	for _, stmt := range []string{`UPDATE reconciliation_events SET note = 'x'`, `UPDATE reconciliation_runs SET result = 'ok'`, `UPDATE reconciliation_discrepancies SET kind = 'x'`,
		`UPDATE reconciliation_discrepancies SET tenant_id = tenant_id`, `UPDATE settlement_lines SET kind = 'payout'`} {
		if _, err := at.Exec(ctx, stmt); err == nil || !strings.Contains(err.Error(), "42501") {
			t.Errorf("%s: %v, want permission denied", stmt, err)
		}
	}
	for _, table := range []string{"reconciliation_events", "reconciliation_runs"} {
		if _, err := at.Exec(ctx, `DELETE FROM `+table+` WHERE tenant_id = $1`, f.tenant); err == nil || !strings.Contains(err.Error(), "never deleted") {
			t.Errorf("delete from %s: %v, want refused", table, err)
		}
	}
	if _, err := f.w.At(f.tenant).Exec(ctx, `UPDATE reconciliation_discrepancies SET status = 'resolved', resolution = 'other' WHERE tenant_id = $1`, f.tenant); err != nil {
		t.Errorf("the columns that change are granted: %v", err)
	}

	// The live tenant's rows make the reset refuse it, whatever the request says.
	reset := func(tenant uuid.UUID) error {
		_, err := sandboxreset.New(f.w.DB).Reset(ctx, tenant)
		return err
	}
	if err := reset(f.tenant); err == nil || !strings.Contains(err.Error(), "admin.sandbox.live_refused") {
		t.Errorf("reset of a live tenant: %v", err)
	}

	// A sandbox tenant's discrepancies, events, runs and settlement lines go; its fee rules stay.
	sb := uuid.New()
	inv := &billingv1.Invoice{Id: uuid.NewString(), Number: "INV-S", PeriodId: "2026-10", Currency: "USD", TotalMinor: 100, Status: "paid"}
	f.billing.invoices[inv.Id] = inv
	if _, err := command.Run(ctx, f.deps, command.RunInput{Tenant: sb, Environment: "sandbox", Period: "2026-10", StartedBy: command.ByOperator, Actor: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.deps.Settlements.Put(ctx, sb, "sandbox", []store.SettlementLine{{Provider: "stripe", LineRef: "l1", Kind: "payment", PaymentRef: "pi_1", Currency: "USD", GrossMinor: 1, NetMinor: 1, OccurredAt: time.Now(), Source: "csv"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.deps.Settlements.PutFeeRule(ctx, sb, store.FeeRule{Environment: "sandbox", Provider: "stripe", Currency: "USD", RateBps: 290}); err != nil {
		t.Fatal(err)
	}
	if rows, _ := f.disc.List(ctx, sb, store.Filter{Limit: 10}); len(rows) == 0 { // the fake billing shows every tenant the same invoices
		t.Fatalf("sandbox discrepancies = %+v", rows)
	}
	if err := reset(sb); err != nil {
		t.Fatalf("reset of a sandbox tenant: %v", err)
	}
	if rows, _ := f.disc.List(ctx, sb, store.Filter{Limit: 10}); len(rows) != 0 {
		t.Errorf("discrepancies survived the reset: %+v", rows)
	}
	if runs, _ := f.disc.Runs(ctx, sb, 10); len(runs) != 0 {
		t.Errorf("runs survived the reset: %+v", runs)
	}
	if lines, _ := f.deps.Settlements.Since(ctx, sb, time.Time{}); len(lines) != 0 {
		t.Errorf("settlement lines survived the reset: %+v", lines)
	}
	if rules, _ := f.deps.Settlements.FeeRules(ctx, sb); len(rules) != 1 {
		t.Errorf("fee rules = %+v, want kept (they are configuration)", rules)
	}
	if rows, _ := f.disc.List(ctx, f.tenant, store.Filter{Limit: 10}); len(rows) != 4 {
		t.Errorf("another tenant's rows were touched: %d", len(rows))
	}
}

const settlementCSV = `reference,payment_ref,currency,gross,fee,occurred_on,settled_on,batch
l1,txn_a,USD,10.00,0.59,2026-10-03,2026-10-12,batch-1
l2,txn_c,USD,6.40,0.30,2026-10-03,,
l3,txn_unrelated,USD,1.00,0,2026-10-15,,
`

// "Paid" is not "settled": a report that is imported tells them apart in the API and raises what the lines cannot support, and the same report twice changes nothing.
func TestSettlementReportMakesPaidAndSettledDifferentStates(t *testing.T) {
	f := newWorkflow(t, "live")
	post := func(path, body string) (int, map[string]any) { return f.call("POST", path, body) }

	if code, m := post("/settlement/import?environment=live&provider=paddle", "reference,currency,gross,occurred_on\nr,USD,x,2026-10-01\n"); code != 400 || errorKey(m) != "payments.settlement.bad_csv" {
		t.Fatalf("a bad report: %d %v", code, m)
	}
	if code, m := post("/settlement/import?environment=live&provider=paddle", settlementCSV); code != 200 || m["lines"] != float64(3) || m["changed"] != float64(3) {
		t.Fatalf("import: %d %v", code, m)
	}
	if code, m := post("/settlement/import?environment=live&provider=paddle", settlementCSV); code != 200 || m["changed"] != float64(0) {
		t.Fatalf("the same report again: %d %v", code, m)
	}
	if code, m := f.call("PUT", "/settlement/fee-rules", `{"environment":"live","provider":"paddle","currency":"usd","rate_bps":290,"fixed_minor":30}`); code != 200 {
		t.Fatalf("fee rule: %d %v", code, m)
	}
	if code, m := f.call("PUT", "/settlement/fee-rules", `{"environment":"live","provider":"paddle","currency":"USD","rate_bps":20000}`); code != 400 || errorKey(m) != "payments.settlement.bad_fee_rule" {
		t.Errorf("a rate above 100 percent: %d %v", code, m)
	}
	if code, m := f.call("GET", "/settlement/fee-rules", ""); code != 200 || len(m["rules"].([]any)) != 1 {
		t.Errorf("fee rules: %d %v", code, m)
	}

	code, m := f.call("GET", "/settlement/payments?period=2026-10", "")
	if code != 200 {
		t.Fatalf("payments: %d %v", code, m)
	}
	states := map[string]map[string]any{}
	for _, p := range m["payments"].([]any) {
		p := p.(map[string]any)
		states[p["provider_ref"].(string)] = p
	}
	if p := states["txn_a"]; p["state"] != "settled" || p["settlement_batch"] != "batch-1" || p["fee_minor"] != float64(59) || p["expected_fee_minor"] != float64(59) || p["settled_at"] == nil {
		t.Errorf("txn_a = %v", p)
	}
	if p := states["txn_c"]; p["state"] != "pending_payout" || p["settled_at"] != nil {
		t.Errorf("txn_c = %v", p)
	}
	for _, ref := range []string{"txn_d1", "txn_d2"} {
		if states[ref]["state"] != "not_reported" {
			t.Errorf("%s = %v", ref, states[ref])
		}
	}

	f.run(command.ByOperator)
	got := map[string]int{}
	for _, d := range f.list(store.Filter{Kinds: []string{query.SettlementMissing, query.SettlementMismatch, query.FeeMismatch}}) {
		got[d.Kind+" "+d.ProviderRef]++
		if d.Kind == query.FeeMismatch && (d.LedgerMinor != 49 || d.SettledMinor != 30) {
			t.Errorf("fee mismatch = %+v, want expected 49 and kept 30", d)
		}
		if d.Kind == query.SettlementMismatch && (d.LedgerMinor != 650 || d.SettledMinor != 640) {
			t.Errorf("settlement mismatch = %+v", d)
		}
	}
	want := map[string]int{query.SettlementMissing + " txn_d1": 1, query.SettlementMissing + " txn_d2": 1, query.SettlementMismatch + " txn_c": 1, query.FeeMismatch + " txn_c": 1}
	if len(got) != len(want) {
		t.Fatalf("settlement discrepancies = %v, want %v", got, want)
	}
	for k := range want {
		if got[k] != 1 {
			t.Errorf("%s: %d", k, got[k])
		}
	}
	// The line for d1 arrives: the next run clears that discrepancy.
	late := strings.Replace(settlementCSV, "l3,txn_unrelated", "l4,txn_d1,USD,9.00,0.50,2026-10-03,2026-10-13,batch-1\nl3,txn_unrelated", 1)
	if code, m := post("/settlement/import?environment=live&provider=paddle", late); code != 200 || m["changed"] != float64(1) {
		t.Fatalf("late report: %d %v", code, m)
	}
	if res := f.run(command.ByOperator); res.Cleared != 1 {
		t.Errorf("run after the line arrived: %+v, want settlement_missing for txn_d1 cleared", res)
	}
}

// The adapter of a provider that documents its settlement is read through the account: Stripe (against a fake server) fills in what a payout carried. A provider whose
// adapter cannot is told so, and so is a tenant without the account.
func TestSettlementIsReadFromTheProvidersOwnAPI(t *testing.T) {
	ctx := context.Background()
	f := newWorkflow(t, "live")
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		txn := func(id string, amount, fee int64) map[string]any {
			return map[string]any{"id": id, "reporting_category": "charge", "amount": amount, "fee": fee, "net": amount - fee, "currency": "usd", "created": 1_760_000_000,
				"available_on": 1_760_200_000, "source": map[string]any{"id": "ch_" + id, "payment_intent": "pi_" + id}}
		}
		var body map[string]any
		switch {
		case r.URL.Path == "/v1/payouts":
			body = map[string]any{"data": []any{map[string]any{"id": "po_1", "amount": 941, "currency": "usd", "created": 1_760_100_000, "arrival_date": 1_760_300_000, "reconciliation_status": "completed"}}}
		case r.URL.Query().Get("payout") == "po_1":
			body = map[string]any{"data": []any{txn("t1", 1000, 59)}}
		default:
			body = map[string]any{"data": []any{txn("t1", 1000, 59), txn("t2", 500, 30)}}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(fake.Close)
	if err := f.accounts.Put(ctx, f.tenant, "stripe", "sandbox", []byte(`{"api_key":"sk_test_abc","webhook_secret":"whsec_x","base_url":"`+fake.URL+`"}`), false); err != nil {
		t.Fatal(err)
	}
	f.registry.Register("plain", func([]byte) (payments.Provider, error) { return payments.NewMockPSP("http://127.0.0.1:1", 0), nil })
	if err := f.accounts.Put(ctx, f.tenant, "plain", "sandbox", []byte(`{}`), false); err != nil {
		t.Fatal(err)
	}

	if code, m := f.call("POST", "/settlement/sync?environment=sandbox", `{"provider":"stripe"}`); code != 200 || m["lines"] != float64(3) || m["changed"] != float64(3) {
		t.Fatalf("sync: %d %v", code, m)
	}
	lines, err := f.deps.Settlements.Since(ctx, f.tenant, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]store.SettlementLine{}
	for _, l := range lines {
		by[l.LineRef] = l
	}
	if l := by["t1"]; l.Provider != "stripe" || l.PaymentRef != "pi_t1" || l.Batch != "po_1" || l.SettledAt == nil || l.FeeMinor != 59 || l.Source != "stripe" {
		t.Errorf("t1 = %+v, want settled in po_1", l)
	}
	if l := by["t2"]; l.SettledAt != nil || l.Batch != "" {
		t.Errorf("t2 = %+v, want unsettled", l)
	}
	if code, m := f.call("POST", "/settlement/sync?environment=sandbox", `{"provider":"stripe"}`); code != 200 || m["changed"] != float64(0) {
		t.Errorf("sync again: %d %v", code, m)
	}
	if code, m := f.call("POST", "/settlement/sync?environment=sandbox", `{"provider":"plain"}`); code != 501 || errorKey(m) != "payments.provider.not_supported" {
		t.Errorf("a provider with no settlement API: %d %v", code, m)
	}
	if code, m := f.call("POST", "/settlement/sync?environment=sandbox", `{"provider":"paddle"}`); code != 404 || errorKey(m) != "payments.account.not_found" {
		t.Errorf("no account: %d %v", code, m)
	}
	if code, m := f.call("POST", "/settlement/sync?environment=sandbox", `{"provider":"stripe","since":"2999-01-01T00:00:00Z"}`); code != 400 || errorKey(m) != "payments.settlement.bad_since" {
		t.Errorf("a time in the future: %d %v", code, m)
	}
}
