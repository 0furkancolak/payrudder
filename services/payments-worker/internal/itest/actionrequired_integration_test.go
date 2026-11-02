//go:build integration

package itest

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// refProvider is a provider that, like Stripe, gives a reference with its first response and can be asked by it: it records how it was asked.
type refProvider struct {
	mu     sync.Mutex
	charge error // what Charge returns
	status payments.StatusResult
	calls  []string
}

func (p *refProvider) note(s string) { p.mu.Lock(); p.calls = append(p.calls, s); p.mu.Unlock() }
func (p *refProvider) called() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}
func (p *refProvider) Name() string { return "reffake" }
func (p *refProvider) Capabilities() payments.Capabilities {
	return payments.Capabilities{Name: "reffake", Refunds: true, IdempotentCharges: true, IdempotentRefunds: true}
}
func (p *refProvider) Charge(context.Context, payments.ChargeRequest) (payments.ChargeResult, error) {
	if p.charge != nil {
		return payments.ChargeResult{}, p.charge
	}
	return payments.ChargeResult{ChargeID: "pi_ok"}, nil
}
func (p *refProvider) Refund(context.Context, payments.RefundRequest) (payments.RefundResult, error) {
	return payments.RefundResult{RefundID: "re_1"}, nil
}
func (p *refProvider) Status(_ context.Context, key string) (payments.StatusResult, error) {
	p.note("search " + key)
	return p.status, nil
}
func (p *refProvider) StatusByRef(_ context.Context, ref, key string) (payments.StatusResult, error) {
	p.note("ref " + ref + " " + key)
	return p.status, nil
}

