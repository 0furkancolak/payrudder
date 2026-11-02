//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/stubworker"
	"github.com/0furkancolak/payrudder/pkg/testkit"
)

// An invoice whose payment is unknown (the charge failed with an error and the status query is never answered) sits in PAYMENT_UNKNOWN. The operator can void it, which plan 22
// said they could: but the provider may have taken the money, so the call needs the operator's word that nothing was charged. Without it nothing changes; with it the invoice
// is void, nothing is refunded, and the confirmation and the reason are in the audit log.
func testAParkedUnknownPaymentCanBeVoidedOnlyWhenTheOperatorConfirms(t *testing.T) {
	e := newEnv(t, envOpts{})
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, _ int) stubworker.Action {
		switch stubworker.Kind(c) {
		case "charge":
			return stubworker.Action{Reply: stubworker.Errored}
		case "status":
			return stubworker.Action{} // the provider does not answer
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.250", 3)
	e.driveRun("2026-10")
	testkit.Eventually(t, 30*time.Second, "the saga to wait for the status of the payment", func() bool { return e.sagaStates("2026-10")["cus_a"] == "PAYMENT_UNKNOWN" })
	svc := e.api()
	var inv uuid.UUID
	if err := shared.sql.QueryRow(context.Background(), `SELECT id FROM invoices WHERE tenant_id = $1 AND snapshot->>'customer_external_id' = 'cus_a'`, e.tid).Scan(&inv); err != nil {
		t.Fatal(err)
	}
	void := func(confirm bool, reason string) error {
		_, err := svc.VoidInvoice(opCtx(), &billingv1.VoidInvoiceRequest{TenantId: e.tid.String(), InvoiceId: inv.String(), Reason: reason, ConfirmNotCharged: confirm})
		return err
	}

	keyOf(t, void(false, "stuck"), codes.FailedPrecondition, "billing.saga.void_needs_confirmation")
	if got := e.sagaStates("2026-10")["cus_a"]; got != "PAYMENT_UNKNOWN" {
		t.Fatalf("saga after a void without the confirmation = %s, want PAYMENT_UNKNOWN", got)
	}
	var voids int
	if err := shared.sql.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE tenant_id = $1 AND action = 'invoice.void'`, e.tid).Scan(&voids); err != nil || voids != 0 {
		t.Fatalf("audit entries after a refused void = %d, %v; want none", voids, err)
	}

	if err := void(true, "checked in the provider's dashboard: no charge"); err != nil {
		t.Fatalf("void with the confirmation: %v", err)
	}
	if got := e.sagaStates("2026-10")["cus_a"]; got != "VOIDED" {
		t.Fatalf("saga = %s, want VOIDED", got)
	}
	var status string
	if err := shared.sql.QueryRow(context.Background(), `SELECT status FROM invoices WHERE tenant_id = $1 AND id = $2`, e.tid, inv).Scan(&status); err != nil || status != "void" {
		t.Fatalf("invoice status = %q, %v; want void", status, err)
	}
	if n := stub.Count("refund"); n != 0 {
		t.Errorf("refund commands = %d; nothing is known to have been charged, so nothing is refunded", n)
	}
	var confirmed bool
	var reason string
	if err := shared.sql.QueryRow(context.Background(), `SELECT COALESCE((detail->>'confirmed_not_charged')::boolean, false), COALESCE(detail->>'reason', '') FROM audit_events
		WHERE tenant_id = $1 AND action = 'invoice.void' AND target_id = $2`, e.tid, inv.String()).Scan(&confirmed, &reason); err != nil || !confirmed || reason != "checked in the provider's dashboard: no charge" {
		t.Errorf("audit entry: confirmed %v, reason %q, %v; want the confirmation and the reason", confirmed, reason, err)
	}
	// Voided once: the repeat is refused as for any other void.
	keyOf(t, void(true, "again"), codes.FailedPrecondition, "billing.saga.event_not_possible")
}
