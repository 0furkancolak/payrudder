//go:build integration

package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/iyzico"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	paymentsreset "github.com/0furkancolak/payrudder/services/payments-worker/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
	"github.com/0furkancolak/payrudder/services/payments-worker/migrations"
)

// The tenant migrations build exactly the schema the public migrations build.
func TestTenantSchemaMatchesPublic(t *testing.T) {
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	pub := testkit.MigratedDB(t, pg, "payments", migrations.FS)
	cell := testkit.CreateCellDB(t, pg, "payments", "c1")
	id := uuid.New()
	if _, err := pgxx.ProvisionTenant(ctx, cell.Migrator, tenantSchemas(), id, nil); err != nil {
		t.Fatal(err)
	}
	pubConn, err := pgx.Connect(ctx, pub.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pubConn.Close(ctx) }()
	cellConn, err := pgx.Connect(ctx, cell.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cellConn.Close(ctx) }()
	want, err := pgxx.SchemaShape(ctx, pubConn, "public")
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgxx.SchemaShape(ctx, cellConn, tenantdir.Schema(id))
	if err != nil {
		t.Fatal(err)
	}
	if len(want) < 60 {
		t.Fatalf("the public shape has only %d lines: the comparison is not looking at the schema", len(want))
	}
	// The one difference there is meant to be: a cell has the trigger that feeds the index of charge keys (migration 00002 of the tenant set); the pooled table
	// is its own index. Everything else must be equal.
	var diff []string
	var indexTrigger int
	for _, l := range pgxx.Diff(want, got) {
		if strings.HasPrefix(l, "+ trigger payment_attempts payment_attempts_index_charge_key ") {
			indexTrigger++
			continue
		}
		diff = append(diff, l)
	}
	if indexTrigger != 1 {
		t.Errorf("the tenant schema has %d index triggers on payment_attempts, want 1", indexTrigger)
	}
	if len(diff) > 0 {
		for _, l := range diff {
			t.Error(l)
		}
		t.Fatal("the tenant migrations and the public migrations of payments-worker have drifted apart (- public only, + tenant only)")
	}
}

// places is a router over two cells and the pooled database, with tenants in each: a1 and a2 share the cell c1, b is alone in c2, p is pooled.
type places struct {
	*world
	a1, a2, b, p uuid.UUID
	all          []uuid.UUID
}

func openPlaces(t *testing.T) *places {
	t.Helper()
	pg := testkit.StartPostgres(t)
	w := openWorld(t, pg, true, "c1", "c2")
	pl := &places{world: w, a1: uuid.New(), a2: uuid.New(), b: uuid.New(), p: uuid.New()}
	w.Dir.assign(pl.b, "c2")
	w.Dir.assign(pl.p, "")
	pl.all = []uuid.UUID{pl.a1, pl.a2, pl.b, pl.p}
	return pl
}

func newBox(t *testing.T) *secretbox.Box {
	t.Helper()
	box, err := secretbox.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func sandboxChargeOf(tenant, invoice, command uuid.UUID) *commandsv1.CommandEnvelope {
	return &commandsv1.CommandEnvelope{CommandId: command.String(), SagaId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_SANDBOX,
		Command: &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{InvoiceId: invoice.String(), AmountMinor: 500, Currency: "USD",
			PaymentMethod: "pm_x", IdempotencyKey: invoice.String() + ":charge:0"}}}
}

// tenantTables are the tables whose rows carry a tenant_id.
var tenantTables = []string{"payment_attempts", "provider_accounts", "payment_ledger", "provider_events", "provider_subscriptions", "provider_objects", "manual_receipts",
	"einvoice_accounts", "einvoice_issuances", "provider_account_events"}

