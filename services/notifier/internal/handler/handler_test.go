package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/mailcategory"
	"github.com/0furkancolak/payrudder/services/notifier/internal/delivery"
)

type stubSwitch struct{ off map[string]bool }

func (s stubSwitch) Enabled(_ context.Context, _ uuid.UUID, category string) (bool, error) {
	return !s.off[category], nil
}

// A command for a switched-off category is answered as done and suppressed, and nothing else happens: the Mailer has no store and no sender, so any
// attempt to claim or send would panic.
func TestHandleDropsDisabledCategory(t *testing.T) {
	m := &delivery.Mailer{Switch: stubSwitch{off: map[string]bool{mailcategory.PaymentReceipts: true}}}
	cmd := &commandsv1.CommandEnvelope{
		CommandId: uuid.NewString(), TenantId: uuid.NewString(),
		Command: &commandsv1.CommandEnvelope_SendNotification{SendNotification: &commandsv1.SendNotification{Template: "invoice_paid.v1", To: "a@example.com"}},
	}
	reply, err := (&Handler{Mailer: m}).Handle(context.Background(), cmd)
	if err != nil {
		t.Fatalf("a dropped mail is not an error: %v", err)
	}
	if got := reply.GetNotificationSent(); got == nil || !got.GetSuppressed() || got.GetMessageId() != "" {
		t.Fatalf("reply = %v, want a suppressed NotificationSent", reply)
	}
}
