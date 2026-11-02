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
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// noDedupe is a provider that charges again whatever key it is sent, as iyzico and Paddle do ("iyzico": Status finds the payment by the key, "paddle": the charge is
// accepted and settled by a notification, so Status can only say that it waits for it). It counts the charges it took.
type noDedupe struct {
	mu      sync.Mutex
	taken   int
	charged map[string]string // account|key -> the charge id
	down    map[string]bool   // account -> "definitely not processed"
	timeout map[string]bool   // account -> the charge is taken and the answer is lost
	calls   []string
}

type noDedupeAccount struct {
	w       *noDedupe
	account string
	like    string
}

func (w *noDedupe) factory(settings []byte) (payments.Provider, error) {
	var s struct{ Account, Like string }
	if err := json.Unmarshal(settings, &s); err != nil {
		return nil, err
	}
	return &noDedupeAccount{w: w, account: s.Account, like: s.Like}, nil
}

func (a *noDedupeAccount) Name() string { return "nodedupe" }
func (a *noDedupeAccount) Capabilities() payments.Capabilities {
	return payments.Capabilities{Name: "nodedupe", Refunds: true, IdempotentRefunds: true}
}

func (a *noDedupeAccount) Charge(_ context.Context, r payments.ChargeRequest) (payments.ChargeResult, error) {
	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	a.w.calls = append(a.w.calls, a.account+" charge "+r.IdempotencyKey)
	if a.w.down[a.account] {
		return payments.ChargeResult{}, &payments.NotProcessedError{Err: &payments.TransientError{Err: errors.New("psp answered 503 before processing")}}
	}
	a.w.taken++
	id := fmt.Sprintf("pay_%s_%d", a.account, a.w.taken)
	a.w.charged[a.account+"|"+r.IdempotencyKey] = id
	if a.w.timeout[a.account] {
		return payments.ChargeResult{}, payments.ErrTimeout
	}
	if a.like == "paddle" {
		return payments.ChargeResult{}, &payments.PendingError{Ref: r.IdempotencyKey}
	}
	return payments.ChargeResult{ChargeID: id}, nil
}

func (a *noDedupeAccount) Refund(context.Context, payments.RefundRequest) (payments.RefundResult, error) {
	return payments.RefundResult{RefundID: "re_1"}, nil
}

func (a *noDedupeAccount) Status(_ context.Context, key string) (payments.StatusResult, error) {
	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	a.w.calls = append(a.w.calls, a.account+" status "+key)
	if a.like == "paddle" {
		return payments.StatusResult{}, &payments.PendingError{Ref: "awaiting the notification"}
	}
	if id, ok := a.w.charged[a.account+"|"+key]; ok {
		return payments.StatusResult{State: payments.StateSucceeded, ChargeID: id}, nil
	}
	return payments.StatusResult{State: payments.StateNotFound}, nil
}

func (w *noDedupe) chargeCalls(account string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, c := range w.calls {
		if strings.HasPrefix(c, account+" charge ") {
			n++
		}
	}
	return n
}

