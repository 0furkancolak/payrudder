//go:build integration

package itest

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/plans-svc/migrations"
)

// The hosted withdrawal function read "is there a withdrawal for this invoice?" and then inserted, so two presses of the button that raced both inserted and the customer was
// acknowledged twice. Migration 00022 makes the database refuse the second: one open (not rejected) hosted withdrawal for an invoice.
func testContractWithdrawalOpenOnce(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Withdraw Once Co")[plansv1.Environment_ENVIRONMENT_LIVE].GetId()
	ctx := as(live)
	c := e.contracts
	for _, ext := range []string{"ada", "bob"} {
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@example.com", Locale: "en", CustomerType: "individual"}); err != nil {
			t.Fatal(err)
		}
	}
	hosted := func(customer, invoice string) (*plansv1.CreateContractWithdrawalResponse, error) {
		return c.CreateContractWithdrawal(ctx, &plansv1.CreateContractWithdrawalRequest{CustomerExternalId: customer, InvoiceId: invoice, Source: "hosted", Statement: "Your name: " + customer})
	}
	count := func(invoice string) (rows, facts int) {
		if err := e.scan(context.Background(), live, `SELECT count(*) FROM contract_withdrawals WHERE tenant_id = $1 AND invoice_id = $2`, []any{uuid.MustParse(live), invoice}, &rows); err != nil {
			t.Fatal(err)
		}
		if err := e.scan(context.Background(), live, `SELECT count(*) FROM outbox WHERE tenant_id = $1 AND topic = 'contract.withdrawal-received.v1'`, []any{uuid.MustParse(live)}, &facts); err != nil {
			t.Fatal(err)
		}
		return rows, facts
	}

	t.Run("a second press that raced the first is refused and acknowledged no more than once", func(t *testing.T) {
		inv := uuid.NewString()
		const presses = 8
		var wg sync.WaitGroup
		errs := make([]error, presses)
		start := make(chan struct{})
		for i := range presses {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, errs[i] = hosted("ada", inv)
			}()
		}
		close(start)
		wg.Wait()
		ok := 0
		for i, err := range errs {
			if err == nil {
				ok++
				continue
			}
			wantCode(t, fmt.Sprintf("press %d", i), err, codes.AlreadyExists, "plans.common.already_exists")
		}
		if rows, _ := count(inv); ok != 1 || rows != 1 {
			t.Fatalf("%d presses succeeded and %d withdrawals were recorded, want 1 and 1", ok, rows)
		}
		var facts int
		if err := e.scan(context.Background(), live, `SELECT count(*) FROM outbox o WHERE o.tenant_id = $1 AND o.topic = 'contract.withdrawal-received.v1' AND position(convert_to($2, 'UTF8') in o.payload) > 0`, []any{uuid.MustParse(live), inv}, &facts); err != nil || facts != 1 {
			t.Errorf("acknowledgements queued for the invoice: %d %v, want 1", facts, err)
		}
		// Another customer's statement about the same invoice is the same invoice.
		_, err := hosted("bob", inv)
		wantCode(t, "another customer, the same invoice", err, codes.AlreadyExists, "plans.common.already_exists")
	})

	t.Run("once the first is rejected the invoice can be withdrawn from again, and other invoices are free", func(t *testing.T) {
		inv := uuid.NewString()
		first, err := hosted("ada", inv)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.SetContractWithdrawalStatus(ctx, &plansv1.SetContractWithdrawalStatusRequest{Id: first.GetWithdrawal().GetId(), Status: "rejected", DecidedBy: "owner@example.com"}); err != nil {
			t.Fatal(err)
		}
		if _, err := hosted("ada", inv); err != nil {
			t.Errorf("after the first was rejected: %v", err)
		}
		if _, err := hosted("ada", uuid.NewString()); err != nil {
			t.Errorf("another invoice: %v", err)
		}
		// An accepted withdrawal still holds the invoice.
		acc := uuid.NewString()
		w, _ := hosted("ada", acc)
		if _, err := c.SetContractWithdrawalStatus(ctx, &plansv1.SetContractWithdrawalStatusRequest{Id: w.GetWithdrawal().GetId(), Status: "accepted", DecidedBy: "owner@example.com"}); err != nil {
			t.Fatal(err)
		}
		_, err = hosted("ada", acc)
		wantCode(t, "an accepted withdrawal", err, codes.AlreadyExists, "plans.common.already_exists")
	})

	t.Run("what a seller records through the API or the console is not limited (default, not agreed)", func(t *testing.T) {
		inv := uuid.NewString()
		for _, src := range []string{"api", "api", "console"} {
			if _, err := c.CreateContractWithdrawal(ctx, &plansv1.CreateContractWithdrawalRequest{CustomerExternalId: "ada", InvoiceId: inv, Source: src}); err != nil {
				t.Errorf("%s: %v", src, err)
			}
		}
		if rows, _ := count(inv); rows != 3 {
			t.Errorf("%d recorded, want 3", rows)
		}
		// A withdrawal that names no invoice (a subscription, or nothing) is not an invoice's.
		for range 2 {
			if _, err := c.CreateContractWithdrawal(ctx, &plansv1.CreateContractWithdrawalRequest{CustomerExternalId: "ada", Source: "hosted"}); err != nil {
				t.Errorf("hosted without an invoice: %v", err)
			}
		}
	})
}