// noForeignRows fails when any tenant schema of any cell holds a row of another tenant, or when the pooled database holds a row of a tenant that lives in a cell.
func (pl *places) noForeignRows(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for name, pool := range pl.pools {
		ids, err := pgxx.TenantSchemaIDs(ctx, pool)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			for _, table := range tenantTables {
				var n int
				if err := pgxx.InTenantSchemaTx(ctx, pool, id, func(tx pgx.Tx) error {
					return tx.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id <> $1`, id).Scan(&n)
				}); err != nil {
					t.Fatal(err)
				}
				if n != 0 {
					t.Errorf("cell %s, schema of %s: %d rows of %s belong to another tenant", name, id, n, table)
				}
			}
		}
	}
	for _, id := range []uuid.UUID{pl.a1, pl.a2, pl.b} {
		for _, table := range tenantTables {
			var n int
			if err := pl.Pooled.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("the pooled database holds %d rows of %s of tenant %s, which lives in a cell", n, table, id)
			}
		}
	}
}

// Every store reads and writes the place of the tenant it is asked about, and nothing else: two tenants of one cell, one of another and a pooled one write the
// same keys, references and event ids and none of them sees or disturbs another's. In a cell the schema is the only boundary (no row-level security), so this is
// what keeps it.
func TestStoresStayInTheirTenantsPlace(t *testing.T) {
	pl := openPlaces(t)
	ctx := context.Background()
	box := newBox(t)
	accounts, objects, subs := store.Accounts{DB: pl.DB, Box: box}, store.Objects{DB: pl.DB}, store.Subscriptions{DB: pl.DB}
	ledger, attempts, events, transfers := store.Ledger{DB: pl.DB}, store.Attempts{DB: pl.DB}, store.Events{DB: pl.DB}, store.Transfers{DB: pl.DB}
	invoice := uuid.New() // the same invoice id and references in every tenant: nothing may collide or leak
	now := time.Now().UTC().Truncate(time.Second)

	for i, tenant := range pl.all {
		secret := fmt.Sprintf(`{"api_key":"k%d"}`, i)
		if err := accounts.Put(ctx, tenant, "iyzico", "live", []byte(secret), true); err != nil {
			t.Fatal(err)
		}
		if err := objects.Put(ctx, tenant, "paddle", "live", "price", "plan:pro", fmt.Sprintf("pri_%d", i)); err != nil {
			t.Fatal(err)
		}
		if err := subs.Upsert(ctx, tenant, "paddle", "live", "sub_1", nil, fmt.Sprintf("status-%d", i), nil, now); err != nil {
			t.Fatal(err)
		}
		if _, err := attempts.Start(ctx, uuid.New(), tenant, invoice, "charge", "live", "iyzico", invoice.String()+":charge:0", map[string]any{"amount_minor": 100 + i, "currency": "TRY"}); err != nil {
			t.Fatal(err)
		}
		if fresh, err := ledger.Append(ctx, store.LedgerEntry{TenantID: tenant, InvoiceID: invoice, Provider: "iyzico", Environment: "live", Kind: "charge", Status: "succeeded",
			AmountMinor: int64(100 + i), Currency: "TRY", ProviderRef: "P1", IdempotencyKey: invoice.String() + ":charge:0", Source: "command"}); err != nil || !fresh {
			t.Fatalf("tenant %d: the same fact P1 in another tenant is a new fact: fresh = %v, %v", i, fresh, err)
		}
		if fresh, err := events.Receive(ctx, tenant, "paddle", "evt_1", []byte(`{}`)); err != nil || !fresh {
			t.Fatalf("tenant %d: event evt_1 is new in every tenant's inbox: %v, %v", i, fresh, err)
		}
		if fresh, err := events.Receive(ctx, tenant, "paddle", "evt_1", []byte(`{}`)); err != nil || fresh {
			t.Fatalf("tenant %d: a repeated event is a duplicate: %v, %v", i, fresh, err)
		}
		rec, err := transfers.Record(ctx, store.NewReceipt{TenantID: tenant, InvoiceID: invoice, Provider: "banktransfer", Environment: "live", AmountMinor: 40,
			Currency: "TRY", BankReference: "REF-1", ReceivedAt: now, RecordedBy: "operator"}, 100)
		if err != nil || rec.Duplicate || rec.Received != 40 {
			t.Fatalf("tenant %d: receipt = %+v, %v; the same reference of another tenant's invoice is another receipt", i, rec, err)
		}
	}

	for i, tenant := range pl.all {
		list, err := ledger.List(ctx, tenant, 10)
		if err != nil || len(list) != 2 {
			t.Fatalf("tenant %d: ledger = %+v, %v; want its two rows (the charge and the receipt)", i, list, err)
		}
		for _, r := range list {
			if *r.InvoiceID != invoice.String() || (r.Source == "command" && r.AmountMinor != int64(100+i)) || (r.Source == "manual" && r.AmountMinor != 40) {
				t.Errorf("tenant %d: ledger row %+v is not its own", i, r)
			}
		}
		if raw, _, err := accounts.Settings(ctx, tenant, "iyzico", "live"); err != nil || string(raw) != fmt.Sprintf(`{"api_key":"k%d"}`, i) {
			t.Errorf("tenant %d: settings = %q, %v", i, raw, err)
		}
		if ref, ok, err := objects.Get(ctx, tenant, "paddle", "live", "price", "plan:pro"); err != nil || !ok || ref != fmt.Sprintf("pri_%d", i) {
			t.Errorf("tenant %d: price = %q %v %v", i, ref, ok, err)
		}
		if l, err := subs.List(ctx, tenant); err != nil || len(l) != 1 || l[0].Status != fmt.Sprintf("status-%d", i) {
			t.Errorf("tenant %d: subscriptions = %+v, %v", i, l, err)
		}
		if got, err := transfers.Received(ctx, tenant, invoice, "banktransfer"); err != nil || got.Minor != 40 {
			t.Errorf("tenant %d: received = %+v, %v; want 40 (its own receipt, not the sum of all)", i, got, err)
		}
		if a, ok, err := attempts.ChargeOf(ctx, tenant, invoice.String()+":charge:0"); err != nil || !ok || a.TenantID != tenant || a.AmountMinor != int64(100+i) {
			t.Errorf("tenant %d: charge by key = %+v %v %v", i, a, ok, err)
		}
	}

	// What one tenant has, another asking for it by name does not get.
	if err := accounts.Put(ctx, pl.a1, "paddle", "live", []byte(`{"only":"a1"}`), false); err != nil {
		t.Fatal(err)
	}
	for _, other := range []uuid.UUID{pl.a2, pl.b, pl.p} {
		if _, _, err := accounts.Settings(ctx, other, "paddle", "live"); !errors.Is(err, store.ErrNoAccount) {
			t.Errorf("tenant %s read the account of a1: %v", other, err)
		}
		if l, err := accounts.List(ctx, other); err != nil || len(l) != 1 {
			t.Errorf("tenant %s lists %d accounts (%v); want its own one", other, len(l), err)
		}
	}
	only := uuid.New()
	if _, err := ledger.Append(ctx, store.LedgerEntry{TenantID: pl.a1, InvoiceID: only, Provider: "iyzico", Environment: "live", Kind: "charge", Status: "succeeded", AmountMinor: 7,
		Currency: "TRY", ProviderRef: "ONLY-A1", Source: "command"}); err != nil {
		t.Fatal(err)
	}
	for _, other := range []uuid.UUID{pl.a2, pl.b, pl.p} {
		if l, err := ledger.LookupInvoices(ctx, other, []uuid.UUID{only}); err != nil || len(l) != 0 {
			t.Errorf("tenant %s sees the ledger rows of a1's invoice: %+v, %v", other, l, err)
		}
		if n, err := ledger.Outcomes(ctx, other, "iyzico", "charge", "ONLY-A1"); err != nil || n != 0 {
			t.Errorf("tenant %s counts outcomes of a1's reference: %d, %v", other, n, err)
		}
	}
	pl.noForeignRows(t)

	// Inside a tenant's transaction the search path is its schema and nothing else answers an unqualified table name.
	for _, tenant := range []uuid.UUID{pl.a1, pl.a2, pl.b} {
		err := store.Tx(ctx, pl.DB, tenant, func(tx pgx.Tx, q *sqlc.Queries, schema string) error {
			if schema != tenantdir.Schema(tenant) {
				return fmt.Errorf("schema = %q", schema)
			}
			var path string
			if err := tx.QueryRow(ctx, `SHOW search_path`).Scan(&path); err != nil || path != pgxx.SearchPath(schema) {
				return fmt.Errorf("search_path = %q, %v", path, err)
			}
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM payment_ledger WHERE tenant_id <> $1`, tenant).Scan(&n); err != nil || n != 0 {
				return fmt.Errorf("%d ledger rows of other tenants are visible, %v", n, err)
			}
			return nil
		})
		if err != nil {
			t.Errorf("tenant %s: %v", tenant, err)
		}
	}
	// The cell's own public schema holds nothing: a connection that is not scoped to a tenant sees no table at all.
	for name, pool := range pl.pools {
		if _, err := pool.Exec(ctx, `SELECT 1 FROM payment_ledger`); pgxx.ErrCode(err) != "42P01" {
			t.Errorf("cell %s: an unscoped statement found the ledger: %v", name, err)
		}
	}
}

