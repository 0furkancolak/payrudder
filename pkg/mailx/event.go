package mailx

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// EventType is what happened to a message after the provider accepted it.
type EventType string

// The event types the notifier understands. They are modelled on the names of Resend's public webhook events (email.delivered, email.delivery_delayed,
// email.bounced, email.complained; read 2026-11-02 at https://resend.com/docs/dashboard/webhooks/event-types).
const (
	EventDelivered  EventType = "delivered"
	EventDelayed    EventType = "delayed"
	EventBounced    EventType = "bounced"
	EventComplained EventType = "complained"
)

// Bounce types of a bounced event. Resend names them "Permanent" and "Temporary" in its example payload; a Nafru translating layer maps its own.
const (
	BounceHard = "hard"
	BounceSoft = "soft"
)

// Event is Payrudder's neutral delivery event, the body of POST /v1/mail-events on the notifier. It is NOT a provider's payload: Resend wraps the same facts in
// {"type":"email.bounced","data":{"email_id":…,"to":[…],"bounce":{"type":"Permanent"}}} and signs it with Svix, and Nafru's format and signature are UNVERIFIED, so a
// provider's webhook reaches the notifier through a small translating layer that builds this and signs it with the shared secret (pkg/webhooksig).
type Event struct {
	Type EventType `json:"type"`
	// MessageID is the id the provider returned when it accepted the message (the notifier stored it as the message's id).
	MessageID string `json:"message_id"`
	// Recipient, when the provider names one, must be the address the message went to; an event that names another address is ignored.
	Recipient string `json:"recipient,omitempty"`
	// BounceType is "hard" or "soft" and is required on a bounced event: a bounce that is not known to be permanent never suppresses an address.
	BounceType string    `json:"bounce_type,omitempty"`
	OccurredAt time.Time `json:"occurred_at,omitempty"`
	// Detail is the provider's explanation, short, for the operator.
	Detail string `json:"detail,omitempty"`
}

// Validate checks an event at the trust boundary.
func (e Event) Validate() error {
	switch e.Type {
	case EventDelivered, EventDelayed, EventComplained:
	case EventBounced:
		if e.BounceType != BounceHard && e.BounceType != BounceSoft {
			return fmt.Errorf("a bounced event needs bounce_type %q or %q", BounceHard, BounceSoft)
		}
	default:
		return fmt.Errorf("unknown event type %q", e.Type)
	}
	if strings.TrimSpace(e.MessageID) == "" {
		return errors.New("message_id is required")
	}
	return nil
}

// Suppresses reports whether the event puts the address on the suppression list: a permanent bounce and a complaint do, nothing else does.
func (e Event) Suppresses() bool {
	return e.Type == EventComplained || (e.Type == EventBounced && e.BounceType == BounceHard)
}