// A charge that needs the customer to act is answered PaymentActionRequired, with no ledger row (a pending one would make the hosted page refuse the payment the customer
// is asked to make); the reference of its first response is kept with the attempt, and the status query asks the provider by it, not by a search; a charge that got no
// reference is searched for as before; and a payment still waiting after the window of the pending payments is declined as authentication_required.
func TestAChargeThatNeedsTheCustomerIsAskedByItsReference(t *testing.T) {
	wd := openDB(t, testkit.StartPostgres(t))
	ctx := context.Background()
	box, _ := secretbox.New(bytes.Repeat([]byte{4}, 32))
	prov := &refProvider{}
	registry := payments.NewRegistry()
	registry.Register("reffake", func([]byte) (payments.Provider, error) { return prov, nil })
	accounts := store.Accounts{DB: wd.DB, Box: box}
	tenant := uuid.New()
	if err := accounts.Put(ctx, tenant, "reffake", "live", []byte(`{}`), true); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC() // the attempt's start is stamped by the database, so the injected clock starts at the real one
	h := &handler.Handler{Sandbox: payments.NewMockPSP("http://127.0.0.1:1", 0), Attempts: store.Attempts{DB: wd.DB}, Ledger: store.Ledger{DB: wd.DB},
		Resolver: &handler.Resolver{Registry: registry, Accounts: accounts}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		TransferWindow: 48 * time.Hour, Now: func() time.Time { return now }}
	charge := func(invoice uuid.UUID, key string) *commandsv1.ReplyEnvelope {
		t.Helper()
		reply, err := h.Handle(ctx, &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), SagaId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
			Command: &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{InvoiceId: invoice.String(), AmountMinor: 1116, Currency: "USD", PaymentMethod: "pm_card", IdempotencyKey: key}}})
		if err != nil {
			t.Fatal(err)
		}
		return reply
	}
	status := func(invoice uuid.UUID, key string) *commandsv1.PaymentStatus {
		t.Helper()
		reply, err := h.Handle(ctx, &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), SagaId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
			Command: &commandsv1.CommandEnvelope_QueryPaymentStatus{QueryPaymentStatus: &commandsv1.QueryPaymentStatus{InvoiceId: invoice.String(), IdempotencyKey: key}}})
		if err != nil {
			t.Fatal(err)
		}
		return reply.GetPaymentStatus()
	}
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := wd.At(tenant).QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// The customer has to act: the reply says so, the books show nothing, the reference is kept.
	invoice, key := uuid.New(), "inv-3ds:charge:0"
	prov.charge = &payments.ActionRequiredError{Ref: "pi_act"}
	reply := charge(invoice, key)
	if reply.GetPaymentActionRequired().GetPspChargeId() != "pi_act" || reply.GetPaymentDeclined() != nil {
		t.Fatalf("reply = %v, want PaymentActionRequired pi_act", reply)
	}
	if n := count(`SELECT count(*) FROM payment_ledger WHERE tenant_id = $1`, tenant); n != 0 {
		t.Errorf("%d ledger rows for a payment that waits for the customer, want none", n)
	}
	if n := count(`SELECT count(*) FROM payment_attempts WHERE tenant_id = $1 AND idempotency_key = $2 AND provider_ref = 'pi_act'`, tenant, key); n != 1 {
		t.Errorf("%d attempts hold the reference, want 1", n)
	}

	// Asked by the reference, not searched for; it is still waiting.
	prov.status = payments.StatusResult{State: payments.StateActionRequired, ChargeID: "pi_act"}
	if st := status(invoice, key); st.GetState() != commandsv1.PaymentStatus_STATE_ACTION_REQUIRED || st.GetPspChargeId() != "pi_act" {
		t.Errorf("status = %v, want action required", st)
	}
	if got := prov.called(); len(got) != 1 || got[0] != "ref pi_act "+key {
		t.Errorf("the provider was asked %v, want only the reference", got)
	}
	// Past the window of the pending payments it is a decline that dunning handles, not final.
	now = now.Add(49 * time.Hour)
	if st := status(invoice, key); st.GetState() != commandsv1.PaymentStatus_STATE_DECLINED || st.GetDeclineCode() != "authentication_required" || st.GetFinal() {
		t.Errorf("status after the window = %v, want declined authentication_required", st)
	}
	// The customer came back and paid.
	prov.status = payments.StatusResult{State: payments.StateSucceeded, ChargeID: "pi_act"}
	if st := status(invoice, key); st.GetState() != commandsv1.PaymentStatus_STATE_SUCCEEDED || st.GetPspChargeId() != "pi_act" {
		t.Errorf("status = %v, want succeeded", st)
	}

	// A decline keeps the reference of its payment too; a charge that never got an answer has none and is searched for by its key.
	declined, dkey := uuid.New(), "inv-dec:charge:0"
	prov.charge = &payments.DeclinedError{Code: "card_declined", Ref: "pi_dec"}
	if reply := charge(declined, dkey); reply.GetPaymentDeclined().GetDeclineCode() != "card_declined" {
		t.Fatalf("reply = %v", reply)
	}
	if n := count(`SELECT count(*) FROM payment_attempts WHERE tenant_id = $1 AND idempotency_key = $2 AND provider_ref = 'pi_dec'`, tenant, dkey); n != 1 {
		t.Errorf("a decline did not keep the reference")
	}
	lost, lkey := uuid.New(), "inv-lost:charge:0"
	prov.charge = payments.ErrTimeout
	if reply := charge(lost, lkey); reply.GetPaymentErrored().GetCode() != handler.CodeTimeout {
		t.Fatalf("reply = %v", reply)
	}
	prov.status = payments.StatusResult{State: payments.StateNotFound}
	before := len(prov.called())
	if st := status(lost, lkey); st.GetState() != commandsv1.PaymentStatus_STATE_NOT_FOUND {
		t.Errorf("status = %v, want not found", st)
	}
	if got := prov.called(); len(got) != before+1 || got[before] != "search "+lkey {
		t.Errorf("a charge with no reference was asked %v, want a search by its key", got[before:])
	}
	if count(`SELECT count(*) FROM payment_attempts WHERE tenant_id = $1 AND idempotency_key = $2 AND provider_ref IS NOT NULL`, tenant, lkey) != 0 {
		t.Error("a charge that got no answer holds a reference")
	}
}
