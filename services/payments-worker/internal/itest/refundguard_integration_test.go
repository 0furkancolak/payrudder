//go:build integration

package itest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/iyzico"
	"github.com/0furkancolak/payrudder/pkg/payments/paddle"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// refundFake plays the refund endpoints of iyzico (POST /v2/payment/refund) and of Paddle (POST /adjustments), neither of which dedupes a repeated request, and counts
// the refunds it was asked for.
type refundFake struct {
	mu      sync.Mutex
	srv     *httptest.Server
	refunds int
	mode    string // ok, down (HTTP 500: the outcome is unknown), refuse (the provider says no)
}

func newRefundFake(t *testing.T) *refundFake {
	f := &refundFake{mode: "ok"}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/transactions/") { // Paddle reads the transaction before it refunds an amount; that is no refund
			_, _ = io.WriteString(w, `{"data":{"id":"txn_01","currency_code":"USD","details":{"totals":{"grand_total":"1116"},"line_items":[{"id":"txnitm_01"}]}}}`)
			return
		}
		f.refunds++
		iyz := r.URL.Path == "/v2/payment/refund"
		switch {
		case f.mode == "down":
			w.WriteHeader(http.StatusInternalServerError)
		case f.mode == "refuse" && iyz:
			_, _ = io.WriteString(w, `{"status":"failure","errorCode":"5000","errorMessage":"cannot be refunded","errorGroup":"NOT_REFUNDABLE"}`)
		case f.mode == "refuse":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"code":"adjustment_pending_approval","detail":"an adjustment is pending approval"}}`)
		case iyz:
			_, _ = io.WriteString(w, `{"status":"success","paymentId":"P1","refundHostReference":"R1"}`)
		default:
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"data":{"id":"adj_01"}}`)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *refundFake) set(mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
}

func (f *refundFake) asked() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refunds
}

// ledgerWritesFail is a database whose inserts into the ledger fail while everything else works: the bookkeeping breaks after the provider has been called.
type ledgerWritesFail struct{ store.DB }

func (d ledgerWritesFail) BeginTx(ctx context.Context, o pgx.TxOptions) (pgx.Tx, error) {
	tx, err := d.DB.BeginTx(ctx, o)
	if err != nil {
		return nil, err
	}
	return failingTx{tx}, nil
}

type failingTx struct{ pgx.Tx }

func (t failingTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "INSERT INTO payment_ledger") {
		return pgconn.CommandTag{}, errors.New("the ledger cannot be written")
	}
	return t.Tx.Exec(ctx, sql, args...)
}

