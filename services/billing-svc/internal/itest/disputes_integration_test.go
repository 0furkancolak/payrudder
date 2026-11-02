//go:build integration

package itest

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/api"
)

// blocker is the part of plans-svc a dispute asks to block a customer.
type blocker struct {
	mu   sync.Mutex
	asks []*plansv1.BlockCustomerRequest
}

func (b *blocker) BlockCustomer(_ context.Context, in *plansv1.BlockCustomerRequest, _ ...grpc.CallOption) (*plansv1.BlockCustomerResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.asks = append(b.asks, in)
	return &plansv1.BlockCustomerResponse{}, nil
}

func (b *blocker) count() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.asks) }

// Each step of a dispute is announced once: the webhook payment.disputed (with where the case stands), one audit entry; a repeat of the step is told nothing new. The customer
// is blocked for a chargeback only when the seller turned that on in the dunning settings, only for a formal dispute that opens, and a request that names no setting leaves
// the setting as it was.
func testPaymentDisputesAreAnnouncedOncePerStepAndBlockWhenAsked(t *testing.T) {
	infra(t)
	ctx := context.Background()
	tid, customer := uuid.New(), uuid.New()
	invoice := seedOne(t, tid, customer, "cus_d", "paid", "USD", "INV-2026-000770", 1250, "live")
	plans := &blocker{}
	svc := api.New(api.Deps{App: shared.app, System: shared.system, Blocker: plans})
	tctx := tenant.NewContext(ctx, tenant.Info{ID: tid})
	step := func(ref, outcome string, formal bool) *billingv1.RecordPaymentDisputeResponse {
		t.Helper()
		r, err := svc.RecordPaymentDispute(tctx, &billingv1.RecordPaymentDisputeRequest{InvoiceId: invoice.String(), Provider: "stripe", DisputeRef: ref, PaymentRef: "pi_1", AmountMinor: 1250,
			Currency: "USD", Outcome: outcome, Formal: formal, Reason: "fraudulent", ProviderStatus: "needs_response"})
		if err != nil {
			t.Fatalf("%s %s: %v", ref, outcome, err)
		}
		return r
	}
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := shared.sql.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	events := func() int {
		return count(`SELECT count(*) FROM tenant_event_outbox WHERE tenant_id = $1 AND type = 'payment.disputed'`, tid)
	}
	audits := func(action string) int {
		return count(`SELECT count(*) FROM audit_events WHERE tenant_id = $1 AND action = $2 AND target_id = $3`, tid, action, invoice.String())
	}

	// Setting off (the default): announced, nobody blocked.
	if r := step("du_1", "open", true); !r.GetAnnounced() || r.GetCustomerBlocked() || plans.count() != 0 {
		t.Fatalf("first step = %v, %d blocks; want announced and no block", r, plans.count())
	}
	if events() != 1 || audits("payment.dispute_open") != 1 {
		t.Fatalf("events %d, audit entries %d; want 1 and 1", events(), audits("payment.dispute_open"))
	}
	var data string
	if err := shared.sql.QueryRow(ctx, `SELECT data::text FROM tenant_event_outbox WHERE tenant_id = $1 AND type = 'payment.disputed'`, tid).Scan(&data); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"dispute_id": "du_1"`, `"outcome": "open"`, `"amount_minor": "1250"`, `"customer_external_id": "cus_d"`, `"invoice_number": "INV-2026-000770"`, `"formal": true`, `"provider": "stripe"`} {
		if !strings.Contains(data, want) {
			t.Errorf("the event lacks %s: %s", want, data)
		}
	}
	// The same step again: not announced again, no second entry.
	if r := step("du_1", "open", true); r.GetAnnounced() || events() != 1 || audits("payment.dispute_open") != 1 {
		t.Fatalf("a repeat: %v, %d events, %d audit entries", r, events(), audits("payment.dispute_open"))
	}

	// The seller turns the setting on. A request that names no setting keeps it.
	set, err := svc.SetDunningSettings(tctx, &billingv1.SetDunningSettingsRequest{RetryHours: []int32{24}, BlockOnChargeback: ptr(true)})
	if err != nil || !set.GetSettings().GetBlockOnChargeback() {
		t.Fatalf("set: %v, %v", set, err)
	}
	if again, err := svc.SetDunningSettings(tctx, &billingv1.SetDunningSettingsRequest{RetryHours: []int32{24, 48}}); err != nil || !again.GetSettings().GetBlockOnChargeback() {
		t.Fatalf("a request without the setting: %v, %v; want it kept", again, err)
	}
	if got, err := svc.GetDunningSettings(tctx, &billingv1.GetDunningSettingsRequest{}); err != nil || !got.GetSettings().GetBlockOnChargeback() {
		t.Fatalf("get: %v, %v", got, err)
	}

	// A formal dispute that opens now blocks the customer, for a chargeback.
	if r := step("du_2", "open", true); !r.GetAnnounced() || !r.GetCustomerBlocked() || plans.count() != 1 {
		t.Fatalf("second case = %v, %d blocks; want announced and blocked", r, plans.count())
	}
	if ask := plans.asks[0]; ask.GetExternalId() != "cus_d" || ask.GetCategory() != "chargeback" || ask.GetReason() == "" {
		t.Errorf("the block = %v", ask)
	}
	// An inquiry, a win and a loss are announced and never block.
	step("du_3", "open", false)
	step("du_2", "won", true)
	step("du_1", "lost", true)
	if plans.count() != 1 {
		t.Errorf("blocks = %d, want only the one for the chargeback that opened", plans.count())
	}
	if events() != 5 || audits("payment.dispute_won") != 1 || audits("payment.dispute_lost") != 1 || audits("payment.dispute_open") != 3 {
		t.Errorf("events %d, audit: open %d, won %d, lost %d; want 5 events and 3, 1, 1", events(), audits("payment.dispute_open"), audits("payment.dispute_won"), audits("payment.dispute_lost"))
	}
	// An invoice that is not the tenant's is not found.
	if _, err := svc.RecordPaymentDispute(tctx, &billingv1.RecordPaymentDisputeRequest{InvoiceId: uuid.NewString(), Provider: "stripe", DisputeRef: "du_x", Outcome: "open"}); err == nil {
		t.Error("a dispute of an unknown invoice was taken")
	}
	if _, err := svc.RecordPaymentDispute(tctx, &billingv1.RecordPaymentDisputeRequest{InvoiceId: invoice.String(), Provider: "stripe", DisputeRef: "du_x", Outcome: "maybe"}); err == nil {
		t.Error("an outcome that is neither open, won nor lost was taken")
	}
}

func ptr[T any](v T) *T { return &v }
