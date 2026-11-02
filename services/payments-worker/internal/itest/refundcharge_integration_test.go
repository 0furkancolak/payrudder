//go:build integration

package itest

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/iyzico"
	"github.com/0furkancolak/payrudder/pkg/payments/paddle"
	"github.com/0furkancolak/payrudder/pkg/payments/stripe"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// chargeFake plays the charge and refund endpoints of Stripe, iyzico and Paddle and remembers which charge each refund was made against.
type chargeFake struct {
	mu      sync.Mutex
	srv     *httptest.Server
	refunds []string // the provider's id of the charge, as the refund named it
}

func newChargeFake(t *testing.T) *chargeFake {
	f := &chargeFake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/v1/payment_intents": // Stripe charge
			_, _ = io.WriteString(w, `{"id":"pi_1","status":"succeeded"}`)
		case r.URL.Path == "/v1/refunds":
			form, _ := url.ParseQuery(string(raw))
			f.refunds = append(f.refunds, form.Get("payment_intent"))
			_, _ = io.WriteString(w, `{"id":"re_1","status":"succeeded"}`)
		case r.URL.Path == "/payment/auth": // iyzico charge
			_, _ = io.WriteString(w, `{"status":"success","paymentId":"P1"}`)
		case r.URL.Path == "/v2/payment/refund" || r.URL.Path == "/payment/cancel": // iyzico cancels a payment refunded in full the day it was taken
			f.refunds = append(f.refunds, jsonField(raw, "paymentId"))
			_, _ = io.WriteString(w, `{"status":"success","paymentId":"P1","refundHostReference":"R1"}`)
		case strings.HasPrefix(r.URL.Path, "/subscriptions/") && strings.HasSuffix(r.URL.Path, "/charge"): // Paddle charge: accepted, settled by the notification
			_, _ = io.WriteString(w, `{"data":{}}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/transactions/"):
			_, _ = io.WriteString(w, `{"data":{"id":"txn_01","currency_code":"USD","details":{"totals":{"grand_total":"1116"},"line_items":[{"id":"txnitm_01"}]}}}`)
		case r.URL.Path == "/adjustments":
			f.refunds = append(f.refunds, jsonField(raw, "transaction_id"))
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"data":{"id":"adj_01"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// jsonField reads a string field of a flat JSON body without decoding it.
func jsonField(raw []byte, name string) string {
	_, rest, ok := strings.Cut(string(raw), `"`+name+`":"`)
	if !ok {
		return ""
	}
	v, _, _ := strings.Cut(rest, `"`)
	return v
}

func (f *chargeFake) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.refunds...)
}

func (f *chargeFake) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refunds = nil
}

