// Package model is what the webhook module shares: the event types a tenant can subscribe to, the body of a delivery, the associated data an endpoint
// secret is sealed with and the wait between attempts.
package model

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
)

// Event types a tenant can subscribe an endpoint to. An endpoint with no types gets all of them.
//
//nolint:gosec // event type names (credit_note.*), not credentials
const (
	Ping                        = "ping"
	InvoiceFinalized            = "invoice.finalized"
	InvoicePaid                 = "invoice.paid"
	InvoiceVoided               = "invoice.voided"
	InvoiceUncollectible        = "invoice.uncollectible"
	CreditNoteCreated           = "credit_note.created"
	CreditNoteRefunded          = "credit_note.refunded"
	CreditNoteRefundFailed      = "credit_note.refund_failed"
	SubscriptionCreated         = "subscription.created"
	SubscriptionChanged         = "subscription.changed"
	SubscriptionChangeScheduled = "subscription.change_scheduled"
	SubscriptionCancelled       = "subscription.cancelled"
	SubscriptionCancelScheduled = "subscription.cancellation_scheduled"
	SubscriptionCancelWithdrawn = "subscription.cancellation_withdrawn"
	SubscriptionPaused          = "subscription.paused"
	SubscriptionResumed         = "subscription.resumed"
	SubscriptionMovedToProvider = "subscription.moved_to_provider"
	SubscriptionTrialWillEnd    = "subscription.trial_will_end"
	PaymentLinkCreated          = "payment_link.created"
	PaymentLinkPaid             = "payment_link.paid"
	PaymentLinkExpired          = "payment_link.expired"
	CreditsPurchased            = "credits.purchased"
	CreditsLow                  = "credits.low"
)

// Types lists every event type, in the order the documentation gives them.
var Types = []string{
	InvoiceFinalized, InvoicePaid, InvoiceVoided, InvoiceUncollectible,
	CreditNoteCreated, CreditNoteRefunded, CreditNoteRefundFailed,
	SubscriptionCreated, SubscriptionChanged, SubscriptionChangeScheduled, SubscriptionCancelled, SubscriptionCancelScheduled, SubscriptionCancelWithdrawn,
	SubscriptionPaused, SubscriptionResumed, SubscriptionMovedToProvider, SubscriptionTrialWillEnd,
	PaymentLinkCreated, PaymentLinkPaid, PaymentLinkExpired,
	CreditsPurchased, CreditsLow,
}

// Known reports whether t is an event type (ping is sent only on request and cannot be subscribed to).
func Known(t string) bool { return slices.Contains(Types, t) }

// EventJSON writes API messages the way the API returns them, for the data of an event.
var EventJSON = protojson.MarshalOptions{UseProtoNames: true}

// Backoff is the wait before attempt 2, 3, ...; a delivery that fails after the last one is failed (7 attempts in about 32 hours).
var Backoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour, 24 * time.Hour}

// SecretAAD is the associated data an endpoint's secret is sealed with.
func SecretAAD(endpoint uuid.UUID) []byte { return []byte("webhook:" + endpoint.String()) }

// Envelope is the body of a delivery.
type Envelope struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Created  string          `json:"created"`
	TenantID string          `json:"tenant_id"`
	Data     json.RawMessage `json:"data"`
}
