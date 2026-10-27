package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
)

// lazyPool is a pool that never connects: every test here must be answered before a statement is sent, so a statement that does get sent fails the test by
// not connecting.
func lazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), "postgres://nobody:nothing@127.0.0.1:1/none?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func routerOf(t *testing.T, dir tenantdir.Fixed) *pgxx.Router {
	t.Helper()
	r, err := pgxx.NewRouter(lazyPool(t), map[string]*pgxpool.Pool{"c1": lazyPool(t)}, dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// A tenant that is moving is refused by every store before a statement is sent: no store reads or writes where it cannot say the data is.
func TestEveryStoreRefusesAMovingTenant(t *testing.T) {
	moving, cell, ghost := uuid.New(), uuid.New(), uuid.New()
	r := routerOf(t, tenantdir.Fixed{
		moving: {TenantID: moving, Cell: "c1", Status: tenantdir.StatusMoving},
		cell:   {TenantID: cell, Cell: "c1", Status: tenantdir.StatusReady},
		ghost:  {TenantID: ghost, Cell: "elsewhere", Status: tenantdir.StatusReady},
	})
	ctx := context.Background()
	box, err := secretbox.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	inv, now := uuid.New(), time.Now()
	calls := map[string]func(tenant uuid.UUID) error{
		"attempt start": func(tn uuid.UUID) error {
			_, err := Attempts{DB: r}.Start(ctx, uuid.New(), tn, inv, KindCharge, "live", "iyzico", "k", map[string]any{})
			return err
		},
		"reply lookup": func(tn uuid.UUID) error { _, _, err := Attempts{DB: r}.LookupFor(ctx, tn, uuid.New()); return err },
		"reply store": func(tn uuid.UUID) error {
			return Attempts{DB: r}.StoreFor(ctx, tn, uuid.New(), &commandsv1.ReplyEnvelope{})
		},
		"charge by tenant": func(tn uuid.UUID) error { _, _, err := Attempts{DB: r}.ChargeOf(ctx, tn, "k"); return err },
		"charge provider":  func(tn uuid.UUID) error { _, _, err := Attempts{DB: r}.ChargeProvider(ctx, tn, inv); return err },
		"link attempts":    func(tn uuid.UUID) error { _, err := Attempts{DB: r}.CountLinkAttempts(ctx, tn, inv); return err },
		"inbox receive": func(tn uuid.UUID) error {
			_, err := Events{DB: r}.Receive(ctx, tn, "paddle", "e", []byte(`{}`))
			return err
		},
		"inbox finish": func(tn uuid.UUID) error { return Events{DB: r}.Finish(ctx, "paddle", "e", tn, "processed", "") },
		"inbox forget": func(tn uuid.UUID) error { return Events{DB: r}.Forget(ctx, tn, "paddle", "e") },
		"ledger append": func(tn uuid.UUID) error {
			_, err := Ledger{DB: r}.Append(ctx, LedgerEntry{TenantID: tn, Provider: "iyzico", Environment: "live", Kind: KindCharge, Status: StatusSucceeded, Currency: "TRY"})
			return err
		},
		"ledger list": func(tn uuid.UUID) error { _, err := Ledger{DB: r}.List(ctx, tn, 1); return err },
		"ledger outcomes": func(tn uuid.UUID) error {
			_, err := Ledger{DB: r}.Outcomes(ctx, tn, "iyzico", KindCharge, "r")
			return err
		},
		"ledger fact":    func(tn uuid.UUID) error { _, _, err := Ledger{DB: r}.ChargeFact(ctx, tn, "k"); return err },
		"ledger charges": func(tn uuid.UUID) error { _, err := Ledger{DB: r}.SuccessfulCharges(ctx, tn); return err },
		"ledger lookup": func(tn uuid.UUID) error {
			_, err := Ledger{DB: r}.LookupInvoices(ctx, tn, []uuid.UUID{inv})
			return err
		},
		"account put": func(tn uuid.UUID) error {
			return Accounts{DB: r, Box: box}.Put(ctx, tn, "iyzico", "live", []byte(`{}`), true)
		},
		"account list": func(tn uuid.UUID) error { _, err := Accounts{DB: r, Box: box}.List(ctx, tn); return err },
		"account open": func(tn uuid.UUID) error {
			_, _, err := Accounts{DB: r, Box: box}.Settings(ctx, tn, "iyzico", "live")
			return err
		},
		"account default": func(tn uuid.UUID) error { _, _, err := Accounts{DB: r, Box: box}.Default(ctx, tn, "live"); return err },
		"account delete": func(tn uuid.UUID) error {
			_, err := Accounts{DB: r, Box: box}.Delete(ctx, tn, "iyzico", "live")
			return err
		},
		"object put": func(tn uuid.UUID) error { return Objects{DB: r}.Put(ctx, tn, "paddle", "live", "price", "k", "ref") },
		"object get": func(tn uuid.UUID) error {
			_, _, err := Objects{DB: r}.Get(ctx, tn, "paddle", "live", "price", "k")
			return err
		},
		"subscription put": func(tn uuid.UUID) error {
			return Subscriptions{DB: r}.Upsert(ctx, tn, "paddle", "live", "s", nil, "active", nil, now)
		},
		"subscription list": func(tn uuid.UUID) error { _, err := Subscriptions{DB: r}.List(ctx, tn); return err },
		"transfer receipt": func(tn uuid.UUID) error {
			_, err := Transfers{DB: r}.Record(ctx, NewReceipt{TenantID: tn, InvoiceID: inv, AmountMinor: 1}, 10)
			return err
		},
		"transfer sum": func(tn uuid.UUID) error {
			_, err := Transfers{DB: r}.Received(ctx, tn, inv, "banktransfer")
			return err
		},
		"transfer pending": func(tn uuid.UUID) error {
			_, _, err := Transfers{DB: r}.Pending(ctx, tn, "banktransfer", "k")
			return err
		},
		"einvoice put": func(tn uuid.UUID) error {
			return EInvoiceAccounts{DB: r, Box: box}.Put(ctx, tn, "live", "nilvera", "production", []byte(`{}`))
		},
		"einvoice begin": func(tn uuid.UUID) error {
			_, err := EInvoiceIssuances{DB: r}.Begin(ctx, uuid.New(), tn, "live", "nilvera", "invoice", "e-arsiv")
			return err
		},
	}
	for name, call := range calls {
		if err := call(moving); !errors.Is(err, tenantdir.ErrNotServing) {
			t.Errorf("%s for a moving tenant: %v; want ErrNotServing", name, err)
		}
		if err := call(ghost); !errors.Is(err, pgxx.ErrUnknownCell) {
			t.Errorf("%s for a tenant in a cell this process has no pool for: %v; want ErrUnknownCell", name, err)
		}
	}
}

// With cells on a statement that names no tenant is refused, not answered from the pooled database; with them off (a plain pool) the same call is the one table.
func TestNoTenantIsRefusedOnlyWhenCellsAreOn(t *testing.T) {
	r := routerOf(t, tenantdir.Fixed{})
	ctx := context.Background()
	if err := (Attempts{DB: r}).StoreFor(ctx, uuid.Nil, uuid.New(), &commandsv1.ReplyEnvelope{}); !errors.Is(err, ErrNoTenant) {
		t.Errorf("StoreFor without a tenant: %v", err)
	}
	if _, err := (Attempts{DB: r}).Start(ctx, uuid.New(), uuid.Nil, uuid.New(), KindCharge, "live", "p", "k", map[string]any{}); !errors.Is(err, ErrNoTenant) {
		t.Errorf("Start without a tenant: %v", err)
	}
	if _, ok, err := (Attempts{DB: r}).LookupFor(ctx, uuid.Nil, uuid.New()); ok || err != nil {
		t.Errorf("LookupFor without a tenant: %v, %v", ok, err)
	}
	if !(Events{DB: r}).ByTenant() {
		t.Error("a router with a cell keeps the inbox per tenant")
	}
	// A router without cells is the pooled database: nothing changes for it.
	plain, err := pgxx.NewRouter(lazyPool(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if (Events{DB: plain}).ByTenant() || (Events{DB: lazyPool(t)}).ByTenant() {
		t.Error("without cells the inbox is the one table")
	}
	if err := requireTenant(plain, uuid.Nil); err != nil {
		t.Errorf("a router without cells refused a nil tenant: %v", err)
	}
}
