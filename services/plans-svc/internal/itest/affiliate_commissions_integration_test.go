//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	affiliatescmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/command"
)

// affiliateWorld is a live tenant with an active 10 % program (hold 30 days), a partner, and a customer the partner brought.
type affiliateWorld struct {
	e        *env
	ctx      context.Context
	tenantID string
	aff      *plansv1.Affiliate
	cust     *plansv1.Customer
}

func newAffiliateWorld(t *testing.T, e *env, name string, program func(p *plansv1.AffiliateProgram)) *affiliateWorld {
	t.Helper()
	live := e.account(t, name)[plansv1.Environment_ENVIRONMENT_LIVE]
	w := &affiliateWorld{e: e, ctx: as(live.GetId()), tenantID: live.GetId()}
	activeProgram(t, e, w.ctx, program)
	w.aff = newAffiliate(t, e, w.ctx, uniq("p")+"@partners.test")
	w.cust = w.customer(t)
	return w
}

// customer creates a customer brought by the partner.
func (w *affiliateWorld) customer(t *testing.T) *plansv1.Customer {
	t.Helper()
	ext := uniq("cus_")
	r, err := w.e.plans.CreateCustomer(w.ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@buyers.test", ReferralCode: w.aff.GetCode()})
	if err != nil {
		t.Fatal(err)
	}
	return r.GetCustomer()
}

// invoice is what billing-svc announces about an invoice.
type invoice struct {
	id          uuid.UUID
	customer    string
	number      string
	currency    string
	total, tax  int64
	environment string
	omitNet     bool
}

func (w *affiliateWorld) newInvoice(c *plansv1.Customer, total, tax int64) invoice {
	return invoice{id: uuid.New(), customer: c.GetId(), number: "INV-" + uniq(""), currency: "TRY", total: total, tax: tax, environment: "live"}
}

func (w *affiliateWorld) send(t *testing.T, typ string, data map[string]any, at time.Time, eventID string) {
	t.Helper()
	b, _ := json.Marshal(data)
	if eventID == "" {
		eventID = uuid.NewString()
	}
	if _, err := w.e.hooks.RecordTenantEvent(w.ctx, &plansv1.RecordTenantEventRequest{EventId: eventID, Type: typ, DataJson: string(b), OccurredTime: timestamppb.New(at)}); err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
}

// paid announces that the invoice was paid, as billing-svc does (amounts are strings).
func (w *affiliateWorld) paid(t *testing.T, inv invoice, at time.Time, eventID string) {
	t.Helper()
	d := map[string]any{"invoice_id": inv.id.String(), "number": inv.number, "customer_id": inv.customer, "currency": inv.currency, "total_minor": strconv.FormatInt(inv.total, 10),
		"status": "paid", "environment": inv.environment}
	if !inv.omitNet {
		d["tax_minor"], d["net_minor"] = strconv.FormatInt(inv.tax, 10), strconv.FormatInt(inv.total-inv.tax, 10)
	}
	w.send(t, "invoice.paid", d, at, eventID)
}

func (w *affiliateWorld) voided(t *testing.T, inv invoice, at time.Time) {
	t.Helper()
	w.send(t, "invoice.voided", map[string]any{"invoice_id": inv.id.String(), "customer_id": inv.customer, "currency": inv.currency, "total_minor": strconv.FormatInt(inv.total, 10),
		"status": "void", "environment": inv.environment}, at, "")
}

func (w *affiliateWorld) creditNote(t *testing.T, inv invoice, id string, amount int64, at time.Time) {
	t.Helper()
	w.send(t, "credit_note.created", map[string]any{"credit_note_id": id, "invoice_id": inv.id.String(), "customer_id": inv.customer, "currency": inv.currency,
		"total_minor": strconv.FormatInt(amount, 10), "disposition": "refund", "environment": inv.environment}, at, "")
}

// commissions lists the rows of an invoice (the commission and its reversal rows), oldest first.
func (w *affiliateWorld) commissions(t *testing.T, inv invoice) []*plansv1.AffiliateCommission {
	t.Helper()
	r, err := w.e.aff.ListAffiliateCommissions(w.ctx, &plansv1.ListAffiliateCommissionsRequest{InvoiceId: inv.id.String()})
	if err != nil {
		t.Fatal(err)
	}
	out := r.GetCommissions()
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (w *affiliateWorld) commission(t *testing.T, inv invoice) *plansv1.AffiliateCommission {
	t.Helper()
	rows := w.commissions(t, inv)
	if len(rows) == 0 || rows[0].GetKind() != "commission" {
		t.Fatalf("no commission for the invoice: %v", rows)
	}
	return rows[0]
}

func (w *affiliateWorld) setStatus(t *testing.T, inv invoice, status string) {
	t.Helper()
	q := `UPDATE affiliate_commissions SET status = $2 WHERE tenant_id = $3::uuid AND invoice_id = $1 AND kind = 'commission'`
	if status == "paid" {
		q = `UPDATE affiliate_commissions SET status = $2, paid_at = now(), approved_at = COALESCE(approved_at, now()) WHERE tenant_id = $3::uuid AND invoice_id = $1 AND kind = 'commission'`
	}
	if status == "approved" {
		q = `UPDATE affiliate_commissions SET status = $2, approved_at = now() WHERE tenant_id = $3::uuid AND invoice_id = $1 AND kind = 'commission'`
	}
	if _, err := w.e.system.Exec(context.Background(), q, inv.id, status, w.tenantID); err != nil {
		t.Fatal(err)
	}
}

func testAffiliateCommissions(t *testing.T) {
	e := newEnv(t)

	t.Run("replaying invoice.paid creates one commission, on the net amount", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffCommission", nil)
		inv := w.newInvoice(w.cust, 12000, 2000) // 100.00 net + 20.00 tax
		at := time.Now().Truncate(time.Second)
		id := uuid.NewString()
		w.paid(t, inv, at, id)
		w.paid(t, inv, at, id)                // the same event again
		w.paid(t, inv, at, "")                // the same invoice announced under another event id
		w.paid(t, inv, at.Add(time.Hour), "") // and again, later
		rows := w.commissions(t, inv)
		if len(rows) != 1 {
			t.Fatalf("%d rows after replays, want 1: %v", len(rows), rows)
		}
		c := rows[0]
		if c.GetAmountMinor() != 1000 || c.GetNetMinor() != 10000 || c.GetInvoiceTotalMinor() != 12000 || c.GetCurrency() != "TRY" || c.GetStatus() != "pending" ||
			c.GetAffiliateId() != w.aff.GetId() || c.GetCustomerExternalId() != w.cust.GetExternalId() || c.GetInvoiceNumber() != inv.number {
			t.Fatalf("commission = %v", c)
		}
		if got := c.GetApproveAfterTime().AsTime(); !got.Equal(at.AddDate(0, 0, 30)) {
			t.Fatalf("approve after %v, want %v: the hold is 30 days", got, at.AddDate(0, 0, 30))
		}
		// An old billing-svc sends no net: the total less the tax is the base.
		old := w.newInvoice(w.cust, 6000, 1000)
		old.omitNet = true
		w.paid(t, old, at, "")
		if c := w.commission(t, old); c.GetNetMinor() != 6000 || c.GetAmountMinor() != 600 {
			t.Errorf("without a net in the event: %v", c)
		}
	})

	t.Run("20 parallel announcements of one invoice make one commission", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffParallel", nil)
		inv := w.newInvoice(w.cust, 5000, 0)
		var wg sync.WaitGroup
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				b, _ := json.Marshal(map[string]any{"invoice_id": inv.id.String(), "customer_id": inv.customer, "currency": "TRY", "total_minor": "5000", "net_minor": "5000", "tax_minor": "0"})
				if _, err := e.hooks.RecordTenantEvent(w.ctx, &plansv1.RecordTenantEventRequest{EventId: uuid.NewString(), Type: "invoice.paid", DataJson: string(b)}); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if rows := w.commissions(t, inv); len(rows) != 1 || rows[0].GetAmountMinor() != 500 {
			t.Fatalf("rows = %v", rows)
		}
	})

	t.Run("the hold is respected, then a job approves", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffHold", func(p *plansv1.AffiliateProgram) { p.HoldDays = 14 })
		inv := w.newInvoice(w.cust, 10000, 0)
		at := time.Now().Add(-10 * 24 * time.Hour)
		w.paid(t, inv, at, "")
		job := func(now time.Time) int64 {
			t.Helper()
			n, err := affiliatescmd.NewApprover(e.system, time.Hour, slog.Default()).WithClock(func() time.Time { return now }).Once(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
		job(at.AddDate(0, 0, 13)) // other tests' commissions may be due; ours is not
		if c := w.commission(t, inv); c.GetStatus() != "pending" {
			t.Fatalf("approved inside the hold: %v", c)
		}
		job(at.AddDate(0, 0, 14).Add(time.Second))
		c := w.commission(t, inv)
		if c.GetStatus() != "approved" || c.GetApproveTime() == nil {
			t.Fatalf("not approved after the hold: %v", c)
		}
		if n := job(at.AddDate(0, 0, 40)); n < 0 {
			t.Fatal("unreachable")
		}
		// A program without a hold approves at once.
		w2 := newAffiliateWorld(t, e, "AffNoHold", func(p *plansv1.AffiliateProgram) { p.HoldDays = 0 })
		inv2 := w2.newInvoice(w2.cust, 10000, 0)
		w2.paid(t, inv2, time.Now(), "")
		if c := w2.commission(t, inv2); c.GetStatus() != "approved" {
			t.Fatalf("hold 0: %v", c)
		}
	})

	t.Run("nothing is earned by an unattributed customer, a suspended partner, a paused program or the wrong environment", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffNoEarn", nil)
		at := time.Now()
		// A customer nobody brought.
		stranger, err := e.plans.CreateCustomer(w.ctx, &plansv1.CreateCustomerRequest{ExternalId: uniq("cus_s"), Name: "S", Email: uniq("s") + "@buyers.test"})
		if err != nil {
			t.Fatal(err)
		}
		inv := w.newInvoice(stranger.GetCustomer(), 5000, 0)
		w.paid(t, inv, at, "")
		if rows := w.commissions(t, inv); len(rows) != 0 {
			t.Errorf("an unattributed customer earned: %v", rows)
		}
		// The environment of the event is not the tenant's.
		wrong := w.newInvoice(w.cust, 5000, 0)
		wrong.environment = "sandbox"
		w.paid(t, wrong, at, "")
		if rows := w.commissions(t, wrong); len(rows) != 0 {
			t.Errorf("a sandbox event earned in live: %v", rows)
		}
		// Suspended: nothing new, and reinstating does not bring back what passed.
		if _, err := e.aff.UpdateAffiliate(w.ctx, &plansv1.UpdateAffiliateRequest{Id: w.aff.GetId(), Status: "suspended"}); err != nil {
			t.Fatal(err)
		}
		sus := w.newInvoice(w.cust, 5000, 0)
		w.paid(t, sus, at, "")
		if rows := w.commissions(t, sus); len(rows) != 0 {
			t.Errorf("a suspended partner earned: %v", rows)
		}
		if _, err := e.aff.UpdateAffiliate(w.ctx, &plansv1.UpdateAffiliateRequest{Id: w.aff.GetId(), Status: "active"}); err != nil {
			t.Fatal(err)
		}
		// Paused.
		activeProgram(t, e, w.ctx, func(p *plansv1.AffiliateProgram) { p.Status = "paused" })
		paused := w.newInvoice(w.cust, 5000, 0)
		w.paid(t, paused, at, "")
		if rows := w.commissions(t, paused); len(rows) != 0 {
			t.Errorf("a paused program paid: %v", rows)
		}
		activeProgram(t, e, w.ctx, nil)
		ok := w.newInvoice(w.cust, 5000, 0)
		w.paid(t, ok, at, "")
		if rows := w.commissions(t, ok); len(rows) != 1 {
			t.Errorf("an active program did not pay: %v", rows)
		}
		// Nothing but tax is nothing.
		taxOnly := w.newInvoice(w.cust, 2000, 2000)
		w.paid(t, taxOnly, at, "")
		if rows := w.commissions(t, taxOnly); len(rows) != 0 {
			t.Errorf("tax earned a commission: %v", rows)
		}
		// Garbage is ignored, not retried for ever.
		w.send(t, "invoice.paid", map[string]any{"invoice_id": "not-a-uuid"}, at, "")
		w.send(t, "invoice.paid", map[string]any{"invoice_id": uuid.NewString(), "customer_id": "nope"}, at, "")
	})

	t.Run("a fixed commission is in its currency only, and a term runs out", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffFixed", func(p *plansv1.AffiliateProgram) {
			p.CommissionKind, p.CommissionValue, p.Currency = "fixed", 750, "EUR"
			p.CommissionTerm, p.CommissionTermValue = "first_invoices", 2
		})
		at := time.Now()
		try := func(currency string) []*plansv1.AffiliateCommission {
			inv := w.newInvoice(w.cust, 9000, 0)
			inv.currency = currency
			w.paid(t, inv, at, "")
			return w.commissions(t, inv)
		}
		if rows := try("TRY"); len(rows) != 0 {
			t.Errorf("a fixed EUR commission on a TRY invoice: %v", rows)
		}
		if rows := try("EUR"); len(rows) != 1 || rows[0].GetAmountMinor() != 750 || rows[0].GetCurrency() != "EUR" {
			t.Errorf("first EUR invoice: %v", rows)
		}
		if rows := try("EUR"); len(rows) != 1 {
			t.Errorf("second EUR invoice: %v", rows)
		}
		if rows := try("EUR"); len(rows) != 0 {
			t.Errorf("the third invoice is past the term of 2: %v", rows)
		}
		// A percentage follows the currency of the invoice.
		pct := newAffiliateWorld(t, e, "AffPercentFX", nil)
		inv := pct.newInvoice(pct.cust, 20000, 0)
		inv.currency = "USD"
		pct.paid(t, inv, at, "")
		if c := pct.commission(t, inv); c.GetCurrency() != "USD" || c.GetAmountMinor() != 2000 {
			t.Errorf("percent in USD: %v", c)
		}
	})

	t.Run("a credit note takes back in proportion, once per note; a void takes back the rest", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffCredit", nil)
		inv := w.newInvoice(w.cust, 12000, 2000) // commission 1000
		at := time.Now()
		w.paid(t, inv, at, "")
		w.creditNote(t, inv, "cn_1", 3000, at) // a quarter of the invoice
		c := w.commission(t, inv)
		if c.GetReversedMinor() != 250 || c.GetStatus() != "pending" {
			t.Fatalf("after a quarter: %v", c)
		}
		w.creditNote(t, inv, "cn_1", 3000, at) // the same credit note again
		if c := w.commission(t, inv); c.GetReversedMinor() != 250 {
			t.Fatalf("a repeated credit note took back again: %v", c)
		}
		w.creditNote(t, inv, "cn_2", 3000, at)
		if c := w.commission(t, inv); c.GetReversedMinor() != 500 {
			t.Fatalf("after a half: %v", c)
		}
		w.voided(t, inv, at)
		c = w.commission(t, inv)
		if c.GetReversedMinor() != 1000 || c.GetStatus() != "reversed" {
			t.Fatalf("after the void: %v", c)
		}
		w.voided(t, inv, at)
		w.creditNote(t, inv, "cn_3", 6000, at)
		if c := w.commission(t, inv); c.GetReversedMinor() != 1000 || c.GetStatus() != "reversed" {
			t.Fatalf("reversing a reversed commission: %v", c)
		}
		if rows := w.commissions(t, inv); len(rows) != 1 {
			t.Fatalf("an unpaid commission is reduced in place, not given extra rows: %v", rows)
		}
		// A credit note against an invoice that earned nothing is nothing.
		other := w.newInvoice(w.cust, 1000, 0)
		w.creditNote(t, other, "cn_x", 500, at)
		w.voided(t, other, at)
	})

	t.Run("a reversal after the payout is a negative row that the next payout nets", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffAfterPayout", nil)
		inv := w.newInvoice(w.cust, 10000, 0) // commission 1000
		at := time.Now()
		w.paid(t, inv, at, "")
		w.setStatus(t, inv, "paid")
		w.creditNote(t, inv, "cn_p1", 5000, at)
		rows := w.commissions(t, inv)
		if len(rows) != 2 || rows[0].GetKind() != "commission" || rows[1].GetKind() != "reversal" {
			t.Fatalf("rows = %v", rows)
		}
		neg := rows[1]
		if neg.GetAmountMinor() != -500 || neg.GetStatus() != "approved" || neg.GetParentId() != rows[0].GetId() || neg.GetPayoutId() != "" {
			t.Fatalf("negative row = %v", neg)
		}
		if rows[0].GetStatus() != "paid" || rows[0].GetReversedMinor() != 500 {
			t.Fatalf("the paid commission = %v", rows[0])
		}
		w.creditNote(t, inv, "cn_p1", 5000, at) // repeat: no second negative row
		w.voided(t, inv, at)                    // the void takes back the other half
		rows = w.commissions(t, inv)
		if len(rows) != 3 || rows[2].GetAmountMinor() != -500 {
			t.Fatalf("rows after the void = %v", rows)
		}
		bal, err := e.aff.GetAffiliateBalances(w.ctx, &plansv1.GetAffiliateBalancesRequest{AffiliateId: w.aff.GetId()})
		if err != nil || len(bal.GetBalances()) != 1 {
			t.Fatalf("balances: %v %v", bal, err)
		}
		b := bal.GetBalances()[0]
		// Paid out 1000, taken back 1000 in two negative rows that wait: 0 available - 1000.
		if b.GetPaidMinor() != 1000 || b.GetApprovedMinor() != -1000 || b.GetReversedMinor() != 1000 || b.GetPendingMinor() != 0 {
			t.Fatalf("balance = %v", b)
		}
	})

	t.Run("a chargeback and a manual reversal, each once", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffManual", nil)
		inv := w.newInvoice(w.cust, 10000, 0)
		w.paid(t, inv, time.Now(), "")
		reverse := func(req *plansv1.ReverseAffiliateCommissionRequest) (*plansv1.ReverseAffiliateCommissionResponse, error) {
			req.InvoiceId = inv.id.String()
			return e.aff.ReverseAffiliateCommission(w.ctx, req)
		}
		if _, err := reverse(&plansv1.ReverseAffiliateCommissionRequest{Reason: "manual"}); code(t, err) != codes.InvalidArgument {
			t.Errorf("a manual reversal without a reference: %v", err)
		}
		if _, err := reverse(&plansv1.ReverseAffiliateCommissionRequest{Reason: "refund"}); code(t, err) != codes.InvalidArgument {
			t.Errorf("an unknown reason: %v", err)
		}
		r, err := reverse(&plansv1.ReverseAffiliateCommissionRequest{Reason: "manual", Reference: "ticket-1", AmountMinor: 300, Note: "goodwill"})
		if err != nil || r.GetReversedMinor() != 300 || r.GetCommission().GetReversedMinor() != 300 {
			t.Fatalf("manual: %v %v", r, err)
		}
		if r, err := reverse(&plansv1.ReverseAffiliateCommissionRequest{Reason: "manual", Reference: "ticket-1", AmountMinor: 300}); err != nil || r.GetReversedMinor() != 0 {
			t.Fatalf("the same manual reversal again: %v %v", r, err)
		}
		r, err = reverse(&plansv1.ReverseAffiliateCommissionRequest{Reason: "chargeback"})
		if err != nil || r.GetReversedMinor() != 700 || r.GetCommission().GetStatus() != "reversed" {
			t.Fatalf("chargeback takes the rest: %v %v", r, err)
		}
		if r, err := reverse(&plansv1.ReverseAffiliateCommissionRequest{Reason: "chargeback"}); err != nil || r.GetReversedMinor() != 0 {
			t.Fatalf("the same chargeback again: %v %v", r, err)
		}
		_, err = e.aff.ReverseAffiliateCommission(w.ctx, &plansv1.ReverseAffiliateCommissionRequest{InvoiceId: uuid.NewString(), Reason: "chargeback"})
		if errkey.Of(err) != "affiliate.commission.not_found" {
			t.Errorf("an invoice with no commission: %v", err)
		}
	})

	t.Run("filters, balances and the funnel", func(t *testing.T) {
		w := newAffiliateWorld(t, e, "AffFunnel", nil)
		for range 3 {
			if _, err := e.pub.TrackAffiliateClick(context.Background(), &plansv1.TrackAffiliateClickRequest{Code: w.aff.GetCode()}); err != nil {
				t.Fatal(err)
			}
		}
		second := w.customer(t)
		now := time.Now()
		a, b := w.newInvoice(w.cust, 10000, 0), w.newInvoice(second, 20000, 0)
		w.paid(t, a, now, "")
		w.paid(t, b, now, "")
		w.creditNote(t, b, "cn_f", 10000, now)
		f, err := e.aff.GetAffiliateFunnel(w.ctx, &plansv1.GetAffiliateFunnelRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if f.GetClicks() != 3 || f.GetReferrals() != 2 || f.GetPayingCustomers() != 2 || len(f.GetCommissions()) != 1 || f.GetCommissions()[0].GetEarnedMinor() != 3000 ||
			f.GetCommissions()[0].GetReversedMinor() != 1000 || f.GetCommissions()[0].GetCurrency() != "TRY" {
			t.Fatalf("funnel = %v", f)
		}
		if _, err := e.aff.GetAffiliateFunnel(w.ctx, &plansv1.GetAffiliateFunnelRequest{FromDate: "2026-02-30"}); errkey.Of(err) != "affiliate.bad_period" {
			t.Errorf("a bad date: %v", err)
		}
		old, err := e.aff.GetAffiliateFunnel(w.ctx, &plansv1.GetAffiliateFunnelRequest{FromDate: "2020-01-01", ToDate: "2020-01-31"})
		if err != nil || old.GetClicks() != 0 || old.GetReferrals() != 0 || len(old.GetCommissions()) != 0 {
			t.Errorf("an empty period: %v %v", old, err)
		}
		bal, _ := e.aff.GetAffiliateBalances(w.ctx, &plansv1.GetAffiliateBalancesRequest{})
		if len(bal.GetBalances()) != 1 || bal.GetBalances()[0].GetPendingMinor() != 2000 || bal.GetBalances()[0].GetReversedMinor() != 1000 {
			t.Errorf("balances = %v", bal)
		}
		pending, err := e.aff.ListAffiliateCommissions(w.ctx, &plansv1.ListAffiliateCommissionsRequest{Status: "pending", AffiliateId: w.aff.GetId(), PageSize: 1})
		if err != nil || pending.GetTotal() != 2 || len(pending.GetCommissions()) != 1 || pending.GetNextPageToken() == "" {
			t.Errorf("filter and page: %v %v", pending, err)
		}
		if _, err := e.aff.ListAffiliateCommissions(w.ctx, &plansv1.ListAffiliateCommissionsRequest{Status: "bogus"}); code(t, err) != codes.InvalidArgument {
			t.Errorf("a bad status: %v", err)
		}
		// Another tenant sees none of it.
		other := e.account(t, "AffFunnelOther")[plansv1.Environment_ENVIRONMENT_LIVE]
		if r, err := e.aff.ListAffiliateCommissions(as(other.GetId()), &plansv1.ListAffiliateCommissionsRequest{}); err != nil || r.GetTotal() != 0 {
			t.Errorf("another tenant sees commissions: %v %v", r, err)
		}
	})
}
