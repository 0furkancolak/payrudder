//go:build integration

// Package itest runs payments-worker against real Postgres and RabbitMQ with a scripted HTTP payment provider.
package itest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"google.golang.org/protobuf/proto"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/migrations"
)

// recordingProvider stands in for PAYMENTS_LIVE_PROVIDER and records its calls.
type recordingProvider struct{ calls atomic.Int32 }

func (r *recordingProvider) Name() string { return "recording" }
func (r *recordingProvider) Charge(context.Context, payments.ChargeRequest) (payments.ChargeResult, error) {
	r.calls.Add(1)
	return payments.ChargeResult{ChargeID: "live_ch"}, nil
}
func (r *recordingProvider) Refund(context.Context, payments.RefundRequest) (payments.RefundResult, error) {
	r.calls.Add(1)
	return payments.RefundResult{RefundID: "live_re"}, nil
}
func (r *recordingProvider) Status(context.Context, string) (payments.StatusResult, error) {
	r.calls.Add(1)
	return payments.StatusResult{State: payments.StateNotFound}, nil
}

// scriptedPSP answers charges from a script: statuses are used in order, then the last repeats.
type scriptedPSP struct {
	mu       sync.Mutex
	statuses []int
	times    []time.Time
	keys     []string
	srv      *httptest.Server
	status   func(key string) (int, map[string]any)
}

