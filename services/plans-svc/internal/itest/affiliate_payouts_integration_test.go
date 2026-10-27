//go:build integration

package itest

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/sandbox"
)

// noHold is a program that approves a commission as soon as it is earned, so that a payout can be made at once.
func noHold(p *plansv1.AffiliateProgram) { p.HoldDays = 0 }

func (w *affiliateWorld) payouts(t *testing.T, status string) []*plansv1.AffiliatePayout {
	t.Helper()
	r, err := w.e.aff.ListAffiliatePayouts(w.ctx, &plansv1.ListAffiliatePayoutsRequest{AffiliateId: w.aff.GetId(), Status: status})
	if err != nil {
		t.Fatal(err)
	}
	return r.GetPayouts()
}

func (w *affiliateWorld) createPayouts(t *testing.T) *plansv1.CreateAffiliatePayoutsResponse {
	t.Helper()
	r, err := w.e.aff.CreateAffiliatePayouts(w.ctx, &plansv1.CreateAffiliatePayoutsRequest{AffiliateId: w.aff.GetId()})
	if err != nil {
		t.Fatalf("CreateAffiliatePayouts: %v", err)
	}
	return r
}

func (w *affiliateWorld) pay(t *testing.T, id, reference string) *plansv1.AffiliatePayout {
	t.Helper()
	r, err := w.e.aff.MarkAffiliatePayoutPaid(w.ctx, &plansv1.MarkAffiliatePayoutPaidRequest{Id: id, Reference: reference})
	if err != nil {
		t.Fatalf("MarkAffiliatePayoutPaid: %v", err)
	}
	return r.GetPayout()
}

func (w *affiliateWorld) journal(t *testing.T, payout string) *plansv1.ListAffiliateJournalResponse {
	t.Helper()
	r, err := w.e.aff.ListAffiliateJournal(w.ctx, &plansv1.ListAffiliateJournalRequest{PayoutId: payout})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// balanced fails unless debits equal credits in every currency, and says what the totals are.
func balanced(t *testing.T, j *plansv1.ListAffiliateJournalResponse) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, tot := range j.GetTotals() {
		if tot.GetDebitMinor() != tot.GetCreditMinor() {
			t.Errorf("the books do not balance in %s: debits %d, credits %d", tot.GetCurrency(), tot.GetDebitMinor(), tot.GetCreditMinor())
		}
		out[tot.GetCurrency()] = tot.GetDebitMinor()
	}
	return out
}