// Statements that name no tenant cannot be answered in a cell, and the stores refuse them instead of answering from the pooled database.
func TestStoresRefuseWhatNamesNoTenant(t *testing.T) {
	pl := openPlaces(t)
	ctx := context.Background()
	attempts, events := store.Attempts{DB: pl.DB}, store.Events{DB: pl.DB}
	if _, _, err := attempts.Lookup(ctx, uuid.New()); !errors.Is(err, store.ErrNoTenant) {
		t.Errorf("Lookup without a tenant: %v", err)
	}
	if err := attempts.Store(ctx, uuid.New(), &commandsv1.ReplyEnvelope{}); !errors.Is(err, store.ErrNoTenant) {
		t.Errorf("Store without a tenant: %v", err)
	}
	if _, _, err := attempts.ChargeOf(ctx, uuid.Nil, "k"); !errors.Is(err, store.ErrNoTenant) {
		t.Errorf("ChargeOf without a tenant: %v", err)
	}
	if _, ok, err := attempts.LookupFor(ctx, uuid.Nil, uuid.New()); ok || err != nil {
		t.Errorf("LookupFor of a command without a tenant = %v, %v; there is nothing to find", ok, err)
	}
	if !events.ByTenant() {
		t.Error("the inbox of a router with cells is kept per tenant")
	}
	if (store.Events{DB: pl.Pooled}).ByTenant() {
		t.Error("the inbox of a plain pool is the one table")
	}
}