// A refund is sent again by billing with a new command id and the same idempotency key (a credit note after five minutes of silence, the voiding saga after its step
// deadline), and a command that failed comes back with its own id. iyzico and Paddle do not dedupe, so payments-worker must: exactly one refund at the provider,
// whatever is resent, and never a second try of one whose outcome it does not know.
func TestRefundResendNeverRefundsTwice(t *testing.T) {
	pg := testkit.StartPostgres(t)
	cdb := openDB(t, pg)
	pool := cdb.DB
	ctx := context.Background()
	fake := newRefundFake(t)

	box, _ := secretbox.New(bytes.Repeat([]byte{5}, 32))
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	paddle.Register(registry)
	accounts := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	attempts, ledger := store.Attempts{DB: pool}, store.Ledger{DB: pool}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	newHandler := func(l store.Ledger) *handler.Handler {
		return &handler.Handler{Sandbox: payments.NewMockPSP("http://unused", 0), Attempts: attempts, Ledger: l, Resolver: resolver, Log: log}
	}
	h := newHandler(ledger)
	hNoLedger := newHandler(store.Ledger{DB: ledgerWritesFail{pool}})

	providers := []struct{ name, settings, charge, ref string }{
		{"iyzico", `{"api_key":"api","secret_key":"s","base_url":"` + fake.srv.URL + `","buyer":{"name":"Ada","surname":"L","identity_number":"11111111111","address":"x","city":"Istanbul","country":"Turkey","zip_code":"34000","ip":"1.2.3.4","email":"b@example.com"}}`, "P1", "R1"},
		{"paddle", `{"api_key":"pdl_secret","base_url":"` + fake.srv.URL + `","webhook_secret":"w","usage_product_id":"pro_usage"}`, "txn_01", "adj_01"},
	}
	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			// scenario starts a tenant with its own account, so that the scenarios do not see each other's attempts, and sets the provider's behaviour.
			type run struct {
				tenant, invoice uuid.UUID
				key             string
			}
			scenario := func(t *testing.T, mode string) run {
				t.Helper()
				tenant, invoice := uuid.New(), uuid.New()
				if err := accounts.Put(ctx, tenant, p.name, "live", []byte(p.settings), true); err != nil {
					t.Fatal(err)
				}
				fake.mu.Lock()
				fake.refunds, fake.mode = 0, mode
				fake.mu.Unlock()
				return run{tenant, invoice, invoice.String() + ":cn:" + uuid.NewString() + ":refund"}
			}
			// send is one RefundPayment command. As workerx does, it stores the reply of a command it answered.
			send := func(h *handler.Handler, r run, command uuid.UUID, key string) (*commandsv1.ReplyEnvelope, error) {
				t.Helper()
				reply, err := h.Handle(ctx, &commandsv1.CommandEnvelope{CommandId: command.String(), TenantId: r.tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
					Command: &commandsv1.CommandEnvelope_RefundPayment{RefundPayment: &commandsv1.RefundPayment{InvoiceId: r.invoice.String(), PspChargeId: p.charge,
						AmountMinor: 1116, Currency: "USD", IdempotencyKey: key}}})
				if err == nil && reply != nil {
					if err := attempts.StoreFor(ctx, r.tenant, command, reply); err != nil {
						t.Fatal(err)
					}
				}
				return reply, err
			}
			wantRefunded := func(t *testing.T, reply *commandsv1.ReplyEnvelope, err error) {
				t.Helper()
				if err != nil || reply.GetPaymentRefunded().GetPspRefundId() != p.ref {
					t.Fatalf("reply = %v, %v; want the refund %s", reply, err, p.ref)
				}
			}
			wantUnknown := func(t *testing.T, reply *commandsv1.ReplyEnvelope, err error) {
				t.Helper()
				if got := reply.GetPaymentErrored(); err != nil || got.GetCode() != handler.CodeRefundUnknown || got.GetRetryable() {
					t.Fatalf("reply = %v, %v; want a permanent %s", reply, err, handler.CodeRefundUnknown)
				}
			}

			t.Run("a resend with a new command id is answered from the ledger", func(t *testing.T) {
				r := scenario(t, "ok")
				first, err := send(h, r, uuid.New(), r.key)
				wantRefunded(t, first, err)
				for range 2 {
					again, err := send(h, r, uuid.New(), r.key)
					wantRefunded(t, again, err)
				}
				if fake.asked() != 1 {
					t.Fatalf("the provider was asked to refund %d times, want 1", fake.asked())
				}
			})

			t.Run("the ledger write failed: the stored reply answers the resend and the ledger row is written then", func(t *testing.T) {
				r := scenario(t, "ok")
				failures := counted(t, "ledger_write_failures", attribute.String("provider", p.name), attribute.String("kind", store.KindRefund))
				first, err := send(hNoLedger, r, uuid.New(), r.key)
				wantRefunded(t, first, err) // the money moved: the command is answered, the bookkeeping error does not undo it
				if got := counted(t, "ledger_write_failures", attribute.String("provider", p.name), attribute.String("kind", store.KindRefund)); got != failures+1 {
					t.Errorf("ledger_write_failures went from %d to %d, want one more (the alert LedgerWriteFailed reads it)", failures, got)
				}
				if _, ok, _ := ledger.RefundFact(ctx, r.tenant, r.invoice, r.key); ok {
					t.Fatal("the ledger row was written although its write must have failed")
				}
				again, err := send(h, r, uuid.New(), r.key)
				wantRefunded(t, again, err)
				if ref, ok, err := ledger.RefundFact(ctx, r.tenant, r.invoice, r.key); err != nil || !ok || ref != p.ref {
					t.Errorf("ledger = %q, %v, %v; the resend must write the missing row", ref, ok, err)
				}
				if fake.asked() != 1 {
					t.Fatalf("the provider was asked to refund %d times, want 1", fake.asked())
				}
			})

			t.Run("a refund the provider did not answer is never sent again", func(t *testing.T) {
				r := scenario(t, "down")
				command := uuid.New()
				if reply, err := send(h, r, command, r.key); err == nil || reply != nil {
					t.Fatalf("a 500 from the provider: %v, %v; the command must be retried", reply, err)
				}
				// The queue redelivers the same command, then billing resends with new ids, and the provider is back.
				fake.set("ok")
				redelivered, err := send(h, r, command, r.key)
				wantUnknown(t, redelivered, err)
				for range 2 {
					resent, err := send(h, r, uuid.New(), r.key)
					wantUnknown(t, resent, err)
				}
				if fake.asked() != 1 {
					t.Fatalf("the provider was asked to refund %d times, want 1", fake.asked())
				}
			})

			t.Run("a crash between the provider call and the reply leaves an unknown outcome, never repeated", func(t *testing.T) {
				r := scenario(t, "ok")
				if _, err := attempts.Start(ctx, uuid.New(), r.tenant, r.invoice, store.KindRefund, "live", p.name, r.key, nil); err != nil {
					t.Fatal(err)
				}
				unknown := counted(t, "refund_outcome_unknown", attribute.String("provider", p.name))
				resent, err := send(h, r, uuid.New(), r.key)
				wantUnknown(t, resent, err)
				if got := counted(t, "refund_outcome_unknown", attribute.String("provider", p.name)); got != unknown+1 {
					t.Errorf("refund_outcome_unknown went from %d to %d, want one more (the alert RefundOutcomeUnknown reads it)", unknown, got)
				}
				if fake.asked() != 0 {
					t.Fatalf("the provider was asked to refund %d times, want 0", fake.asked())
				}
			})

			t.Run("a timeout is an unknown outcome too", func(t *testing.T) {
				r := scenario(t, "ok")
				timedOut := uuid.New()
				if _, err := attempts.Start(ctx, timedOut, r.tenant, r.invoice, store.KindRefund, "live", p.name, r.key, nil); err != nil {
					t.Fatal(err)
				}
				if err := attempts.StoreFor(ctx, r.tenant, timedOut, &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentErrored{
					PaymentErrored: &commandsv1.PaymentErrored{Code: handler.CodeTimeout}}}); err != nil {
					t.Fatal(err)
				}
				resent, err := send(h, r, uuid.New(), r.key)
				wantUnknown(t, resent, err)
				if fake.asked() != 0 {
					t.Fatalf("the provider was asked to refund %d times, want 0", fake.asked())
				}
			})

			t.Run("a refusal moved no money, so the same key may be tried again", func(t *testing.T) {
				r := scenario(t, "refuse")
				refused, err := send(h, r, uuid.New(), r.key)
				if got := refused.GetPaymentErrored(); err != nil || got.GetCode() != handler.CodeRejected {
					t.Fatalf("refusal = %v, %v", refused, err)
				}
				fake.set("ok")
				retried, err := send(h, r, uuid.New(), r.key)
				wantRefunded(t, retried, err)
				if fake.asked() != 2 {
					t.Fatalf("the provider was asked to refund %d times, want 2 (the refusal and the retry)", fake.asked())
				}
			})

			t.Run("a new key is a new refund", func(t *testing.T) {
				r := scenario(t, "ok")
				first, err := send(h, r, uuid.New(), r.key)
				wantRefunded(t, first, err)
				second, err := send(h, r, uuid.New(), r.key+":1") // what an operator's retry after a refusal sends (the generation)
				wantRefunded(t, second, err)
				if fake.asked() != 2 {
					t.Fatalf("the provider was asked to refund %d times, want 2", fake.asked())
				}
			})
		})
	}
}