// Migration 00022 builds a unique index. Rows that already repeat an invoice would stop it, so the later undecided ones are rejected first ("system", with the reason) and a
// decided one is never touched: the earliest decided one, or else the earliest, keeps the invoice. Run on data made by the release before it.
func TestWithdrawalMigrationRejectsDuplicatesThatWereLeftByTheRace(t *testing.T) {
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	testkit.MigratedDB(t, pg, "plans", migrations.FS) // creates the roles the cell database is owned by
	db := testkit.CreateCellDB(t, pg, "plans", "wdonce")
	for v := int64(1); v <= 21; v++ {
		if err := pgxx.MigrateVersion(ctx, db.Migrator, migrations.FS, v, true); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := pgx.Connect(ctx, db.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	tenant, customer := uuid.New(), uuid.New()
	// The owner is under forced row-level security like everyone else: name the tenant for this connection.
	if _, err := conn.Exec(ctx, `SELECT set_config('app.tenant_id', $1, false)`, tenant.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO customers (tenant_id, id, external_id, name, email) VALUES ($1, $2, 'ada', 'Ada', 'ada@example.com')`, tenant, customer); err != nil {
		t.Fatal(err)
	}
	invoice, other := uuid.New(), uuid.New()
	ids := map[string]uuid.UUID{}
	add := func(name string, inv uuid.UUID, source, status string, minutes int) {
		id := uuid.New()
		ids[name] = id
		decided := "NULL"
		if status != "received" {
			decided = "now()"
		}
		if _, err := conn.Exec(ctx, fmt.Sprintf(`INSERT INTO contract_withdrawals (tenant_id, id, customer_id, invoice_id, source, status, received_at, decided_at)
			VALUES ($1, $2, $3, $4, $5, $6, now() - make_interval(mins => $7), %s)`, decided), tenant, id, customer, inv, source, status, minutes); err != nil {
			t.Fatal(err)
		}
	}
	add("first", invoice, "hosted", "received", 30)   // earliest, undecided
	add("second", invoice, "hosted", "received", 20)  // a duplicate
	add("decided", invoice, "hosted", "accepted", 10) // later, but the seller decided it: it keeps the invoice
	add("rejected", invoice, "hosted", "rejected", 40)
	add("api1", other, "api", "received", 5)
	add("api2", other, "api", "received", 4)
	if err := pgxx.MigrateVersion(ctx, db.Migrator, migrations.FS, 22, true); err != nil {
		t.Fatalf("the migration over duplicates: %v", err)
	}
	status := func(name string) (st, by string) {
		if err := conn.QueryRow(ctx, `SELECT status, decided_by FROM contract_withdrawals WHERE id = $1`, ids[name]).Scan(&st, &by); err != nil {
			t.Fatal(err)
		}
		return st, by
	}
	for name, want := range map[string]string{"decided": "accepted", "first": "rejected", "second": "rejected", "rejected": "rejected", "api1": "received", "api2": "received"} {
		if st, _ := status(name); st != want {
			t.Errorf("%s is %q after the migration, want %q", name, st, want)
		}
	}
	if _, by := status("first"); by != "system" {
		t.Errorf("a duplicate was rejected by %q, want system", by)
	}
	if _, by := status("decided"); by != "" {
		t.Errorf("the decided one was touched: %q", by)
	}
	// And now the database holds the line: another open hosted one for the invoice is refused, a rejected one is not.
	_, err = conn.Exec(ctx, `INSERT INTO contract_withdrawals (tenant_id, id, customer_id, invoice_id, source) VALUES ($1, $2, $3, $4, 'hosted')`, tenant, uuid.New(), customer, invoice)
	if pgxx.ErrCode(err) != pgxx.CodeUniqueViolation {
		t.Errorf("a second open hosted withdrawal for the invoice: %v, want a unique violation", err)
	}
	// Down drops the index and leaves the data as it is.
	if err := pgxx.MigrateVersion(ctx, db.Migrator, migrations.FS, 22, false); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO contract_withdrawals (tenant_id, id, customer_id, invoice_id, source) VALUES ($1, $2, $3, $4, 'hosted')`, tenant, uuid.New(), customer, invoice); err != nil {
		t.Errorf("after Down: %v", err)
	}
}
