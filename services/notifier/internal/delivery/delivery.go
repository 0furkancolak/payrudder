// Package delivery sends the notifier's emails: it claims the dedup key, renders, picks the sender (sandbox mail goes to the sandbox sink),
// asks the tenant's mail switch, the suppression list, the plan's allowance and the rate limit, and records the send. The command handler and the consumers all
// deliver through it.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/tenantinfo"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
	"github.com/0furkancolak/payrudder/services/notifier/internal/templates"
)

// ErrInFlight means another delivery is sending this email; the command is retried.
var ErrInFlight = errors.New("notification is being sent by another delivery")

// ErrRateLimited means the tenant sent more mail than the platform's sender allows it just now. It is an ordinary (retryable) error, not poison: the callers'
// own retries send the mail later. A command retries three times (5 s, 30 s, 2 min) and is then dead-lettered; a Kafka consumer holds its partition and retries
// the batch with backoff, so nothing is lost there.
var ErrRateLimited = errors.New("tenant mail rate limit reached; the mail is retried")

// Dropped is the id Deliver returns, with no error, for a mail it decided not to send (a suppressed address, an allowance used up): the caller treats the mail as
// done and a command answers "suppressed".
const Dropped = ""

var rateLimitedTotal = func() metric.Int64Counter {
	c, err := otel.Meter("github.com/0furkancolak/payrudder/services/notifier").Int64Counter("notifier_mail_rate_limited",
		metric.WithDescription("Mails put off because the tenant reached its send rate limit (they are retried)"))
	if err != nil {
		panic(err)
	}
	return c
}()

// Suppressions says whether mail to an address is suppressed for a tenant (it bounced for good, complained, or the operator said so).
type Suppressions interface {
	IsSuppressed(ctx context.Context, tenantID uuid.UUID, address string) (bool, error)
}

// Notes is the dedup record (store.Notifications): the right to send one email, and the proof that it went.
type Notes interface {
	Claim(ctx context.Context, key string, commandID *uuid.UUID, tenantID uuid.UUID, kind, template, recipient string) (store.Claim, string, error)
	MarkSent(ctx context.Context, key, messageID string) error
	Release(ctx context.Context, key string) error
}

// Mailer sends rendered emails, routing sandbox mail to the sandbox sink.
type Mailer struct {
	Default mailx.Sender
	// Sandbox, when set, receives every sandbox tenant's mail instead of the default sender: in any non-local
	// deployment sandbox notifications must never reach a customer's real address (ADR 0011).
	Sandbox mailx.Sender
	From    string
	Notes   Notes
	// Switch, when set, is asked before a send whether the tenant has the mail's category on (see Permitted).
	Switch Switch
	// Tenants, when set, is asked whether the tenant is in service; a tenant on hold (suspended, frozen, banned) sends no customer mail (see Permitted).
	Tenants tenantinfo.Source
	// Providers, when set, is asked before a live send whether the tenant connected its own e-mail provider; its mail then goes through it, from its own
	// address. Sandbox mail never does (ADR 0011).
	Providers Providers
	// Suppress, when set, is asked before a live send whether the address is on the tenant's suppression list.
	Suppress Suppressions
	// Quota, when set, limits and meters the live mail the platform's sender sends for a tenant (the `mails` meter of the plan). Mail through a tenant's own
	// provider is its own business and is not counted.
	Quota Quota
	// Limit, when set, caps the rate at which the platform's sender sends one tenant's live mail.
	Limit *Limiter
}

// Delivery is one email to send.
type Delivery struct {
	// Key is the dedup key: sending it twice sends one email.
	Key string
	// CommandID is the command that asked for the email; nil when no command did (a limit alert).
	CommandID *uuid.UUID
	// Kind is what asked for it ("command", "limit", "trial", "price_change", "contract", "renewal" or "withdrawal").
	Kind     string
	Template string
	// TenantID is the tenant the mail is sent for; uuid.Nil when there is none (no provider lookup then).
	TenantID uuid.UUID
	To       string
	Data     map[string]string
	// Sandbox sends through the sandbox sink, with a marked subject.
	Sandbox bool
}

