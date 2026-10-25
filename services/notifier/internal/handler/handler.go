// Package handler sends the emails: SendNotification commands from billing-svc.
package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/notifier/internal/mail"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
	"github.com/0furkancolak/payrudder/services/notifier/internal/templates"
)

// ErrInFlight means another delivery is sending this email; the command is retried.
var ErrInFlight = errors.New("notification is being sent by another delivery")

// Mailer sends rendered emails, routing sandbox mail to the sandbox sink.
type Mailer struct {
	Default mail.Sender
	// Sandbox, when set, receives every sandbox tenant's mail instead of the default sender: in any non-local
	// deployment sandbox notifications must never reach a customer's real address (ADR 0011).
	Sandbox mail.Sender
	From    string
	Notes   store.Notifications
	// Switch, when set, is asked before a send whether the tenant has the mail's category on (see Permitted).
	Switch Switch
}

// Deliver sends one rendered email under a dedup key. The returned id is the SMTP message id (also for an email that
// was already sent). Unsent failures release the claim so the retry starts clean.
func (m *Mailer) Deliver(ctx context.Context, key string, commandID *uuid.UUID, kind, template, to string, data map[string]string, sandbox bool) (string, error) {
	msg, err := templates.Render(template, data, sandbox)
	if err != nil {
		return "", fmt.Errorf("%w: %w", workerx.ErrPoison, err)
	}
	claim, id, err := m.Notes.Claim(ctx, key, commandID, kind, template, to)
	if err != nil {
		return "", err
	}
	switch claim {
	case store.AlreadySent:
		return id, nil
	case store.InFlight:
		return "", ErrInFlight
	}
	sender := m.Default
	if sandbox && m.Sandbox != nil {
		sender = m.Sandbox
	}
	mid, err := sender.Send(ctx, mail.Message{From: m.From, To: to, Subject: msg.Subject, HTML: msg.HTML, Text: msg.Text, ID: key})
	if err != nil {
		var ns *mail.NotSentError
		if errors.As(err, &ns) {
			_ = m.Notes.Release(ctx, key)
		}
		return "", err
	}
	if err := m.Notes.MarkSent(ctx, key, mid); err != nil {
		return "", err
	}
	return mid, nil
}

// Handler executes SendNotification commands.
type Handler struct{ Mailer *Mailer }

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
	tenantID, _ := uuid.Parse(cmd.GetTenantId())
	ok, err := h.Mailer.Permitted(ctx, tenantID, n.GetTemplate())
	if err != nil {
		return nil, err
	}
	if !ok {
		// The tenant switched this kind of mail off: the command is answered as done, so that the saga goes on.
		return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_NotificationSent{NotificationSent: &commandsv1.NotificationSent{Suppressed: true}}}, nil
	}
	mid, err := h.Mailer.Deliver(ctx, cmd.GetCommandId(), &id, "command", n.GetTemplate(), n.GetTo(), n.GetData(), cmd.GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX)
	if err != nil {
		return nil, err
	}
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_NotificationSent{NotificationSent: &commandsv1.NotificationSent{MessageId: mid}}}, nil
}
