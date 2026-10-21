//go:build integration

package itest

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/credits/command"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/credits/query"
)

func wantKey(t *testing.T, err error, code codes.Code, key string) {
	t.Helper()
	if status.Code(err) != code || errkey.Of(err) != key {
		t.Fatalf("error = %v (code %v, key %q), want %v / %s", err, status.Code(err), errkey.Of(err), code, key)
	}
}

func balanceOf(t *testing.T, tid, cid uuid.UUID) int64 {
	t.Helper()
	b, err := query.BalanceOf(context.Background(), shared.app, tid, cid)
	if err != nil {
		t.Fatal(err)
	}
	return b.Credits
}

func testCreditPacks(t *testing.T) {
	infra(t)
	ctx, tid := context.Background(), uuid.New()
	for _, bad := range []struct {
		name     string
		credits  int64
		price    int64
		currency string
		key      string
	}{
		{"", 100, 500, "USD", "billing.credits.bad_pack"},
		{"Starter", 0, 500, "USD", "billing.credits.bad_pack"},
		{"Starter", 100, 0, "USD", "billing.credits.bad_pack"},
		{"Starter", 100, 500, "XXX", "billing.credits.unknown_currency"},
	} {
		_, err := command.CreatePack(ctx, shared.app, tid, bad.name, bad.credits, bad.price, bad.currency)
		wantKey(t, err, codes.InvalidArgument, bad.key)
	}
	starter, err := command.CreatePack(ctx, shared.app, tid, "Starter", 1000, 500, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := command.CreatePack(ctx, shared.app, tid, "Team", 10000, 4000, "USD"); err != nil {
		t.Fatal(err)
	}
	if err := command.SetPackActive(ctx, shared.app, tid, starter.ID, false); err != nil {
		t.Fatal(err)
	}
	all, _ := query.Packs(ctx, shared.app, tid, false)
	active, _ := query.Packs(ctx, shared.app, tid, true)
	if len(all) != 2 || len(active) != 1 || active[0].Name != "Team" {
		t.Fatalf("packs = %d, active = %+v; want 2 and only Team", len(all), active)
	}
	wantKey(t, command.SetPackActive(ctx, shared.app, tid, uuid.New(), true), codes.NotFound, "billing.credits.pack_not_found")
	// Another tenant sees none of them.
	if other, _ := query.Packs(ctx, shared.app, uuid.New(), false); len(other) != 0 {
		t.Fatalf("another tenant sees %d packs", len(other))
	}
	if _, err := query.PackByID(ctx, shared.app, uuid.New(), starter.ID); status.Code(err) != codes.NotFound {
		t.Fatalf("a pack of another tenant: %v, want NotFound", err)
	}
}

func testCreditsConsumeTheSoonestExpiryFirst(t *testing.T) {
	infra(t)
	ctx, tid, cid := context.Background(), uuid.New(), uuid.New()
	soon := time.Now().Add(time.Hour)
	if _, err := command.Grant(ctx, shared.app, tid, cid, 50, "manual", nil, "never expires", "g-forever"); err != nil {
		t.Fatal(err)
	}
	if _, err := command.Grant(ctx, shared.app, tid, cid, 100, "promo", &soon, "launch", "g-promo"); err != nil {
		t.Fatal(err)
	}
	// A repeat of a grant adds nothing; the same key for another amount is refused.
	r, err := command.Grant(ctx, shared.app, tid, cid, 100, "promo", &soon, "launch", "g-promo")
	if err != nil || !r.Replayed || r.Balance != 150 {
		t.Fatalf("repeated grant = %+v, %v; want replayed, balance 150", r, err)
	}
	_, err = command.Grant(ctx, shared.app, tid, cid, 7, "promo", &soon, "launch", "g-promo")
	wantKey(t, err, codes.AlreadyExists, "billing.credits.key_reused")
	_, err = command.Grant(ctx, shared.app, tid, cid, 5, "promo", ptr(time.Now().Add(-time.Minute)), "", "")
	wantKey(t, err, codes.InvalidArgument, "billing.credits.bad_expiry")
	_, err = command.Grant(ctx, shared.app, tid, cid, 5, "purchase", nil, "", "")
	wantKey(t, err, codes.InvalidArgument, "billing.credits.bad_source")

	// 120 comes from the promo lot (100, expires first), then 20 from the lot that never expires.
	c, err := command.Consume(ctx, shared.app, tid, cid, 120, "ai run", "k1")
	if err != nil || c.Balance != 30 || c.Replayed {
		t.Fatalf("consume = %+v, %v; want balance 30", c, err)
	}
	b, _ := query.BalanceOf(ctx, shared.app, tid, cid)
	if len(b.Lots) != 1 || b.Lots[0].Remaining != 30 || b.Lots[0].Source != "manual" || b.NextExpiry != nil {
		t.Fatalf("lots after the consume = %+v (next expiry %v); want only the 30 that never expire", b.Lots, b.NextExpiry)
	}
	// Too many: nothing is taken.
	_, err = command.Consume(ctx, shared.app, tid, cid, 31, "", "k2")
	wantKey(t, err, codes.FailedPrecondition, "billing.credits.insufficient")
	if got := balanceOf(t, tid, cid); got != 30 {
		t.Fatalf("balance after a refused consume = %d, want 30", got)
	}
	// The key makes a retry harmless.
	again, err := command.Consume(ctx, shared.app, tid, cid, 120, "ai run", "k1")
	if err != nil || !again.Replayed || again.Balance != 30 {
		t.Fatalf("retried consume = %+v, %v; want replayed, balance 30", again, err)
	}
	_, err = command.Consume(ctx, shared.app, tid, cid, 121, "", "k1")
	wantKey(t, err, codes.AlreadyExists, "billing.credits.key_reused")
	_, err = command.Consume(ctx, shared.app, tid, cid, 1, "", "")
	wantKey(t, err, codes.InvalidArgument, "billing.credits.key_required")
	_, err = command.Consume(ctx, shared.app, tid, cid, 0, "", "k3")
	wantKey(t, err, codes.InvalidArgument, "billing.credits.bad_amount")

	// The ledger holds every change, newest first, and pages.
	page1, next, err := query.Ledger(ctx, shared.app, tid, cid, 2, "")
	if err != nil || len(page1) != 2 || next == "" {
		t.Fatalf("ledger page 1 = %d entries, next %q, %v", len(page1), next, err)
	}
	page2, next2, err := query.Ledger(ctx, shared.app, tid, cid, 2, next)
	if err != nil || len(page2) != 2 || next2 != "" {
		t.Fatalf("ledger page 2 = %d entries, next %q, %v; want the last 2", len(page2), next2, err)
	}
	var sum int64
	for _, e := range append(page1, page2...) {
		sum += e.Amount
	}
	if sum != 30 { // 50 + 100 - 100 - 20
		t.Fatalf("ledger adds up to %d, want the balance 30", sum)
	}
	if _, _, err := query.Ledger(ctx, shared.app, tid, cid, 2, "garbage"); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad page token: %v", err)
	}
	// Another tenant's customer id is a different customer with nothing.
	if got := balanceOf(t, uuid.New(), cid); got != 0 {
		t.Fatalf("another tenant sees %d credits of the customer", got)
	}
}