// Commands (the command worker) run in the place of their command's tenant: each tenant's command is executed once, writes one attempt and one ledger row in its
// own place, finds its own stored reply when redelivered and not another tenant's, and a tenant that is moving is neither charged nor told that no provider is
// configured: the command is retried.
func TestCommandsRunInTheirTenantsPlace(t *testing.T) {
	pl := openPlaces(t)
	ctx := context.Background()
	psp := newScripted(t, 200)
	attempts, ledger := store.Attempts{DB: pl.DB}, store.Ledger{DB: pl.DB}
	h := &handler.Handler{Sandbox: payments.NewMockPSP(psp.srv.URL, 2*time.Second), Attempts: attempts, Ledger: ledger}

	// More tenants than places, handled concurrently: none skipped, none twice, none in another's transaction.
	tenants := append([]uuid.UUID{}, pl.all...)
	for range 8 {
		tenants = append(tenants, uuid.New())
	}
	commands := map[uuid.UUID]uuid.UUID{}
	invoices := map[uuid.UUID]uuid.UUID{}
	for _, tn := range tenants {
		commands[tn], invoices[tn] = uuid.New(), uuid.New()
	}
	var wg sync.WaitGroup
	replies := make([]*commandsv1.ReplyEnvelope, len(tenants))
	errs := make([]error, len(tenants))
	for i, tn := range tenants {
		wg.Go(func() { replies[i], errs[i] = h.Handle(ctx, sandboxChargeOf(tn, invoices[tn], commands[tn])) })
	}
	wg.Wait()
	for i, tn := range tenants {
		if errs[i] != nil || replies[i].GetPaymentSucceeded().GetPspChargeId() != "ch_scripted" {
			t.Fatalf("tenant %s: %v, %v", tn, replies[i], errs[i])
		}
	}
	psp.mu.Lock()
	if len(psp.keys) != len(tenants) {
		t.Fatalf("the provider saw %d charges for %d commands: a command was skipped or run twice", len(psp.keys), len(tenants))
	}
	psp.mu.Unlock()
	for _, tn := range tenants {
		rows, err := ledger.List(ctx, tn, 10)
		if err != nil || len(rows) != 1 || *rows[0].InvoiceID != invoices[tn].String() {
			t.Errorf("tenant %s: ledger = %+v, %v; want exactly its own row", tn, rows, err)
		}
		a, ok, err := attempts.ChargeOf(ctx, tn, invoices[tn].String()+":charge:0")
		if err != nil || !ok || a.TenantID != tn || a.InvoiceID != invoices[tn] {
			t.Errorf("tenant %s: attempt = %+v %v %v", tn, a, ok, err)
		}
	}
	pl.noForeignRows(t)

	// The inbox: a reply is stored and found for its own tenant's command and for nobody else's.
	tn, other := pl.a1, pl.a2
	reply := &commandsv1.ReplyEnvelope{CommandId: commands[tn].String(), Reply: &commandsv1.ReplyEnvelope_PaymentSucceeded{PaymentSucceeded: &commandsv1.PaymentSucceeded{PspChargeId: "ch_scripted"}}}
	if _, ok, err := attempts.LookupFor(ctx, tn, commands[tn]); ok || err != nil {
		t.Fatalf("a reply before the command was finished: %v, %v", ok, err)
	}
	if err := attempts.StoreFor(ctx, tn, commands[tn], reply); err != nil {
		t.Fatal(err)
	}
	got, ok, err := attempts.LookupFor(ctx, tn, commands[tn])
	if err != nil || !ok || !proto.Equal(got, reply) {
		t.Fatalf("stored reply = %v %v %v", got, ok, err)
	}
	if _, ok, err := attempts.LookupFor(ctx, other, commands[tn]); ok || err != nil {
		t.Errorf("tenant %s found the reply to a1's command: %v, %v", other, ok, err)
	}
	// A redelivery of a command finds its attempt in its place and is told so.
	if again, err := attempts.Start(ctx, commands[tn], tn, invoices[tn], "charge", "sandbox", "mockpsp", invoices[tn].String()+":charge:0", map[string]any{}); err != nil || !again {
		t.Errorf("a redelivered command: redelivered = %v, %v", again, err)
	}

	for _, moving := range pl.all {
		t.Run("moving "+moving.String()[:8], func(t *testing.T) {
			before := len(psp.keys)
			pl.Dir.setMoving(moving, true)
			defer pl.Dir.setMoving(moving, false)
			r, err := h.Handle(ctx, sandboxChargeOf(moving, uuid.New(), uuid.New()))
			if !errors.Is(err, tenantdir.ErrNotServing) || r != nil {
				t.Fatalf("command of a moving tenant = %v, %v; want it retried (ErrNotServing)", r, err)
			}
			if _, _, err := attempts.LookupFor(ctx, moving, uuid.New()); !errors.Is(err, tenantdir.ErrNotServing) {
				t.Errorf("the inbox of a moving tenant answered: %v", err)
			}
			psp.mu.Lock()
			defer psp.mu.Unlock()
			if len(psp.keys) != before {
				t.Error("the provider was called for a tenant that is moving")
			}
		})
	}
	// And served again once the move is over.
	if r, err := h.Handle(ctx, sandboxChargeOf(pl.a1, uuid.New(), uuid.New())); err != nil || r.GetPaymentSucceeded() == nil {
		t.Fatalf("after the move: %v, %v", r, err)
	}
}