// Billing sends RefundPayment without psp_charge_id (it keeps no charge id), and Stripe, Paddle and iyzico each refuse a refund that names no charge. So payments-worker
// finds the charge in its ledger: the invoice's successful charge at the account that took it. With several it takes the one the refund's amount matches, and never guesses
// among the others. Nothing here sets the charge id on the command, which is what hid this before.
func TestARefundWithoutAChargeIdFindsTheChargeInTheLedger(t *testing.T) {
	pg := testkit.StartPostgres(t)
	cdb := openDB(t, pg)
	ctx := context.Background()
	fake := newChargeFake(t)

	box, _ := secretbox.New(bytes.Repeat([]byte{6}, 32))
	registry := payments.NewRegistry()
	stripe.Register(registry)
	iyzico.Register(registry)
	paddle.Register(registry)
	accounts := store.Accounts{DB: cdb.DB, Box: box}
	attempts, ledger := store.Attempts{DB: cdb.DB}, store.Ledger{DB: cdb.DB}
	h := &handler.Handler{Sandbox: payments.NewMockPSP("http://unused", 0), Attempts: attempts, Ledger: ledger, Resolver: &handler.Resolver{Registry: registry, Accounts: accounts},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	providers := []struct {
		name, settings, method, charge, other string
	}{
		{"stripe", `{"api_key":"sk_live_abc","base_url":"` + fake.srv.URL + `"}`, "cus_1:pm_1", "pi_1", "pi_2"},
		{"iyzico", `{"api_key":"api","secret_key":"s","base_url":"` + fake.srv.URL + `","buyer":{"name":"Ada","surname":"L","identity_number":"11111111111","address":"x","city":"Istanbul","country":"Turkey","zip_code":"34000","ip":"1.2.3.4","gsm_number":"+905550000000","email":"b@example.com"}}`,
			"user:card", "P1", "P2"},
		{"paddle", `{"api_key":"pdl_secret","base_url":"` + fake.srv.URL + `","webhook_secret":"w","usage_product_id":"pro_usage"}`, "sub_1", "txn_01", "txn_02"},
	}
	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			// scenario is a tenant of its own whose invoice was charged through the provider, as the command path and the notification leave it in the ledger.
			start := func(t *testing.T) chargeScenario {
				t.Helper()
				s := chargeScenario{uuid.New(), uuid.New()}
				if err := accounts.Put(ctx, s.tenant, p.name, "live", []byte(p.settings), true); err != nil {
					t.Fatal(err)
				}
				reply, err := h.Handle(ctx, &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: s.tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
					Command: &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{InvoiceId: s.invoice.String(), AmountMinor: 1116, Currency: "USD",
						PaymentMethod: p.method, IdempotencyKey: s.invoice.String() + ":charge:0"}}})
				if err != nil {
					t.Fatalf("charge: %v", err)
				}
				if p.name == "paddle" { // Paddle accepts the charge; its notification, once confirmed, is what writes the transaction into the ledger
					if reply.GetPaymentErrored().GetCode() != handler.CodePending {
						t.Fatalf("charge = %v; want it pending", reply)
					}
					s.chargeNotified(t, ledger, p.name, p.charge, 1116, s.invoice.String()+":charge:0")
				} else if reply.GetPaymentSucceeded().GetPspChargeId() != p.charge {
					t.Fatalf("charge = %v; want the charge %s", reply, p.charge)
				}
				fake.reset()
				return s
			}
			refund := func(s chargeScenario, amount int64, chargeID string) (*commandsv1.ReplyEnvelope, error) {
				return h.Handle(ctx, &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: s.tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
					Command: &commandsv1.CommandEnvelope_RefundPayment{RefundPayment: &commandsv1.RefundPayment{InvoiceId: s.invoice.String(), PspChargeId: chargeID,
						AmountMinor: amount, Currency: "USD", IdempotencyKey: s.invoice.String() + ":cn:" + uuid.NewString() + ":refund"}}})
			}

			t.Run("the invoice's one charge is the one refunded, in full or in part", func(t *testing.T) {
				s := start(t)
				for _, amount := range []int64{1116, 400} {
					if reply, err := refund(s, amount, ""); err != nil || reply.GetPaymentRefunded() == nil {
						t.Fatalf("refund of %d = %v, %v; want it made against %s", amount, reply, err, p.charge)
					}
				}
				if got := fake.asked(); len(got) != 2 || got[0] != p.charge || got[1] != p.charge {
					t.Fatalf("the provider refunded charges %v, want %s twice", got, p.charge)
				}
			})

			t.Run("a charge id the command names is not overridden", func(t *testing.T) {
				s := start(t)
				named := map[string]string{"stripe": "pi_named", "iyzico": "Pnamed", "paddle": "txn_named"}[p.name]
				if reply, err := refund(s, 1116, named); err != nil || reply.GetPaymentRefunded() == nil {
					t.Fatalf("refund = %v, %v", reply, err)
				}
				if got := fake.asked(); len(got) != 1 || got[0] != named {
					t.Fatalf("the provider refunded %v, want %s", got, named)
				}
			})

			t.Run("with several charges the amount picks one, and nothing else is guessed", func(t *testing.T) {
				s := start(t)
				s.chargeNotified(t, ledger, p.name, p.other, 700, s.invoice.String()+":charge:1") // a second payment of the invoice (a payment link that was paid as well)
				if reply, err := refund(s, 700, ""); err != nil || reply.GetPaymentRefunded() == nil {
					t.Fatalf("refund of the second charge's amount = %v, %v", reply, err)
				}
				if reply, err := refund(s, 1116, ""); err != nil || reply.GetPaymentRefunded() == nil {
					t.Fatalf("refund of the first charge's amount = %v, %v", reply, err)
				}
				if got := fake.asked(); len(got) != 2 || got[0] != p.other || got[1] != p.charge {
					t.Fatalf("the provider refunded charges %v, want %s then %s", got, p.other, p.charge)
				}
				fake.reset()
				// 300 is no charge's amount: a partial refund of one of two charges cannot be told from one of the other, so it is refused with the key that says so, and the
				// provider is not asked. The refusal moves no money, so the same refund may be asked again once the operator names the charge.
				reply, err := refund(s, 300, "")
				got := reply.GetPaymentErrored()
				if err != nil || got.GetCode() != handler.CodeRefundChargeUnclear || got.GetErrorKey() != "payments.refund.charge_unclear" || got.GetRetryable() {
					t.Fatalf("refund of 300 = %v, %v; want a permanent %s", reply, err, handler.CodeRefundChargeUnclear)
				}
				if got := fake.asked(); len(got) != 0 {
					t.Fatalf("the provider was asked to refund %v", got)
				}
			})

			t.Run("two charges of the same amount are ambiguous too", func(t *testing.T) {
				s := start(t)
				s.chargeNotified(t, ledger, p.name, p.other, 1116, s.invoice.String()+":charge:1")
				reply, err := refund(s, 1116, "")
				if got := reply.GetPaymentErrored(); err != nil || got.GetCode() != handler.CodeRefundChargeUnclear {
					t.Fatalf("refund = %v, %v; want %s", reply, err, handler.CodeRefundChargeUnclear)
				}
				if got := fake.asked(); len(got) != 0 {
					t.Fatalf("the provider was asked to refund %v", got)
				}
			})
		})
	}
}

type chargeScenario struct{ tenant, invoice uuid.UUID }

func (s chargeScenario) chargeNotified(t *testing.T, ledger store.Ledger, provider, ref string, amount int64, key string) {
	t.Helper()
	if _, err := ledger.Append(context.Background(), store.LedgerEntry{TenantID: s.tenant, InvoiceID: s.invoice, Provider: provider, Environment: "live", Kind: store.KindCharge,
		Status: store.StatusSucceeded, AmountMinor: amount, Currency: "USD", ProviderRef: ref, IdempotencyKey: key, Source: store.SourceWebhook}); err != nil {
		t.Fatal(err)
	}
}