// Billing re-issues a charge whose answer did not come in time with a fresh command id and the same idempotency key (sagas/command/sweep.go). A provider that does not
// dedupe by key must not be charged again: the first charge may have been taken, and the answer lost. The same holds when the first attempt failed over to another
// account: the re-issue goes on there, and the account that did not process it is not tried again. Each case on an iyzico-like provider (asked by the key) and a
// Paddle-like one (answers only through its notification, which is in the ledger once it has been confirmed).
func TestAChargeReissuedWithANewCommandIdIsNeverChargedTwice(t *testing.T) {
	ctx := context.Background()
	for _, like := range []string{"iyzico", "paddle"} {
		t.Run(like, func(t *testing.T) {
			pg := testkit.StartPostgres(t)
			wd := openDB(t, pg)
			w := &noDedupe{charged: map[string]string{}, down: map[string]bool{}, timeout: map[string]bool{}}
			box, _ := secretbox.New(bytes.Repeat([]byte{4}, 32))
			registry := payments.NewRegistry()
			registry.Register("nodedupe", w.factory)
			accounts := store.Accounts{DB: wd.DB, Box: box}
			ledger := store.Ledger{DB: wd.DB}
			ph := store.ProviderHealth{DB: wd.DB}
			h := &handler.Handler{Sandbox: payments.NewMockPSP("http://127.0.0.1:1", 0), Attempts: store.Attempts{DB: wd.DB}, Ledger: ledger, Failovers: ph,
				Resolver: &handler.Resolver{Registry: registry, Accounts: accounts}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

			// scenario is a tenant of its own with two accounts, a (the default) and b.
			type run struct {
				tenant, invoice uuid.UUID
				key             string
			}
			scenario := func(t *testing.T, failover bool) run {
				t.Helper()
				r := run{uuid.New(), uuid.New(), ""}
				r.key = r.invoice.String() + ":charge:0"
				for _, a := range []struct {
					name    string
					account string
					def     bool
				}{{"nodedupe", "a", true}, {"nodedupe~b", "b", false}} {
					if err := accounts.Put(ctx, r.tenant, a.name, "live", []byte(`{"account":"`+a.account+`","like":"`+like+`"}`), a.def); err != nil {
						t.Fatal(err)
					}
				}
				if failover {
					if err := ph.SetFailover(ctx, r.tenant, "live", "nodedupe", "nodedupe~b", "test"); err != nil {
						t.Fatal(err)
					}
				}
				w.mu.Lock()
				w.taken, w.charged, w.down, w.timeout, w.calls = 0, map[string]string{}, map[string]bool{}, map[string]bool{}, nil
				w.mu.Unlock()
				return r
			}
			charge := func(r run, command uuid.UUID) (*commandsv1.ReplyEnvelope, error) {
				return h.Handle(ctx, &commandsv1.CommandEnvelope{CommandId: command.String(), SagaId: uuid.NewString(), TenantId: r.tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
					Command: &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{InvoiceId: r.invoice.String(), AmountMinor: 1116, Currency: "USD",
						PaymentMethod: "pm_card", IdempotencyKey: r.key}}})
			}
			// notified is the provider's notification, as the webhook writes it once the provider has confirmed it.
			notified := func(t *testing.T, r run, provider, ref, key string) {
				t.Helper()
				if _, err := ledger.Append(ctx, store.LedgerEntry{TenantID: r.tenant, InvoiceID: r.invoice, Provider: provider, Environment: "live", Kind: store.KindCharge,
					Status: store.StatusSucceeded, AmountMinor: 1116, Currency: "USD", ProviderRef: ref, IdempotencyKey: key, Source: store.SourceWebhook}); err != nil {
					t.Fatal(err)
				}
			}
			// settled is how the re-issued charge ends: answered with the charge that was taken. A Paddle-like provider cannot be asked, so the command waits (an error: the
			// queue delivers it again) until the notification is in the ledger.
			settled := func(t *testing.T, r run, command uuid.UUID, wantRef, ledgerProvider, ledgerKey string) {
				t.Helper()
				if like == "paddle" {
					if reply, err := charge(r, command); err == nil || reply != nil {
						t.Fatalf("the re-issued charge of a provider that answers only by notification = %v, %v; want it to wait", reply, err)
					}
					notified(t, r, ledgerProvider, wantRef, ledgerKey)
				}
				reply, err := charge(r, command)
				if err != nil || reply.GetPaymentSucceeded().GetPspChargeId() != wantRef {
					t.Fatalf("the re-issued charge = %v, %v; want the charge %s that was taken", reply, err, wantRef)
				}
				if w.taken != 1 {
					t.Fatalf("the provider took %d charges, want 1 (calls: %v)", w.taken, w.calls)
				}
			}

			// The command names no provider (the subscription has none), so it goes to the tenant's default account: the account the first attempt went to, or, when the
			// default moved before the re-issue (or before the same message was delivered again), the account the charge must still be asked at, not the new default (which
			// never saw the key and would charge again).
			for _, c := range []struct {
				name        string
				moved, same bool
			}{{"at the account the charge was sent to", false, false}, {"at the account of the first attempt after the default moved", true, false},
				{"at the account of the first attempt when the same command is delivered again after the default moved", true, true}} {
				moved := c.moved
				t.Run(c.name, func(t *testing.T) {
					r := scenario(t, false)
					if moved {
						w.mu.Lock()
						w.timeout["a"] = true // the charge is taken and nothing answers: no ledger row, nothing for the new default to find
						w.mu.Unlock()
					}
					command := uuid.New()
					first, err := charge(r, command)
					switch {
					case moved:
						if err != nil || first.GetPaymentErrored().GetCode() != handler.CodeTimeout {
							t.Fatalf("first charge = %v, %v; want it timed out", first, err)
						}
					case like == "iyzico" && (err != nil || first.GetPaymentSucceeded().GetPspChargeId() != "pay_a_1"):
						t.Fatalf("first charge = %v, %v", first, err)
					case like == "paddle" && (err != nil || first.GetPaymentErrored().GetCode() != handler.CodePending):
						t.Fatalf("first charge = %v, %v; want it pending", first, err)
					}
					if moved {
						if ok, err := accounts.SetDefault(ctx, r.tenant, "nodedupe~b", "live", store.Audit{}); err != nil || !ok {
							t.Fatalf("move the default: %v, %v", ok, err)
						}
					}
					// The answer never reached billing; the step deadline passed and it asks again with a command id of its own.
					ref := "pay_a_1"
					if like == "paddle" {
						ref = "txn_a_1"
					}
					if !c.same {
						command = uuid.New()
					}
					settled(t, r, command, ref, "nodedupe", r.key)
					if got := w.chargeCalls("a"); got != 1 {
						t.Errorf("the account was sent %d charges, want 1", got)
					}
					if got := w.chargeCalls("b"); got != 0 {
						t.Errorf("the account that never saw the key was sent %d charges, want 0", got)
					}
				})
			}

			t.Run("at the account it failed over to", func(t *testing.T) {
				r := scenario(t, true)
				w.down["a"] = true
				first, err := charge(r, uuid.New())
				if like == "iyzico" && (err != nil || first.GetPaymentSucceeded().GetPspChargeId() != "pay_b_1") {
					t.Fatalf("first charge = %v, %v; want the charge at the second account", first, err)
				}
				if like == "paddle" && (err != nil || first.GetPaymentErrored().GetCode() != handler.CodePending) {
					t.Fatalf("first charge = %v, %v; want it pending at the second account", first, err)
				}
				// The first account is up again when billing re-issues the charge: it must not be charged, nor must a second charge be made at the second.
				w.mu.Lock()
				w.down["a"] = false
				w.mu.Unlock()
				ref := "pay_b_1"
				if like == "paddle" {
					ref = "txn_b_1"
				}
				settled(t, r, uuid.New(), ref, "nodedupe~b", payments.FailoverKey(r.key, "nodedupe~b"))
				if a, b := w.chargeCalls("a"), w.chargeCalls("b"); a != 1 || b != 1 {
					t.Errorf("charges sent: first account %d, second %d; want 1 and 1 (the first account only for the attempt that did not process it)", a, b)
				}
			})
		})
	}
}
