package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/workerx"
)

// fakeReal stands in for a real provider (iyzico, Paddle): not the mock, and it records that it was called.
type fakeReal struct{ calls int }

func (f *fakeReal) Name() string { return "real" }
func (f *fakeReal) Charge(context.Context, payments.ChargeRequest) (payments.ChargeResult, error) {
	f.calls++
	return payments.ChargeResult{ChargeID: "real_ch"}, nil
}
func (f *fakeReal) Refund(context.Context, payments.RefundRequest) (payments.RefundResult, error) {
	f.calls++
	return payments.RefundResult{RefundID: "real_re"}, nil
}
func (f *fakeReal) Status(context.Context, string) (payments.StatusResult, error) {
	f.calls++
	return payments.StatusResult{State: payments.StateNotFound}, nil
}

func TestDemoModeNeverLeavesTheSandboxProvider(t *testing.T) {
	sandbox, live := payments.NewMockPSP("http://sandbox", 0), payments.NewMockPSP("http://live", 0)
	for _, demo := range []bool{false, true} {
		// AllowMockLive is what a local stack sets so that live may be the mock at all; the demo needs nothing.
		h := &Handler{Sandbox: sandbox, Live: live, Demo: demo, AllowMockLive: true}
		// A live tenant that names a provider of its own.
		got, err := h.providerFor(context.Background(), uuid.New(), env.Live, uuid.New(), true, "iyzico")
		if err != nil {
			t.Fatal(err)
		}
		if want := map[bool]payments.PaymentProvider{false: live, true: sandbox}[demo]; got != want {
			t.Errorf("demo=%v: provider %p, want %p", demo, got, want)
		}
	}
}

// The routing rule has two directions and both are one code path: sandbox is served by the sandbox provider and nothing else, and live is never served by the
// mock unless the stack says it has no real provider.
func TestRoutingNeverCrossesTheEnvironments(t *testing.T) {
	mock, real := payments.NewMockPSP("http://sandbox", 0), &fakeReal{}
	ctx := context.Background()

	// Sandbox, with a real provider configured for live: mock-psp, whatever the command names.
	h := &Handler{Sandbox: mock, Live: real}
	for _, named := range []string{"", "real", "iyzico"} {
		got, err := h.providerFor(ctx, uuid.New(), env.Sandbox, uuid.New(), true, named)
		if err != nil || got != payments.PaymentProvider(mock) {
			t.Errorf("sandbox command naming %q: provider %v, %v; want mock-psp", named, got, err)
		}
	}
	// Live, real provider: the real one.
	if got, err := h.providerFor(ctx, uuid.New(), env.Live, uuid.New(), true, ""); err != nil || got != payments.PaymentProvider(real) {
		t.Errorf("live command: provider %v, %v; want the real provider", got, err)
	}

	// Live, the mock as the live provider and no allowance: refused, never executed.
	h = &Handler{Sandbox: mock, Live: mock}
	if got, err := h.providerFor(ctx, uuid.New(), env.Live, uuid.New(), true, ""); !errors.Is(err, ErrWrongProvider) || got != nil {
		t.Errorf("live command routed to the mock: %v, %v; want ErrWrongProvider", got, err)
	}
	// The same command through Handle is answered, not retried and not executed.
	reply, err := h.Handle(ctx, &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: uuid.NewString(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
		Command: &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{InvoiceId: uuid.NewString(), IdempotencyKey: "k", AmountMinor: 1, Currency: "USD"}}})
	if err != nil || reply.GetPaymentErrored().GetCode() != CodeNotConfigured || reply.GetPaymentErrored().GetRetryable() {
		t.Errorf("live charge to the mock: %v, %v; want a permanent PROVIDER_NOT_CONFIGURED", reply, err)
	}
	// ...but with the allowance (a local stack) it goes to the mock, and the demo serves everyone from it.
	h = &Handler{Sandbox: mock, Live: mock, AllowMockLive: true}
	if got, err := h.providerFor(ctx, uuid.New(), env.Live, uuid.New(), true, ""); err != nil || got != payments.PaymentProvider(mock) {
		t.Errorf("allowed: %v, %v", got, err)
	}
}

func chargeFor(environment plansv1.Environment, tenant string) *commandsv1.CommandEnvelope {
	return &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: tenant, Environment: environment,
		Command: &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{InvoiceId: uuid.NewString(), IdempotencyKey: "k", AmountMinor: 1, Currency: "USD"}}}
}

// A command with no environment is dead-lettered, not read as live; so is one whose tenant is in the other environment. A lookup that fails is retried.
func TestCommandsWithoutOrAgainstTheirTenantsEnvironmentAreRefused(t *testing.T) {
	real := &fakeReal{}
	liveTenant, sandboxTenant := uuid.New(), uuid.New()
	var lookupErr error
	dir := env.NewDirectory(func(_ context.Context, id uuid.UUID) (env.Env, error) {
		if lookupErr != nil {
			return "", lookupErr
		}
		if id == sandboxTenant {
			return env.Sandbox, nil
		}
		return env.Live, nil
	}, 0)
	h := &Handler{Sandbox: payments.NewMockPSP("http://sandbox", 0), Live: real, Tenants: dir}
	ctx := context.Background()

	// No environment on the command.
	if _, err := h.Handle(ctx, chargeFor(plansv1.Environment_ENVIRONMENT_UNSPECIFIED, liveTenant.String())); !errors.Is(err, workerx.ErrPoison) || !errors.Is(err, env.ErrMissing) {
		t.Errorf("no environment: %v, want poison (missing)", err)
	}
	// A live command for a sandbox tenant, and a sandbox command for a live one.
	if _, err := h.Handle(ctx, chargeFor(plansv1.Environment_ENVIRONMENT_LIVE, sandboxTenant.String())); !errors.Is(err, workerx.ErrPoison) || !errors.Is(err, env.ErrMismatch) {
		t.Errorf("live command, sandbox tenant: %v, want poison (mismatch)", err)
	}
	if _, err := h.Handle(ctx, chargeFor(plansv1.Environment_ENVIRONMENT_SANDBOX, liveTenant.String())); !errors.Is(err, workerx.ErrPoison) || !errors.Is(err, env.ErrMismatch) {
		t.Errorf("sandbox command, live tenant: %v, want poison (mismatch)", err)
	}
	if _, err := h.Handle(ctx, chargeFor(plansv1.Environment_ENVIRONMENT_LIVE, "not-a-uuid")); !errors.Is(err, workerx.ErrPoison) {
		t.Errorf("a command without a tenant: %v, want poison", err)
	}
	if real.calls != 0 {
		t.Errorf("the real provider was called %d times for refused commands", real.calls)
	}
	// plans-svc is down: not poison, the queue retries.
	lookupErr = errors.New("plans-svc is down")
	if _, err := h.Handle(ctx, chargeFor(plansv1.Environment_ENVIRONMENT_LIVE, uuid.NewString())); err == nil || errors.Is(err, workerx.ErrPoison) {
		t.Errorf("lookup failure: %v, want a retryable error", err)
	}
	// Without a directory (no plans-svc address) only the presence of an environment is checked.
	h.Tenants = nil
	if _, err := h.Handle(ctx, chargeFor(plansv1.Environment_ENVIRONMENT_UNSPECIFIED, liveTenant.String())); !errors.Is(err, workerx.ErrPoison) {
		t.Errorf("no directory, no environment: %v, want poison", err)
	}
}