// A live command resolves the tenant's provider account in the tenant's place; for a tenant that is moving it is retried, never answered "provider not
// configured" (a final answer billing would act on).
func TestLiveCommandOfAMovingTenantIsRetried(t *testing.T) {
	pl := openPlaces(t)
	ctx := context.Background()
	fake := newFakeIyzico(t)
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	accounts := store.Accounts{DB: pl.DB, Box: newBox(t)}
	h := &handler.Handler{Sandbox: payments.NewMockPSP("http://unused", 0), Attempts: store.Attempts{DB: pl.DB}, Ledger: store.Ledger{DB: pl.DB},
		Resolver: &handler.Resolver{Registry: registry, Accounts: accounts}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	settings := `{"api_key":"api","secret_key":"TOP-SECRET-KEY","base_url":"` + fake.srv.URL + `","buyer":{"name":"Ada","surname":"L","identity_number":"11111111111","address":"x","city":"Istanbul","country":"Turkey","zip_code":"34000","ip":"1.2.3.4","email":"b@example.com"}}`
	for _, tn := range pl.all {
		if err := accounts.Put(ctx, tn, "iyzico", "live", []byte(settings), true); err != nil {
			t.Fatal(err)
		}
		r, err := h.Handle(ctx, liveCharge(tn, uuid.New(), uuid.New(), "TRY"))
		if err != nil || r.GetPaymentSucceeded().GetPspChargeId() != "P1" {
			t.Fatalf("tenant %s: %v, %v", tn, r, err)
		}
	}
	if got := fake.count("/payment/auth"); got != len(pl.all) {
		t.Fatalf("iyzico charged %d times for %d tenants", got, len(pl.all))
	}
	for _, tn := range pl.all {
		pl.Dir.setMoving(tn, true)
		r, err := h.Handle(ctx, liveCharge(tn, uuid.New(), uuid.New(), "TRY"))
		if !errors.Is(err, tenantdir.ErrNotServing) || r != nil {
			t.Errorf("tenant %s: %v, %v; want ErrNotServing and no reply", tn, r, err)
		}
		pl.Dir.setMoving(tn, false)
	}
	if got := fake.count("/payment/auth"); got != len(pl.all) {
		t.Errorf("iyzico was charged for a tenant that was moving: %d calls", got)
	}
	pl.noForeignRows(t)
}

// The provider webhook server finds the tenant of a notification that names none through one index per place, keeps the notification in that tenant's inbox,
// confirms it with that tenant's own provider account, and answers "try again" for a tenant that is moving.
func TestWebhookFindsTheTenantAcrossPlaces(t *testing.T) {
	pl := openPlaces(t)
	ctx := context.Background()
	fake := newFakeIyzico(t)
	box := newBox(t)
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	accounts := store.Accounts{DB: pl.DB, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	attempts, ledger := store.Attempts{DB: pl.DB}, store.Ledger{DB: pl.DB}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &handler.Handler{Sandbox: payments.NewMockPSP("http://unused", 0), Attempts: attempts, Ledger: ledger, Resolver: resolver, Log: log}
	api := httptest.NewServer((&webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: store.Events{DB: pl.DB}, Attempts: attempts, Ledger: ledger,
		Log: log, Token: "internal"}).Handler())
	defer api.Close()
	settings := `{"api_key":"api","secret_key":"TOP-SECRET-KEY","base_url":"` + fake.srv.URL + `","buyer":{"name":"Ada","surname":"L","identity_number":"11111111111","address":"x","city":"Istanbul","country":"Turkey","zip_code":"34000","ip":"1.2.3.4","email":"b@example.com"}}`
	notify := func(ref, conversation string) (int, string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"iyziEventType": "API_AUTH", "iyziReferenceCode": ref, "paymentId": 1, "paymentConversationId": conversation, "status": "SUCCESS", "iyziEventTime": 1})
		resp, err := http.Post(api.URL+"/webhooks/iyzico", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(out)
	}

	// iyzico names the invoice of the charge it confirms; the fake answers for the last charge it saw, so say which one is asked about.
	invoices := map[uuid.UUID]uuid.UUID{}
	setBasket := func(tn uuid.UUID) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		fake.basket = invoices[tn].String()
	}
	keys := map[uuid.UUID]string{}
	for _, tn := range pl.all {
		if err := accounts.Put(ctx, tn, "iyzico", "live", []byte(settings), true); err != nil {
			t.Fatal(err)
		}
		inv := uuid.New()
		invoices[tn] = inv
		keys[tn] = inv.String() + ":charge:0"
		if r, err := h.Handle(ctx, liveCharge(tn, inv, uuid.New(), "TRY")); err != nil || r.GetPaymentSucceeded() == nil {
			t.Fatalf("charge of %s: %v, %v", tn, r, err)
		}
	}

	// The index is one row per charge key in the cell, none for the pooled tenant (the pooled table is the index there).
	for name, want := range map[string][]uuid.UUID{"c1": {pl.a1, pl.a2}, "c2": {pl.b}} {
		var got []uuid.UUID
		rows, err := pl.pools[name].Query(ctx, `SELECT tenant_id FROM shared.charge_keys ORDER BY indexed_at`)
		if err != nil {
			t.Fatal(err)
		}
		got, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil || len(got) != len(want) {
			t.Fatalf("cell %s indexes %v, %v; want the keys of %v", name, got, err, want)
		}
	}

	for i, tn := range pl.all {
		event := fmt.Sprintf("ev-%d", i)
		setBasket(tn)
		if code, body := notify(event, keys[tn]); code != 200 || !strings.Contains(body, "processed") {
			t.Fatalf("tenant %s: %d %s", tn, code, body)
		}
		if code, body := notify(event, keys[tn]); code != 200 || !strings.Contains(body, "duplicate") {
			t.Fatalf("tenant %s, the same event again: %d %s", tn, code, body)
		}
		// The notification is in its tenant's inbox, processed and attributed to it.
		var status string
		var owner *uuid.UUID
		if err := pl.At(tn).QueryRow(ctx, `SELECT status, tenant_id FROM provider_events WHERE provider = 'iyzico' AND event_id = $1`, event).Scan(&status, &owner); err != nil ||
			status != "processed" || owner == nil || *owner != tn {
			t.Errorf("tenant %s: inbox row = %q %v, %v", tn, status, owner, err)
		}
		// The command's answer and the confirmed notification are one fact, in that tenant's ledger: still one row.
		rows, err := ledger.List(ctx, tn, 10)
		if err != nil || len(rows) != 1 || *rows[0].InvoiceID != invoices[tn].String() {
			t.Errorf("tenant %s: ledger = %+v, %v; want its one row", tn, rows, err)
		}
	}
	// Cell tenants' notifications are not in the pooled inbox at all.
	var inPooled int
	if err := pl.Pooled.QueryRow(ctx, `SELECT count(*) FROM provider_events WHERE tenant_id = ANY($1)`, []uuid.UUID{pl.a1, pl.a2, pl.b}).Scan(&inPooled); err != nil || inPooled != 0 {
		t.Errorf("the pooled inbox holds %d notifications of tenants in cells, %v", inPooled, err)
	}
	pl.noForeignRows(t)

	// A key nobody made is ignored, and kept where the notifications of no tenant are: the pooled database.
	if code, body := notify("ev-unknown", "someone-elses-charge"); code != 200 || !strings.Contains(body, "ignored") {
		t.Fatalf("unknown key: %d %s", code, body)
	}
	var orphan int
	if err := pl.Pooled.QueryRow(ctx, `SELECT count(*) FROM provider_events WHERE event_id = 'ev-unknown' AND tenant_id IS NULL AND status = 'ignored'`).Scan(&orphan); err != nil || orphan != 1 {
		t.Errorf("the ignored notification is in the pooled inbox %d times, %v", orphan, err)
	}

	// A URL that names a tenant the directory does not know is no webhook, and nothing is written for it.
	stranger := uuid.New()
	pl.Dir.forget(stranger)
	body, _ := json.Marshal(map[string]any{"iyziEventType": "API_AUTH", "iyziReferenceCode": "ev-stranger", "paymentId": 1, "paymentConversationId": "k", "status": "SUCCESS", "iyziEventTime": 1})
	if resp, err := http.Post(api.URL+"/webhooks/iyzico/"+stranger.String()+"/live", "application/json", bytes.NewReader(body)); err != nil || resp.StatusCode != 404 {
		t.Errorf("a webhook of an unknown tenant: %v, %v", resp, err)
	} else {
		_ = resp.Body.Close()
	}

	// A moving tenant's notification is not lost and not recorded anywhere: iyzico retries it.
	for i, tn := range pl.all {
		event := fmt.Sprintf("ev-moving-%d", i)
		setBasket(tn)
		pl.Dir.setMoving(tn, true)
		before := fake.count("/payment/detail")
		if code, body := notify(event, keys[tn]); code != 503 || !strings.Contains(body, "payments.tenant.unavailable") {
			t.Errorf("tenant %s moving: %d %s; want 503 so the provider retries", tn, code, body)
		}
		if fake.count("/payment/detail") != before {
			t.Errorf("tenant %s: the provider was asked to confirm a notification of a moving tenant", tn)
		}
		pl.Dir.setMoving(tn, false)
		if code, body := notify(event, keys[tn]); code != 200 || !strings.Contains(body, "processed") {
			t.Errorf("tenant %s, the retry after the move: %d %s", tn, code, body)
		}
	}
	pl.noForeignRows(t)
}

