// Package handler sends the emails: SendNotification commands from billing-svc.
package handler

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/notifier/internal/delivery"
)

// Handler executes SendNotification commands.
type Handler struct{ Mailer *delivery.Mailer }

// Handle implements workerx.Handler.
func (h *Handler) Handle(ctx context.Context, cmd *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error) {
	n := cmd.GetSendNotification()
	if n == nil {
		return nil, fmt.Errorf("%w: not a notification command", workerx.ErrPoison)
	}
	if n.GetTo() == "" || n.GetTemplate() == "" {
		return nil, fmt.Errorf("%w: notification needs a recipient and a template", workerx.ErrPoison)
	}
	id, err := uuid.Parse(cmd.GetCommandId())
	if err != nil {
		return nil, fmt.Errorf("%w: command_id", workerx.ErrPoison)
	}
	// A command without a tenant id is allowed (no switch to ask); one with an id that is not a UUID is poison, not a mail the switch is skipped for.
	var tenantID uuid.UUID
	if raw := cmd.GetTenantId(); raw != "" {
		if tenantID, err = uuid.Parse(raw); err != nil {
			return nil, fmt.Errorf("%w: tenant_id", workerx.ErrPoison)
		}
	}
	ok, err := h.Mailer.Permitted(ctx, tenantID, n.GetTemplate())
	if err != nil {
		return nil, err
	}
	if !ok {
		// The tenant switched this kind of mail off: the command is answered as done, so that the saga goes on.
		return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_NotificationSent{NotificationSent: &commandsv1.NotificationSent{Suppressed: true}}}, nil
	}
	mid, err := h.Mailer.Deliver(ctx, delivery.Delivery{
		Key: cmd.GetCommandId(), CommandID: &id, Kind: "command", Template: n.GetTemplate(), To: n.GetTo(), Data: n.GetData(),
		Sandbox: cmd.GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX,
	})
	if err != nil {
		return nil, err
	}
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_NotificationSent{NotificationSent: &commandsv1.NotificationSent{MessageId: mid}}}, nil
}