// idempotentProvider is Stripe-like: it dedupes a refund by its key, so payments-worker may send a resend and lets the provider answer it.
type idempotentProvider struct {
	mu      sync.Mutex
	refunds map[string]int
}

func (p *idempotentProvider) Name() string { return "stripe" }
func (p *idempotentProvider) Capabilities() payments.Capabilities {
	return payments.Capabilities{Name: "stripe", Refunds: true, IdempotentCharges: true, IdempotentRefunds: true}
}
func (p *idempotentProvider) Charge(context.Context, payments.ChargeRequest) (payments.ChargeResult, error) {
	return payments.ChargeResult{ChargeID: "pi_1"}, nil
}
func (p *idempotentProvider) Refund(_ context.Context, r payments.RefundRequest) (payments.RefundResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refunds[r.IdempotencyKey]++
	return payments.RefundResult{RefundID: "re_1"}, nil
}
func (p *idempotentProvider) Status(context.Context, string) (payments.StatusResult, error) {
	return payments.StatusResult{State: payments.StateNotFound}, nil
}

// A provider that dedupes by key keeps its behaviour: the resend reaches it and it answers with the first result.
func TestRefundResendReachesAnIdempotentProvider(t *testing.T) {
	pg := testkit.StartPostgres(t)
	cdb := openDB(t, pg)
	ctx := context.Background()
	live := &idempotentProvider{refunds: map[string]int{}}
	attempts := store.Attempts{DB: cdb.DB}
	h := &handler.Handler{Sandbox: payments.NewMockPSP("http://unused", 0), Live: live, Attempts: attempts, Ledger: store.Ledger{DB: cdb.DB}}
	tenant, invoice := uuid.New(), uuid.New()
	key := invoice.String() + ":refund"
	for range 2 {
		reply, err := h.Handle(ctx, &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
			Command: &commandsv1.CommandEnvelope_RefundPayment{RefundPayment: &commandsv1.RefundPayment{InvoiceId: invoice.String(), PspChargeId: "pi_1",
				AmountMinor: 500, Currency: "USD", IdempotencyKey: key}}})
		if err != nil || reply.GetPaymentRefunded().GetPspRefundId() != "re_1" {
			t.Fatalf("refund = %v, %v", reply, err)
		}
	}
	if live.refunds[key] != 2 {
		t.Fatalf("the provider saw the key %d times, want 2: it dedupes, payments-worker does not stand in its way", live.refunds[key])
	}
}