// A sandbox reset deletes one tenant's rows, in its own place, in one transaction, and leaves every other tenant's (same cell, other cell, pooled) alone; it also
// takes the tenant's keys out of the cell's index, and is refused while the tenant moves.
func TestSandboxResetPerPlace(t *testing.T) {
	pl := openPlaces(t)
	ctx := context.Background()
	attempts, ledger := store.Attempts{DB: pl.DB}, store.Ledger{DB: pl.DB}
	accounts := store.Accounts{DB: pl.DB, Box: newBox(t)}
	reset := paymentsreset.New(pl.DB)
	keyOf := map[uuid.UUID]string{}
	for _, tn := range pl.all {
		inv := uuid.New()
		keyOf[tn] = inv.String() + ":charge:0"
		if _, err := attempts.Start(ctx, uuid.New(), tn, inv, "charge", "sandbox", "mockpsp", keyOf[tn], map[string]any{}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(ctx, store.LedgerEntry{TenantID: tn, InvoiceID: inv, Provider: "mockpsp", Environment: "sandbox", Kind: "charge", Status: "succeeded",
			AmountMinor: 100, Currency: "USD", ProviderRef: "ch_" + inv.String(), Source: "command"}); err != nil {
			t.Fatal(err)
		}
		if err := accounts.Put(ctx, tn, "banktransfer", "sandbox", []byte(`{"bank_name":"X","account_holder":"Y","iban":"DE89370400440532013000"}`), false); err != nil {
			t.Fatal(err)
		}
	}
	count := func(tn uuid.UUID, table string) int {
		t.Helper()
		var n int
		if err := pl.At(tn).QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tn).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Moving: refused, nothing deleted.
	pl.Dir.setMoving(pl.a1, true)
	if _, err := reset.Reset(ctx, pl.a1); !errors.Is(err, tenantdir.ErrNotServing) {
		t.Fatalf("reset of a moving tenant: %v", err)
	}
	pl.Dir.setMoving(pl.a1, false)
	if count(pl.a1, "payment_ledger") != 1 {
		t.Fatal("a refused reset deleted rows")
	}

	// Each tenant in turn: its rows go, everyone else's stay.
	for i, tn := range pl.all {
		out, err := reset.Reset(ctx, tn)
		if err != nil || out["payment_ledger"] != 1 || out["payment_attempts"] != 1 {
			t.Fatalf("reset of %s: %v, %v", tn, out, err)
		}
		if count(tn, "payment_ledger") != 0 || count(tn, "payment_attempts") != 0 {
			t.Errorf("tenant %s still has attempts or ledger rows", tn)
		}
		if count(tn, "provider_accounts") != 1 {
			t.Errorf("the provider account of %s was deleted", tn)
		}
		for j, other := range pl.all {
			want := 1
			if j <= i {
				want = 0
			}
			if got := count(other, "payment_ledger"); got != want {
				t.Errorf("after the reset of %s, tenant %s has %d ledger rows, want %d", tn, other, got, want)
			}
			if got := count(other, "payment_attempts"); got != want {
				t.Errorf("after the reset of %s, tenant %s has %d attempts, want %d", tn, other, got, want)
			}
		}
		if out, err := reset.Reset(ctx, tn); err != nil || len(out) != 0 {
			t.Errorf("a second reset of %s: %v, %v", tn, out, err)
		}
	}
	// The cells' indexes no longer name a reset tenant's key.
	for name, pool := range pl.pools {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM shared.charge_keys`).Scan(&n); err != nil || n != 0 {
			t.Errorf("cell %s still indexes %d keys of reset tenants, %v", name, n, err)
		}
	}
	for _, tn := range pl.all {
		if a, ok, err := attempts.ChargeByKey(ctx, keyOf[tn]); err != nil || ok {
			t.Errorf("the key of reset tenant %s is still found: %+v %v %v", tn, a, ok, err)
		}
	}
	pl.noForeignRows(t)
}

// The key index finds a charge's tenant in the pooled table and in each cell, follows a copy of a tenant into a cell (the trigger fires on the copy's inserts),
// refuses while the tenant moves, and a cell with no tenant yet has no index to ask.
func TestChargeByKeyAcrossPlaces(t *testing.T) {
	pg := testkit.StartPostgres(t)
	w := openWorld(t, pg, true, "c1", "c2", "empty")
	ctx := context.Background()
	attempts := store.Attempts{DB: w.DB}
	pooled, inC1, inC2 := uuid.New(), uuid.New(), uuid.New()
	w.Dir.assign(pooled, "")
	w.Dir.assign(inC1, "c1")
	w.Dir.assign(inC2, "c2")
	keys := map[uuid.UUID]string{}
	for _, tn := range []uuid.UUID{pooled, inC1, inC2} {
		inv := uuid.New()
		keys[tn] = inv.String() + ":charge:0"
		if _, err := attempts.Start(ctx, uuid.New(), tn, inv, "charge", "live", "iyzico", keys[tn], map[string]any{"amount_minor": 5, "currency": "EUR"}); err != nil {
			t.Fatal(err)
		}
		// Refunds and status queries are not charges: they are not in the index.
		if _, err := attempts.Start(ctx, uuid.New(), tn, inv, "refund", "live", "iyzico", keys[tn]+"-refund", map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}
	for tn, key := range keys {
		a, ok, err := attempts.ChargeByKey(ctx, key)
		if err != nil || !ok || a.TenantID != tn || a.AmountMinor != 5 || a.Currency != "EUR" {
			t.Errorf("key of %s: %+v %v %v", tn, a, ok, err)
		}
		if _, ok, err := attempts.ChargeByKey(ctx, key+"-refund"); err != nil || ok {
			t.Errorf("a refund's key was found as a charge: %v %v", ok, err)
		}
	}
	if _, ok, err := attempts.ChargeByKey(ctx, "no such key"); err != nil || ok {
		t.Errorf("unknown key: %v %v", ok, err)
	}
	var n int
	if err := w.pools["c1"].QueryRow(ctx, `SELECT count(*) FROM shared.charge_keys`).Scan(&n); err != nil || n != 1 {
		t.Errorf("c1 indexes %d keys, %v; want only the charge key of its one tenant", n, err)
	}
	// "empty" has no tenant, so no shared.charge_keys: asking it is "not there", not an error (the lookups above went through it).

	// A moving tenant makes its key unanswerable: the notification has to be retried, not dropped as unknown.
	w.Dir.setMoving(inC1, true)
	if _, ok, err := attempts.ChargeByKey(ctx, keys[inC1]); !errors.Is(err, tenantdir.ErrNotServing) || ok {
		t.Errorf("key of a moving tenant: %v %v", ok, err)
	}
	w.Dir.setMoving(inC1, false)
}

// Moving a tenant between cells loses no row (pgxx.Move) and moves its place in the index; moving it back into the pooled database is refused, because the
// receipts point at ledger rows by an identity column that renumbering would break.
func TestMovesOfPaymentsTenants(t *testing.T) {
	pg := testkit.StartPostgres(t)
	w := openWorld(t, pg, true, "c1", "c2")
	ctx := context.Background()
	tn := uuid.New()
	w.Dir.assign(tn, "")
	attempts, ledger, transfers := store.Attempts{DB: w.DB}, store.Ledger{DB: w.DB}, store.Transfers{DB: w.DB}
	inv := uuid.New()
	key := inv.String() + ":charge:0"
	if _, err := attempts.Start(ctx, uuid.New(), tn, inv, "charge", "live", "banktransfer", key, map[string]any{"amount_minor": 100, "currency": "EUR"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, store.LedgerEntry{TenantID: tn, InvoiceID: inv, Provider: "banktransfer", Environment: "live", Kind: "charge", Status: "pending",
		AmountMinor: 100, Currency: "EUR", ProviderRef: key, IdempotencyKey: key, Source: "command", Detail: "instructions:REF"}); err != nil {
		t.Fatal(err)
	}
	if _, err := transfers.Record(ctx, store.NewReceipt{TenantID: tn, InvoiceID: inv, Provider: "banktransfer", Environment: "live", AmountMinor: 60, Currency: "EUR",
		BankReference: "B1", ReceivedAt: time.Now(), RecordedBy: "operator"}, 100); err != nil {
		t.Fatal(err)
	}
	move := func(from, to string) pgxx.Move {
		ep := func(cell string) pgxx.Endpoint {
			if cell == "" {
				return pgxx.Endpoint{MigratorDSN: w.PooledMigrator}
			}
			return pgxx.Endpoint{Cell: cell, MigratorDSN: w.cells[cell].Migrator}
		}
		return pgxx.Move{TenantID: tn, From: ep(from), To: ep(to), Schemas: tenantSchemas()}
	}

	rep, err := move("", "c1").Copy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows() != 4 {
		t.Fatalf("copied %d rows, want the attempt, the pending and the manual ledger row, and the receipt", rep.Rows())
	}
	var indexed int
	if err := w.pools["c1"].QueryRow(ctx, `SELECT count(*) FROM shared.charge_keys WHERE tenant_id = $1 AND idempotency_key = $2`, tn, key).Scan(&indexed); err != nil || indexed != 1 {
		t.Fatalf("the copy of the attempt is indexed %d times in c1, %v", indexed, err)
	}
	// Serve it from the cell: the pooled rows are retired, the directory points at c1.
	if _, err := move("", "c1").RetireSource(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	w.Dir.assign(tn, "c1")
	got, err := transfers.Received(ctx, tn, inv, "banktransfer")
	if err != nil || got.Minor != 60 {
		t.Fatalf("received after the move = %+v, %v", got, err)
	}
	if a, ok, err := attempts.ChargeByKey(ctx, key); err != nil || !ok || a.TenantID != tn {
		t.Fatalf("the key after the move: %+v %v %v", a, ok, err)
	}
	// Cell to cell, and the key still finds the tenant (the old cell's entry names the same tenant, the directory says where it is now).
	if _, err := move("c1", "c2").Copy(ctx); err != nil {
		t.Fatal(err)
	}
	w.Dir.assign(tn, "c2")
	if a, ok, err := attempts.ChargeByKey(ctx, key); err != nil || !ok || a.TenantID != tn {
		t.Fatalf("the key after a move between cells: %+v %v %v", a, ok, err)
	}
	// Back into the pooled database: refused before anything is written.
	if _, err := move("c2", "").Copy(ctx); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("a move into the pooled database: %v; want the refusal about the identity column the receipts refer to", err)
	}
}