func testConcurrentConsumesNeverOverspend(t *testing.T) {
	infra(t)
	ctx, tid, cid := context.Background(), uuid.New(), uuid.New()
	if _, err := command.Grant(ctx, shared.app, tid, cid, 100, "manual", nil, "", "seed"); err != nil {
		t.Fatal(err)
	}
	var ok, refused atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := command.Consume(ctx, shared.app, tid, cid, 10, "race", fmt.Sprintf("race-%d", i))
			switch {
			case err == nil:
				ok.Add(1)
			case errkey.Of(err) == "billing.credits.insufficient":
				refused.Add(1)
			default:
				t.Errorf("consume: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 10 || refused.Load() != 15 || balanceOf(t, tid, cid) != 0 {
		t.Fatalf("%d consumes succeeded, %d refused, balance %d; want 10, 15 and 0", ok.Load(), refused.Load(), balanceOf(t, tid, cid))
	}
}

func testExpiredCreditsAreWrittenOff(t *testing.T) {
	infra(t)
	ctx, tid, cid := context.Background(), uuid.New(), uuid.New()
	lot := uuid.New()
	// A lot that ran out an hour ago (a grant refuses a past expiry, so it is written as the system would have found it).
	if _, err := shared.system.Exec(ctx, `INSERT INTO credit_lots (tenant_id, id, customer_id, granted, remaining, source, expires_at) VALUES ($1, $2, $3, 40, 40, 'promo', now() - interval '1 hour')`, tid, lot, cid); err != nil {
		t.Fatal(err)
	}
	if _, err := command.Grant(ctx, shared.app, tid, cid, 10, "manual", nil, "", "keep"); err != nil {
		t.Fatal(err)
	}
	// Expired credits are not spendable before the sweep has run.
	if got := balanceOf(t, tid, cid); got != 10 {
		t.Fatalf("balance with an expired lot = %d, want 10", got)
	}
	_, err := command.Consume(ctx, shared.app, tid, cid, 11, "", "over")
	wantKey(t, err, codes.FailedPrecondition, "billing.credits.insufficient")

	n, err := command.ExpireDue(ctx, shared.system, 100)
	if err != nil || n < 1 {
		t.Fatalf("ExpireDue = %d, %v; want at least this lot", n, err)
	}
	var amount int64
	if err := shared.system.QueryRow(ctx, `SELECT amount FROM credit_ledger WHERE tenant_id = $1 AND lot_id = $2 AND kind = 'expire'`, tid, lot).Scan(&amount); err != nil || amount != -40 {
		t.Fatalf("expire entry = %d, %v; want -40", amount, err)
	}
	if again, _ := command.ExpireDue(ctx, shared.system, 100); again != 0 {
		t.Fatalf("a second pass expired %d lots, want 0", again)
	}
	if got := balanceOf(t, tid, cid); got != 10 {
		t.Fatalf("balance after the sweep = %d, want 10", got)
	}
}

func testAPaidPackGrantsOnce(t *testing.T) {
	infra(t)
	ctx, tid, cid := context.Background(), uuid.New(), uuid.New()
	grant := func(ref string) bool {
		var added bool
		err := pgxx.InTenantTx(ctx, shared.app, tid, func(tx pgx.Tx) error {
			var err error
			added, err = command.GrantPurchase(ctx, tx, tid, cid, 1000, ref)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return added
	}
	if !grant("link-1") || grant("link-1") {
		t.Fatal("a payment must grant its credits once, however often it is confirmed")
	}
	if !grant("link-2") {
		t.Fatal("another payment grants its own credits")
	}
	if got := balanceOf(t, tid, cid); got != 2000 {
		t.Fatalf("balance = %d, want 2000", got)
	}
}

func ptr[T any](v T) *T { return &v }