func newScripted(t *testing.T, statuses ...int) *scriptedPSP {
	s := &scriptedPSP{statuses: statuses}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/charges":
			s.times = append(s.times, time.Now())
			s.keys = append(s.keys, r.Header.Get("Idempotency-Key"))
			code := s.statuses[min(len(s.times), len(s.statuses))-1]
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			switch code {
			case http.StatusOK:
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "ch_scripted", "status": "succeeded"})
			case http.StatusPaymentRequired:
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "card_declined"}})
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/charges":
			code, body := http.StatusNotFound, map[string]any{"found": false}
			if s.status != nil {
				code, body = s.status(r.URL.Query().Get("idempotency_key"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func chargeCmd(env plansv1.Environment, id uuid.UUID, invoice uuid.UUID, dunning int32) []byte {
	b, _ := proto.Marshal(&commandsv1.CommandEnvelope{CommandId: id.String(), SagaId: uuid.NewString(), TenantId: uuid.NewString(), Environment: env,
		Command: &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{
			InvoiceId: invoice.String(), AmountMinor: 500, Currency: "USD", PaymentMethod: "pm_x", DunningAttempt: dunning,
			IdempotencyKey: invoice.String() + ":charge:0"}}})
	return b
}

func TestPaymentsWorker(t *testing.T) {
	pg := testkit.StartPostgres(t)
	db := testkit.MigratedDB(t, pg, "payments", migrations.FS)
	pool, err := pgxx.NewPool(context.Background(), db.System)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	url := testkit.StartRabbitMQ(t)
	if err := rabbitx.DeclareTopology(url); err != nil {
		t.Fatal(err)
	}
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, _ := conn.Channel()
	pub := rabbitx.NewPublisher(url)
	defer pub.Close()
	send := func(queue string, body []byte) {
		t.Helper()
		if err := pub.Publish(context.Background(), rabbitx.Message{Exchange: rabbitx.ExchangeCommands, RoutingKey: queue, Publishing: amqp.Publishing{Body: body}}); err != nil {
			t.Fatal(err)
		}
	}
	reply := func(wait time.Duration) *commandsv1.ReplyEnvelope {
		t.Helper()
		deadline := time.Now().Add(wait)
		for time.Now().Before(deadline) {
			if d, ok, _ := ch.Get(rabbitx.ReplyQueue, true); ok {
				var r commandsv1.ReplyEnvelope
				_ = proto.Unmarshal(d.Body, &r)
				return &r
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("no reply")
		return nil
	}
	start := func(h *handler.Handler) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		w := &workerx.Worker{URL: url, Queues: []string{rabbitx.QueuePaymentCharge, rabbitx.QueuePaymentRefund, rabbitx.QueuePaymentStatus},
			Handler: h, Inbox: h.Attempts, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		go func() { _ = w.Run(ctx) }()
	}
	attempts := store.Attempts{DB: pool}

	t.Run("ServerErrorsRetryThroughTheFiveAndThirtySecondTiers", func(t *testing.T) {
		psp := newScripted(t, 503, 503, 200)
		start(&handler.Handler{Sandbox: payments.NewMockPSP(psp.srv.URL, 2*time.Second), Attempts: attempts})
		send(rabbitx.QueuePaymentCharge, chargeCmd(plansv1.Environment_ENVIRONMENT_SANDBOX, uuid.New(), uuid.New(), 0))
		r := reply(90 * time.Second)
		if r.GetPaymentSucceeded().GetPspChargeId() != "ch_scripted" {
			t.Fatalf("reply = %v, want PaymentSucceeded", r)
		}
		psp.mu.Lock()
		defer psp.mu.Unlock()
		if len(psp.times) != 3 {
			t.Fatalf("%d provider calls, want 3 (two failures and the success)", len(psp.times))
		}
		if gap := psp.times[1].Sub(psp.times[0]); gap < 4500*time.Millisecond {
			t.Errorf("first retry after %s, want the 5 s tier", gap)
		}
		if gap := psp.times[2].Sub(psp.times[1]); gap < 29*time.Second {
			t.Errorf("second retry after %s, want the 30 s tier", gap)
		}
		if psp.keys[0] != psp.keys[1] || psp.keys[1] != psp.keys[2] {
			t.Errorf("idempotency keys differ across retries: %v", psp.keys)
		}
	})
}

// A sandbox command never reaches the live provider, whatever is configured; a live one does.
func TestRoutingByEnvironment(t *testing.T) {
	pg := testkit.StartPostgres(t)
	db := testkit.MigratedDB(t, pg, "payments", migrations.FS)
	pool, err := pgxx.NewPool(context.Background(), db.System)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	psp := newScripted(t, 200)
	live := &recordingProvider{}
	h := &handler.Handler{Sandbox: payments.NewMockPSP(psp.srv.URL, time.Second), Live: live, Attempts: store.Attempts{DB: pool}}
	ctx := context.Background()

	decode := func(b []byte) *commandsv1.CommandEnvelope {
		var c commandsv1.CommandEnvelope
		_ = proto.Unmarshal(b, &c)
		return &c
	}
	r, err := h.Handle(ctx, decode(chargeCmd(plansv1.Environment_ENVIRONMENT_SANDBOX, uuid.New(), uuid.New(), 0)))
	if err != nil || r.GetPaymentSucceeded().GetPspChargeId() != "ch_scripted" {
		t.Fatalf("sandbox charge = %v, %v", r, err)
	}
	if live.calls.Load() != 0 {
		t.Fatalf("the live provider was called %d times for a sandbox command", live.calls.Load())
	}
	r, err = h.Handle(ctx, decode(chargeCmd(plansv1.Environment_ENVIRONMENT_LIVE, uuid.New(), uuid.New(), 0)))
	if err != nil || r.GetPaymentSucceeded().GetPspChargeId() != "live_ch" || live.calls.Load() != 1 {
		t.Fatalf("live charge = %v, %v (live calls %d)", r, err, live.calls.Load())
	}

	// No live provider configured: a permanent PaymentErrored, never a call.
	h.Live = nil
	r, err = h.Handle(ctx, decode(chargeCmd(plansv1.Environment_ENVIRONMENT_LIVE, uuid.New(), uuid.New(), 0)))
	if err != nil || r.GetPaymentErrored().GetCode() != handler.CodeNotConfigured || r.GetPaymentErrored().GetRetryable() {
		t.Fatalf("unconfigured live charge = %v, %v", r, err)
	}

	// The other direction: live never reaches mock-psp. With the mock as the live provider and no statement that this is not production, a live command is
	// refused (a permanent error, not a retry) and mock-psp is not called at all; a command without an environment never gets as far as a provider.
	mockHits := newScripted(t, 200)
	hMock := &handler.Handler{Sandbox: payments.NewMockPSP(psp.srv.URL, time.Second), Live: payments.NewMockPSP(mockHits.srv.URL, time.Second), Attempts: store.Attempts{DB: pool}}
	r, err = hMock.Handle(ctx, decode(chargeCmd(plansv1.Environment_ENVIRONMENT_LIVE, uuid.New(), uuid.New(), 0)))
	if err != nil || r.GetPaymentErrored().GetCode() != handler.CodeNotConfigured || r.GetPaymentErrored().GetRetryable() {
		t.Fatalf("live charge to the mock = %v, %v; want a permanent PROVIDER_NOT_CONFIGURED", r, err)
	}
	if _, err = hMock.Handle(ctx, decode(chargeCmd(plansv1.Environment_ENVIRONMENT_UNSPECIFIED, uuid.New(), uuid.New(), 0))); !errors.Is(err, workerx.ErrPoison) {
		t.Fatalf("a command without an environment = %v, want poison", err)
	}
	mockHits.mu.Lock()
	mockCalls := len(mockHits.times)
	mockHits.mu.Unlock()
	if mockCalls != 0 {
		t.Fatalf("mock-psp was called %d times for live commands", mockCalls)
	}
	hMock.AllowMockLive = true // a local stack: the same command goes through the mock
	if r, err = hMock.Handle(ctx, decode(chargeCmd(plansv1.Environment_ENVIRONMENT_LIVE, uuid.New(), uuid.New(), 0))); err != nil || r.GetPaymentSucceeded() == nil {
		t.Fatalf("allowed live charge to the mock = %v, %v", r, err)
	}

	// Error mapping: 402 → declined, a timeout → errored (not retryable), status lookups.
	declined := newScripted(t, 402)
	h.Sandbox = payments.NewMockPSP(declined.srv.URL, time.Second)
	r, err = h.Handle(ctx, decode(chargeCmd(plansv1.Environment_ENVIRONMENT_SANDBOX, uuid.New(), uuid.New(), 0)))
	if err != nil || r.GetPaymentDeclined().GetDeclineCode() != "card_declined" {
		t.Fatalf("declined charge = %v, %v", r, err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(1500 * time.Millisecond) }))
	defer slow.Close()
	h.Sandbox = payments.NewMockPSP(slow.URL, 300*time.Millisecond)
	r, err = h.Handle(ctx, decode(chargeCmd(plansv1.Environment_ENVIRONMENT_SANDBOX, uuid.New(), uuid.New(), 0)))
	if err != nil || r.GetPaymentErrored().GetCode() != handler.CodeTimeout || r.GetPaymentErrored().GetRetryable() {
		t.Fatalf("timed-out charge = %v, %v; want a non-retryable PaymentErrored (billing queries the status)", r, err)
	}
	found := newScripted(t, 200)
	found.status = func(key string) (int, map[string]any) {
		return 200, map[string]any{"found": true, "id": "ch_found", "status": "succeeded"}
	}
	h.Sandbox = payments.NewMockPSP(found.srv.URL, time.Second)
	statusCmd, _ := proto.Marshal(&commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: uuid.NewString(), Environment: plansv1.Environment_ENVIRONMENT_SANDBOX,
		Command: &commandsv1.CommandEnvelope_QueryPaymentStatus{QueryPaymentStatus: &commandsv1.QueryPaymentStatus{InvoiceId: uuid.NewString(), IdempotencyKey: "k"}}})
	r, err = h.Handle(ctx, decode(statusCmd))
	if err != nil || r.GetPaymentStatus().GetState() != commandsv1.PaymentStatus_STATE_SUCCEEDED || r.GetPaymentStatus().GetPspChargeId() != "ch_found" {
		t.Fatalf("status = %v, %v", r, err)
	}
}
