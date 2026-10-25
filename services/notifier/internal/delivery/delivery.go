// Package delivery sends the notifier's emails: it claims the dedup key, renders, picks the sender (sandbox mail goes to the sandbox sink),
// asks the tenant's mail switch, and records the send. The command handler and the limit alert consumer both deliver through it.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
	"github.com/0furkancolak/payrudder/services/notifier/internal/templates"
)

// ErrInFlight means another delivery is sending this email; the command is retried.
var ErrInFlight = errors.New("notification is being sent by another delivery")

// Mailer sends rendered emails, routing sandbox mail to the sandbox sink.
type Mailer struct {
	Default mailx.Sender
	// Sandbox, when set, receives every sandbox tenant's mail instead of the default sender: in any non-local
	// deployment sandbox notifications must never reach a customer's real address (ADR 0011).
	Sandbox mailx.Sender
	From    string
	Notes   store.Notifications
	// Switch, when set, is asked before a send whether the tenant has the mail's category on (see Permitted).
	Switch Switch
}

// Delivery is one email to send.
type Delivery struct {
	// Key is the dedup key: sending it twice sends one email.
	Key string
	// CommandID is the command that asked for the email; nil when no command did (a limit alert).
	CommandID *uuid.UUID
	// Kind is what asked for it ("command" or "limit").
	Kind     string
	Template string
	To       string
	Data     map[string]string
	// Sandbox sends through the sandbox sink, with a marked subject.
	Sandbox bool
}

// Deliver sends one rendered email under its dedup key. The returned id is the SMTP message id (also for an email that
// was already sent). Unsent failures release the claim so the retry starts clean.
func (m *Mailer) Deliver(ctx context.Context, d Delivery) (string, error) {
	msg, err := templates.Render(d.Template, d.Data, d.Sandbox)
	if err != nil {
		return "", fmt.Errorf("%w: %w", workerx.ErrPoison, err)
	}
	claim, id, err := m.Notes.Claim(ctx, d.Key, d.CommandID, d.Kind, d.Template, d.To)
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
	if d.Sandbox && m.Sandbox != nil {
		sender = m.Sandbox
	}
	mid, err := sender.Send(ctx, mailx.Message{From: m.From, To: d.To, Subject: msg.Subject, HTML: msg.HTML, Text: msg.Text, ID: d.Key})
	if err != nil {
		var ns *mailx.NotSentError
		if errors.As(err, &ns) {
			if rerr := m.Notes.Release(ctx, d.Key); rerr != nil {
				slog.WarnContext(ctx, "claim of an unsent email not released, it stays SENDING until it goes stale", "key", d.Key, "error", rerr)
			}
		}
		return "", err
	}
	if err := m.Notes.MarkSent(ctx, d.Key, mid); err != nil {
		return "", err
	}
	return mid, nil
}