// Deliver sends one rendered email under its dedup key. The returned id is the provider's message id (also for an email that
// was already sent); Dropped (empty) with no error means the mail was decided against and is done. Unsent failures release the claim so the retry starts clean.
func (m *Mailer) Deliver(ctx context.Context, d Delivery) (string, error) {
	msg, err := templates.Render(d.Template, d.Data, d.Sandbox)
	if err != nil {
		return "", fmt.Errorf("%w: %w", workerx.ErrPoison, err)
	}
	claim, id, err := m.Notes.Claim(ctx, d.Key, d.CommandID, d.TenantID, d.Kind, d.Template, d.To)
	if err != nil {
		return "", err
	}
	switch claim {
	case store.AlreadySent:
		return id, nil
	case store.InFlight:
		return "", ErrInFlight
	}
	sender, from := m.Default, m.From
	own := false // the tenant sends through its own provider
	if d.Sandbox && m.Sandbox != nil {
		sender = m.Sandbox
	}
	if !d.Sandbox && m.Providers != nil && d.TenantID != uuid.Nil {
		p, perr := m.Providers.For(ctx, d.TenantID)
		if perr != nil {
			// Not knowing the tenant's choice is not a reason to send through the other sender: the claim is released and the mail retried.
			m.release(ctx, d.Key)
			return "", perr
		}
		if p != nil {
			sender, from, own = p.Sender, p.From, true
		}
	}
	// Sandbox mail goes to a sink and is never suppressed, limited or counted; neither is mail that names no tenant.
	live := !d.Sandbox && d.TenantID != uuid.Nil
	if live {
		if drop, err := m.refuse(ctx, d, own); err != nil || drop {
			m.release(ctx, d.Key)
			return Dropped, err
		}
	}
	mid, err := sender.Send(ctx, mailx.Message{From: from, To: d.To, Subject: msg.Subject, HTML: msg.HTML, Text: msg.Text, ID: d.Key, Template: d.Template})
	if err != nil {
		var perm *mailx.PermanentError
		if errors.As(err, &perm) {
			// The tenant's provider refused it for good (a wrong key, an unverified sender): retrying cannot change that, so it ends here with the reason.
			m.release(ctx, d.Key)
			return "", fmt.Errorf("%w: %w", workerx.ErrPoison, err)
		}
		var ns *mailx.NotSentError
		if errors.As(err, &ns) {
			if rerr := m.Notes.Release(ctx, d.Key); rerr != nil {
				slog.WarnContext(ctx, "claim of an unsent email not released, it stays SENDING until it goes stale", "key", d.Key, "error", rerr)
			}
		}
		return "", err
	}
	if live && !own && m.Quota != nil {
		m.Quota.Count(ctx, d.TenantID, d.Key)
	}
	if err := m.Notes.MarkSent(ctx, d.Key, mid); err != nil {
		return "", err
	}
	return mid, nil
}

// refuse decides, for a live mail of a tenant, whether it is not sent now: because the address is suppressed or the plan's allowance is used up (drop, no
// error: the mail is done) or because the tenant sends too fast (ErrRateLimited). An error from the suppression list is returned too: not knowing is not a reason
// to mail an address that may have complained.
func (m *Mailer) refuse(ctx context.Context, d Delivery, own bool) (drop bool, err error) {
	if m.Suppress != nil {
		sup, err := m.Suppress.IsSuppressed(ctx, d.TenantID, d.To)
		if err != nil {
			return false, fmt.Errorf("suppression list lookup: %w", err)
		}
		if sup {
			droppedTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("category", "suppressed")))
			return true, nil
		}
	}
	if own {
		return false, nil
	}
	if m.Quota != nil && !m.Quota.Allow(ctx, d.TenantID) {
		droppedTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("category", "quota")))
		return true, nil
	}
	if !m.Limit.Allow(ctx, d.TenantID) {
		rateLimitedTotal.Add(ctx, 1)
		return false, ErrRateLimited
	}
	return false, nil
}

func (m *Mailer) release(ctx context.Context, key string) {
	if err := m.Notes.Release(ctx, key); err != nil {
		slog.WarnContext(ctx, "claim of an unsent email not released, it stays SENDING until it goes stale", "key", key, "error", err)
	}
}