func testAffiliatePayouts(t *testing.T) {
	e := newEnv(t)

	t.Run("a payout is netted, paid once, posted and balanced", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffPayoutCycle", noHold)
		at := time.Now()
		i1, i2 := w.newInvoice(w.cust, 10000, 0), w.newInvoice(w.cust, 20000, 0) // commissions 1000 and 2000
		w.paid(t, i1, at, "")
		w.paid(t, i2, at, "")
		if c := w.commission(t, i1); c.GetStatus() != "approved" {
			t.Fatalf("no hold: %v", c)
		}
		r := w.createPayouts(t)
		if len(r.GetPayouts()) != 1 || len(r.GetSkipped()) != 0 {
			t.Fatalf("create: %v", r)
		}
		p1 := r.GetPayouts()[0]
		if p1.GetTotalMinor() != 3000 || p1.GetCommissionCount() != 2 || p1.GetStatus() != "open" || p1.GetCurrency() != "TRY" || p1.GetFake() || p1.GetMethod() != "manual" {
			t.Fatalf("payout = %v", p1)
		}
		// A refund while the payout is open is already out of it.
		w.creditNote(t, i2, "cn_pay_1", 10000, at)
		if open := w.payouts(t, "open"); len(open) != 1 || open[0].GetTotalMinor() != 2000 {
			t.Fatalf("open payout after a refund: %v", open)
		}
		if j := w.journal(t, p1.GetId()); len(j.GetEntries()) != 0 {
			t.Fatalf("an open payout posted: %v", j)
		}
		paid := w.pay(t, p1.GetId(), "TR33 0006 1005 1978 6457 8413 26")
		if paid.GetStatus() != "paid" || paid.GetTotalMinor() != 2000 || paid.GetCommissionCount() != 2 || paid.GetPayTime() == nil || paid.GetReference() == "" {
			t.Fatalf("paid = %v", paid)
		}
		for _, inv := range []invoice{i1, i2} {
			if c := w.commission(t, inv); c.GetStatus() != "paid" || c.GetPayoutId() != p1.GetId() {
				t.Errorf("commission after the payout: %v", c)
			}
		}
		j := w.journal(t, p1.GetId())
		if len(j.GetEntries()) != 4 || balanced(t, j)["TRY"] != 4000 {
			t.Fatalf("journal = %v", j)
		}
		byAccount := map[string][2]int64{}
		for _, en := range j.GetEntries() {
			a := byAccount[en.GetAccount()]
			a[0] += en.GetDebitMinor()
			a[1] += en.GetCreditMinor()
			byAccount[en.GetAccount()] = a
			if en.GetFake() || strings.Contains(en.GetMemo(), "SANDBOX") {
				t.Errorf("a live posting is marked fake: %v", en)
			}
		}
		// The expense is debited, cash credited, the payable passes through at nothing.
		if byAccount["commission_expense"] != [2]int64{2000, 0} || byAccount["cash"] != [2]int64{0, 2000} || byAccount["commissions_payable"] != [2]int64{2000, 2000} {
			t.Fatalf("by account = %v", byAccount)
		}
		// Marking it paid again posts nothing more.
		w.pay(t, p1.GetId(), "again")
		if j := w.journal(t, p1.GetId()); len(j.GetEntries()) != 4 {
			t.Fatalf("a second MarkPaid posted again: %v", j)
		}
		if _, err := e.aff.CancelAffiliatePayout(w.ctx, &plansv1.CancelAffiliatePayoutRequest{Id: p1.GetId()}); errkey.Of(err) != "affiliate.payout.not_open" {
			t.Errorf("cancelling a paid payout: %v", err)
		}

		// The money was paid; now the rest of what the customer paid is refunded: both commissions are taken back by negative rows.
		w.voided(t, i1, at)
		w.voided(t, i2, at)
		negs := 0
		for _, inv := range []invoice{i1, i2} {
			for _, row := range w.commissions(t, inv) {
				if row.GetKind() == "reversal" {
					negs++
					if row.GetAmountMinor() != -1000 || row.GetStatus() != "approved" {
						t.Errorf("negative row = %v", row)
					}
				}
			}
		}
		if negs != 2 {
			t.Fatalf("%d negative rows, want 2", negs)
		}
		// Less than nothing is not a payout: the balance rolls forward.
		i3 := w.newInvoice(w.cust, 5000, 0) // +500
		w.paid(t, i3, at, "")
		r = w.createPayouts(t)
		if len(r.GetPayouts()) != 0 || len(r.GetSkipped()) != 1 || r.GetSkipped()[0].GetReason() != "not_positive" || r.GetSkipped()[0].GetTotalMinor() != -1500 {
			t.Fatalf("a negative balance: %v", r)
		}
		i4 := w.newInvoice(w.cust, 30000, 0) // +3000
		w.paid(t, i4, at, "")
		r = w.createPayouts(t)
		if len(r.GetPayouts()) != 1 || r.GetPayouts()[0].GetTotalMinor() != 1500 || r.GetPayouts()[0].GetCommissionCount() != 4 {
			t.Fatalf("the next payout nets the negative rows: %v", r)
		}
		p2 := w.pay(t, r.GetPayouts()[0].GetId(), "")
		if p2.GetTotalMinor() != 1500 {
			t.Fatalf("p2 = %v", p2)
		}
		// In all: earned 6500, taken back 3000, paid out 3500 in two payouts; the books say the same.
		bal, _ := e.aff.GetAffiliateBalances(w.ctx, &plansv1.GetAffiliateBalancesRequest{AffiliateId: w.aff.GetId()})
		b := bal.GetBalances()[0]
		if b.GetPaidMinor() != 3500 || b.GetApprovedMinor() != 0 || b.GetPendingMinor() != 0 {
			t.Fatalf("balance = %v", b)
		}
		all, err := e.aff.ListAffiliateJournal(w.ctx, &plansv1.ListAffiliateJournalRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if got := balanced(t, all)["TRY"]; got != 7000 { // 2 x (2000 + 1500)
			t.Fatalf("journal debits = %d", got)
		}
		if len(w.payouts(t, "paid")) != 2 {
			t.Fatalf("paid payouts: %v", w.payouts(t, ""))
		}
	})

	t.Run("a minimum, several currencies, one open payout each", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffPayoutMinimum", func(p *plansv1.AffiliateProgram) { p.HoldDays = 0; p.MinPayoutMinor = 2500 })
		at := time.Now()
		try := w.newInvoice(w.cust, 20000, 0) // 2000 TRY: under the minimum
		w.paid(t, try, at, "")
		eur := w.newInvoice(w.cust, 40000, 0)
		eur.currency = "EUR" // 4000 EUR: over it
		w.paid(t, eur, at, "")
		r := w.createPayouts(t)
		if len(r.GetPayouts()) != 1 || r.GetPayouts()[0].GetCurrency() != "EUR" || r.GetPayouts()[0].GetTotalMinor() != 4000 {
			t.Fatalf("payouts = %v", r.GetPayouts())
		}
		if len(r.GetSkipped()) != 1 || r.GetSkipped()[0].GetReason() != "below_minimum" || r.GetSkipped()[0].GetCurrency() != "TRY" {
			t.Fatalf("skipped = %v", r.GetSkipped())
		}
		// The TRY balance grows past the minimum, and a new EUR commission joins the EUR payout that is still open.
		more := w.newInvoice(w.cust, 10000, 0) // +1000 TRY: 3000 now
		w.paid(t, more, at, "")
		moreEur := w.newInvoice(w.cust, 10000, 0)
		moreEur.currency = "EUR"
		w.paid(t, moreEur, at, "")
		r = w.createPayouts(t)
		if len(r.GetPayouts()) != 2 || len(r.GetSkipped()) != 0 {
			t.Fatalf("second round: %v", r)
		}
		open := w.payouts(t, "open")
		if len(open) != 2 {
			t.Fatalf("open payouts: %v", open)
		}
		for _, p := range open {
			switch p.GetCurrency() {
			case "EUR":
				if p.GetTotalMinor() != 5000 || p.GetCommissionCount() != 2 || p.GetId() != r.GetPayouts()[0].GetId() && p.GetId() != r.GetPayouts()[1].GetId() {
					t.Errorf("EUR payout = %v", p)
				}
			case "TRY":
				if p.GetTotalMinor() != 3000 {
					t.Errorf("TRY payout = %v", p)
				}
			}
		}
		for _, p := range open {
			w.pay(t, p.GetId(), "")
		}
		got := balanced(t, w.journalAll(t))
		if got["EUR"] != 10000 || got["TRY"] != 6000 {
			t.Fatalf("totals per currency = %v: they are never added together", got)
		}
	})

	t.Run("a suspended partner is not paid, and keeps the balance", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffPayoutSuspended", noHold)
		inv := w.newInvoice(w.cust, 10000, 0)
		w.paid(t, inv, time.Now(), "")
		p := w.createPayouts(t).GetPayouts()[0]
		suspend := func(status string) {
			if _, err := e.aff.UpdateAffiliate(w.ctx, &plansv1.UpdateAffiliateRequest{Id: w.aff.GetId(), Status: status}); err != nil {
				t.Fatal(err)
			}
		}
		suspend("suspended")
		_, err := e.aff.MarkAffiliatePayoutPaid(w.ctx, &plansv1.MarkAffiliatePayoutPaidRequest{Id: p.GetId()})
		if errkey.Of(err) != "affiliate.payout.affiliate_inactive" || code(t, err) != codes.FailedPrecondition {
			t.Fatalf("paying a suspended partner: %v", err)
		}
		_, err = e.aff.CreateAffiliatePayouts(w.ctx, &plansv1.CreateAffiliatePayoutsRequest{AffiliateId: w.aff.GetId()})
		if errkey.Of(err) != "affiliate.payout.affiliate_inactive" {
			t.Fatalf("batching a suspended partner: %v", err)
		}
		// A bulk run leaves them out without a word.
		if r, err := e.aff.CreateAffiliatePayouts(w.ctx, &plansv1.CreateAffiliatePayoutsRequest{}); err != nil || len(r.GetSkipped()) != 0 {
			t.Fatalf("bulk: %v %v", r, err)
		}
		bal, _ := e.aff.GetAffiliateBalances(w.ctx, &plansv1.GetAffiliateBalancesRequest{AffiliateId: w.aff.GetId()})
		if bal.GetBalances()[0].GetApprovedMinor() != 1000 {
			t.Fatalf("the approved balance was not kept: %v", bal)
		}
		suspend("active")
		if paid := w.pay(t, p.GetId(), ""); paid.GetTotalMinor() != 1000 {
			t.Fatalf("reinstated: %v", paid)
		}
	})

	t.Run("cancelling releases the rows; a payout that came to nothing is cancelled, not paid", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffPayoutCancel", noHold)
		inv := w.newInvoice(w.cust, 10000, 0)
		w.paid(t, inv, time.Now(), "")
		p := w.createPayouts(t).GetPayouts()[0]
		// Everything is refunded before the owner pays.
		w.voided(t, inv, time.Now())
		_, err := e.aff.MarkAffiliatePayoutPaid(w.ctx, &plansv1.MarkAffiliatePayoutPaidRequest{Id: p.GetId()})
		if errkey.Of(err) != "affiliate.payout.nothing_to_pay" {
			t.Fatalf("paying a payout of nothing: %v", err)
		}
		c, err := e.aff.CancelAffiliatePayout(w.ctx, &plansv1.CancelAffiliatePayoutRequest{Id: p.GetId()})
		if err != nil || c.GetPayout().GetStatus() != "cancelled" || c.GetPayout().GetCancelTime() == nil {
			t.Fatalf("cancel: %v %v", c, err)
		}
		if _, err := e.aff.MarkAffiliatePayoutPaid(w.ctx, &plansv1.MarkAffiliatePayoutPaidRequest{Id: p.GetId()}); errkey.Of(err) != "affiliate.payout.not_open" {
			t.Errorf("paying a cancelled payout: %v", err)
		}
		if _, err := e.aff.CancelAffiliatePayout(w.ctx, &plansv1.CancelAffiliatePayoutRequest{Id: p.GetId()}); err != nil {
			t.Errorf("cancelling twice: %v", err)
		}
		// A live commission whose payout is cancelled is payable again.
		w2 := newAffiliateWorld(t, e, "AffPayoutCancel2", noHold)
		inv2 := w2.newInvoice(w2.cust, 10000, 0)
		w2.paid(t, inv2, time.Now(), "")
		p2 := w2.createPayouts(t).GetPayouts()[0]
		if _, err := e.aff.CancelAffiliatePayout(w2.ctx, &plansv1.CancelAffiliatePayoutRequest{Id: p2.GetId()}); err != nil {
			t.Fatal(err)
		}
		if c := w2.commission(t, inv2); c.GetPayoutId() != "" || c.GetStatus() != "approved" {
			t.Fatalf("the commission stayed in the cancelled payout: %v", c)
		}
		again := w2.createPayouts(t)
		if len(again.GetPayouts()) != 1 || again.GetPayouts()[0].GetId() == p2.GetId() || again.GetPayouts()[0].GetTotalMinor() != 1000 {
			t.Fatalf("a new payout: %v", again)
		}
		if _, err := e.aff.MarkAffiliatePayoutPaid(w2.ctx, &plansv1.MarkAffiliatePayoutPaidRequest{Id: uuid.NewString()}); errkey.Of(err) != "affiliate.payout.not_found" {
			t.Errorf("an unknown payout: %v", err)
		}
	})

	t.Run("two payouts cannot take the same commission, and a payout is posted once however often it is paid", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffPayoutRace", noHold)
		for range 5 {
			w.paid(t, w.newInvoice(w.cust, 10000, 0), time.Now(), "")
		}
		var wg sync.WaitGroup
		results := make([]*plansv1.CreateAffiliatePayoutsResponse, 6)
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := e.aff.CreateAffiliatePayouts(w.ctx, &plansv1.CreateAffiliatePayoutsRequest{AffiliateId: w.aff.GetId()})
				if err != nil {
					t.Error(err)
					return
				}
				results[i] = r
			}()
		}
		wg.Wait()
		ids := map[string]bool{}
		for _, r := range results {
			for _, p := range r.GetPayouts() {
				ids[p.GetId()] = true
			}
		}
		open := w.payouts(t, "open")
		if len(open) != 1 || open[0].GetTotalMinor() != 5000 || open[0].GetCommissionCount() != 5 || len(ids) != 1 {
			t.Fatalf("open payouts after 6 parallel creates: %v (ids %v)", open, ids)
		}
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := e.aff.MarkAffiliatePayoutPaid(w.ctx, &plansv1.MarkAffiliatePayoutPaidRequest{Id: open[0].GetId()}); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if j := w.journal(t, open[0].GetId()); len(j.GetEntries()) != 4 || balanced(t, j)["TRY"] != 10000 {
			t.Fatalf("journal after 5 parallel payments: %v", j)
		}
	})

	t.Run("sandbox payouts are fake and say so", func(t *testing.T) {
		w := newAffiliateWorldIn(t, e, "AffPayoutSandbox", plansv1.Environment_ENVIRONMENT_SANDBOX, noHold)
		w.paid(t, w.newInvoice(w.cust, 10000, 0), time.Now(), "")
		p := w.createPayouts(t).GetPayouts()[0]
		if !p.GetFake() {
			t.Fatalf("a sandbox payout is not marked fake: %v", p)
		}
		paid := w.pay(t, p.GetId(), "")
		if !paid.GetFake() || paid.GetStatus() != "paid" {
			t.Fatalf("paid = %v", paid)
		}
		j := w.journal(t, p.GetId())
		if len(j.GetEntries()) != 4 {
			t.Fatalf("journal = %v", j)
		}
		for _, en := range j.GetEntries() {
			if !en.GetFake() || !strings.HasPrefix(en.GetMemo(), "SANDBOX (fake)") {
				t.Errorf("a sandbox posting is not labelled: %v", en)
			}
		}
		balanced(t, j)

		// Resetting the sandbox empties the payouts and their postings with the commissions they describe; the partners and the program stay.
		if _, err := sandbox.NewLocal(e.places).Reset(context.Background(), uuid.MustParse(w.tenantID)); err != nil {
			t.Fatal(err)
		}
		if got := w.payouts(t, ""); len(got) != 0 {
			t.Errorf("payouts after a reset: %v", got)
		}
		if got := w.journalAll(t); got.GetTotal() != 0 {
			t.Errorf("journal after a reset: %v", got)
		}
		if _, err := e.aff.GetAffiliate(w.ctx, &plansv1.GetAffiliateRequest{Id: w.aff.GetId()}); err != nil {
			t.Errorf("the partner was reset: %v", err)
		}
	})

	t.Run("the journal of a live tenant is never rewritten, not even by the system role", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffPayoutLiveJournal", noHold)
		w.paid(t, w.newInvoice(w.cust, 10000, 0), time.Now(), "")
		w.pay(t, w.createPayouts(t).GetPayouts()[0].GetId(), "")
		for _, stmt := range []string{`DELETE FROM affiliate_journal WHERE tenant_id = $1`, `UPDATE affiliate_journal SET memo = 'x' WHERE tenant_id = $1`} {
			// A DELETE is refused by the trigger, an UPDATE by the missing grant: either way nothing changes.
			if err := e.exec(context.Background(), w.tenantID, stmt, uuid.MustParse(w.tenantID)); err == nil || !(strings.Contains(err.Error(), "append-only") || pgxx.ErrCode(err) == pgxx.CodeInsufficientPriv) {
				t.Errorf("%s: %v", stmt, err)
			}
		}
		if j := w.journalAll(t); len(j.GetEntries()) != 4 {
			t.Errorf("journal = %v", j)
		}
	})

	t.Run("a live environment that is not active pays nobody", func(t *testing.T) {
		acct := e.inactiveAccount(t, "AffPayoutInactive")
		live := acct[plansv1.Environment_ENVIRONMENT_LIVE]
		w := startAffiliateWorld(t, e, live, noHold)
		w.paid(t, w.newInvoice(w.cust, 10000, 0), time.Now(), "")
		_, err := e.aff.CreateAffiliatePayouts(w.ctx, &plansv1.CreateAffiliatePayoutsRequest{})
		if errkey.Of(err) != "affiliate.payout.tenant_not_active" || code(t, err) != codes.FailedPrecondition {
			t.Fatalf("before the go-live: %v", err)
		}
		e.activate(t, live.GetId())
		p := w.createPayouts(t).GetPayouts()[0]
		// Suspended by an operator after the payout was made: it is not paid either.
		if _, err := e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: live.GetId(), Status: plansv1.TenantStatus_TENANT_STATUS_SUSPENDED, Reason: "itest"}); err != nil {
			t.Fatal(err)
		}
		_, err = e.aff.MarkAffiliatePayoutPaid(w.ctx, &plansv1.MarkAffiliatePayoutPaidRequest{Id: p.GetId()})
		if errkey.Of(err) != "affiliate.payout.tenant_not_active" {
			t.Fatalf("a suspended tenant pays: %v", err)
		}
		e.activate(t, live.GetId())
		w.pay(t, p.GetId(), "")
	})

	t.Run("the database refuses postings that do not balance", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffPayoutBooks", noHold)
		w.paid(t, w.newInvoice(w.cust, 10000, 0), time.Now(), "")
		p := w.pay(t, w.createPayouts(t).GetPayouts()[0].GetId(), "")
		tid := uuid.MustParse(w.tenantID)
		insert := func(lines ...[2]int64) error {
			return pgxx.InTenantTx(context.Background(), e.appDB, tid, func(tx pgx.Tx) error {
				for _, l := range lines {
					if _, err := tx.Exec(context.Background(), `INSERT INTO affiliate_journal (tenant_id, payout_id, affiliate_id, posted_at, currency, account, debit_minor, credit_minor, sandbox)
						VALUES (app_tenant_id(), $1, $2, now(), 'TRY', 'cash', $3, $4, false)`, uuid.MustParse(p.GetId()), uuid.MustParse(w.aff.GetId()), l[0], l[1]); err != nil {
						return err
					}
				}
				return nil
			})
		}
		if err := insert([2]int64{100, 0}); err == nil || !strings.Contains(err.Error(), "must balance") {
			t.Fatalf("a lone debit was accepted: %v", err)
		}
		if err := insert([2]int64{100, 0}, [2]int64{0, 99}); err == nil {
			t.Fatal("lines that differ by one were accepted")
		}
		if err := insert([2]int64{0, 100}, [2]int64{100, 0}); err != nil {
			t.Fatalf("lines in any order that balance: %v", err)
		}
		if err := insert([2]int64{0, 0}); err == nil {
			t.Fatal("a line of nothing was accepted")
		}
		// Append-only for the application role.
		err := pgxx.InTenantTx(context.Background(), e.appDB, tid, func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), `UPDATE affiliate_journal SET debit_minor = debit_minor`)
			return err
		})
		if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
			t.Fatalf("an UPDATE of the journal: %v", err)
		}
		err = pgxx.InTenantTx(context.Background(), e.appDB, tid, func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), `DELETE FROM affiliate_journal`)
			return err
		})
		if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
			t.Fatalf("a DELETE of the journal: %v", err)
		}
	})

	t.Run("payouts and the journal are the tenant's own", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffPayoutIsolation", noHold)
		w.paid(t, w.newInvoice(w.cust, 10000, 0), time.Now(), "")
		p := w.pay(t, w.createPayouts(t).GetPayouts()[0].GetId(), "")
		other := as(e.account(t, "AffPayoutIsolationOther")[plansv1.Environment_ENVIRONMENT_LIVE].GetId())
		if r, err := e.aff.ListAffiliatePayouts(other, &plansv1.ListAffiliatePayoutsRequest{}); err != nil || r.GetTotal() != 0 {
			t.Errorf("another tenant lists payouts: %v %v", r, err)
		}
		if r, err := e.aff.ListAffiliateJournal(other, &plansv1.ListAffiliateJournalRequest{}); err != nil || r.GetTotal() != 0 {
			t.Errorf("another tenant reads the journal: %v %v", r, err)
		}
		if _, err := e.aff.MarkAffiliatePayoutPaid(other, &plansv1.MarkAffiliatePayoutPaidRequest{Id: p.GetId()}); errkey.Of(err) != "affiliate.payout.not_found" {
			t.Errorf("another tenant pays: %v", err)
		}
		if _, err := e.aff.CancelAffiliatePayout(other, &plansv1.CancelAffiliatePayoutRequest{Id: p.GetId()}); errkey.Of(err) != "affiliate.payout.not_found" {
			t.Errorf("another tenant cancels: %v", err)
		}
	})
}

// journalAll is the whole journal of the world's tenant.
func (w *affiliateWorld) journalAll(t *testing.T) *plansv1.ListAffiliateJournalResponse {
	t.Helper()
	return w.journal(t, "")
}
