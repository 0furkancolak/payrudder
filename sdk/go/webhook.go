package payrudder

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Header names of a webhook delivery.
const (
	// WebhookSignatureHeader carries t=<unix>,v1=<hex>[,v1=<hex>].
	WebhookSignatureHeader = "Payrudder-Signature"
	WebhookEventIDHeader   = "Payrudder-Event-Id"
	WebhookEventTypeHeader = "Payrudder-Event-Type"
	WebhookAttemptHeader   = "Payrudder-Delivery-Attempt"

	// DefaultWebhookTolerance is how far the signed timestamp may be from the local clock.
	DefaultWebhookTolerance = 5 * time.Minute
)

// Event types a webhook endpoint can receive (docs/16-webhooks.md).
const (
	EventInvoiceFinalized                = "invoice.finalized"
	EventInvoicePaid                     = "invoice.paid"
	EventInvoiceVoided                   = "invoice.voided"
	EventInvoiceUncollectible            = "invoice.uncollectible"
	EventCreditNoteCreated               = "credit_note.created"
	EventCreditNoteRefunded              = "credit_note.refunded"      //nolint:gosec // an event type
	EventCreditNoteRefundFailed          = "credit_note.refund_failed" //nolint:gosec // an event type
	EventSubscriptionCreated             = "subscription.created"
	EventSubscriptionChanged             = "subscription.changed"
	EventSubscriptionChangeScheduled     = "subscription.change_scheduled"
	EventSubscriptionCancelled           = "subscription.cancelled"
	EventSubscriptionCancellationSet     = "subscription.cancellation_scheduled"
	EventSubscriptionCancellationDropped = "subscription.cancellation_withdrawn"
	EventSubscriptionPaused              = "subscription.paused"
	EventSubscriptionResumed             = "subscription.resumed"
	EventSubscriptionMovedToProvider     = "subscription.moved_to_provider"
	EventSubscriptionTrialWillEnd        = "subscription.trial_will_end"
	EventPaymentLinkCreated              = "payment_link.created"
	EventPaymentLinkPaid                 = "payment_link.paid"
	EventPaymentLinkExpired              = "payment_link.expired"
	EventCreditsPurchased                = "credits.purchased"
	EventCreditsLow                      = "credits.low"
	// EventPing is what POST /v1/webhook-endpoints/{id}/test sends.
	EventPing = "ping"
)

// WebhookEventTypes lists every type above except ping, for building an endpoint's subscription list.
var WebhookEventTypes = []string{
	EventInvoiceFinalized, EventInvoicePaid, EventInvoiceVoided, EventInvoiceUncollectible,
	EventCreditNoteCreated, EventCreditNoteRefunded, EventCreditNoteRefundFailed,
	EventSubscriptionCreated, EventSubscriptionChanged, EventSubscriptionChangeScheduled, EventSubscriptionCancelled,
	EventSubscriptionCancellationSet, EventSubscriptionCancellationDropped, EventSubscriptionPaused,
	EventSubscriptionResumed, EventSubscriptionMovedToProvider, EventSubscriptionTrialWillEnd,
	EventPaymentLinkCreated, EventPaymentLinkPaid, EventPaymentLinkExpired,
	EventCreditsPurchased, EventCreditsLow,
}

// Reasons a delivery is refused. Test with errors.Is.
var (
	// ErrWebhookHeader means the signature header is missing or malformed.
	ErrWebhookHeader = errors.New("payrudder: malformed webhook signature header")
	// ErrWebhookTimestamp means the signed time is further from now than the tolerance (a replay, or a wrong clock).
	ErrWebhookTimestamp = errors.New("payrudder: webhook timestamp outside the tolerance")
	// ErrWebhookSignature means no v1 signature matches the payload: wrong secret or a changed body.
	ErrWebhookSignature = errors.New("payrudder: webhook signature does not match")
)

// VerifyWebhook checks the Payrudder-Signature header of a delivery against its raw body: HMAC-SHA256 of
// "<t>.<payload>" with the endpoint secret (whsec_...), compared in constant time. Any of the v1 signatures may match,
// which is how a delivery stays valid while a rotated secret is still accepted. A timestamp more than tolerance from
// the local clock is refused; tolerance 0 means five minutes.
//
// payload must be the exact bytes received, not a re-encoded JSON value.
func VerifyWebhook(payload []byte, header, secret string, tolerance time.Duration) error {
	return VerifyWebhookAt(payload, header, secret, tolerance, time.Now())
}

// VerifyWebhookAt is VerifyWebhook with an explicit clock, for tests.
func VerifyWebhookAt(payload []byte, header, secret string, tolerance time.Duration, now time.Time) error {
	if tolerance <= 0 {
		tolerance = DefaultWebhookTolerance
	}
	ts, sigs, err := parseSignatureHeader(header)
	if err != nil {
		return err
	}
	if diff := now.Sub(time.Unix(ts, 0)); diff > tolerance || diff < -tolerance {
		return ErrWebhookTimestamp
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte{'.'})
	mac.Write(payload)
	want := mac.Sum(nil)
	matched := false
	for _, s := range sigs {
		// Every signature is compared so the time does not tell which one matched.
		if hmac.Equal(s, want) {
			matched = true
		}
	}
	if !matched {
		return ErrWebhookSignature
	}
	return nil
}

func parseSignatureHeader(h string) (int64, [][]byte, error) {
	var ts int64
	haveTS := false
	var sigs [][]byte
	for _, part := range strings.Split(h, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return 0, nil, fmt.Errorf("%w: bad timestamp", ErrWebhookHeader)
			}
			if !haveTS {
				ts, haveTS = n, true
			}
		case "v1":
			// A value that is not hex cannot match; skip it rather than refuse a header that has another good one.
			if b, err := hex.DecodeString(v); err == nil {
				sigs = append(sigs, b)
			}
		}
	}
	if !haveTS {
		return 0, nil, fmt.Errorf("%w: no t", ErrWebhookHeader)
	}
	if len(sigs) == 0 {
		return 0, nil, fmt.Errorf("%w: no v1 signature", ErrWebhookHeader)
	}
	return ts, sigs, nil
}

// WebhookEvent is the envelope of a delivery.
type WebhookEvent struct {
	// ID identifies the event; deliveries are at least once, so ignore an ID you have handled.
	ID string `json:"id"`
	// Type is one of the Event* constants.
	Type string `json:"type"`
	// Created is when the event happened.
	Created time.Time `json:"created"`
	// TenantID is the tenant (sandbox or live) the event belongs to.
	TenantID string `json:"tenant_id"`
	// Data is the event's payload; read it with DecodeData.
	Data json.RawMessage `json:"data"`
}

// DecodeData reads Data into v, for example *InvoiceEventData.
func (e *WebhookEvent) DecodeData(v any) error {
	if err := json.Unmarshal(e.Data, v); err != nil {
		return fmt.Errorf("payrudder: decode %s data: %w", e.Type, err)
	}
	return nil
}

// ConstructEvent verifies a delivery (see VerifyWebhook) and parses its body.
func ConstructEvent(payload []byte, header, secret string, tolerance time.Duration) (*WebhookEvent, error) {
	if err := VerifyWebhook(payload, header, secret, tolerance); err != nil {
		return nil, err
	}
	var ev WebhookEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, fmt.Errorf("payrudder: decode webhook event: %w", err)
	}
	return &ev, nil
}

// InvoiceEventData is the data of invoice.* events.
type InvoiceEventData struct {
	InvoiceID          string `json:"invoice_id"`
	Number             string `json:"number"`
	CustomerID         string `json:"customer_id"`
	CustomerExternalID string `json:"customer_external_id"`
	PeriodID           string `json:"period_id"`
	Currency           string `json:"currency"`
	TotalMinor         Int64  `json:"total_minor"`
	Status             string `json:"status"`
	Environment        string `json:"environment"`
	// TaxMinor and NetMinor are set on invoice.paid: the net is the total less its TAX lines.
	TaxMinor Int64 `json:"tax_minor,omitempty"`
	NetMinor Int64 `json:"net_minor,omitempty"`
}

// CreditNoteEventData is the data of credit_note.* events.
type CreditNoteEventData struct {
	CreditNoteID       string `json:"credit_note_id"`
	Number             string `json:"number"`
	InvoiceID          string `json:"invoice_id"`
	InvoiceNumber      string `json:"invoice_number"`
	CustomerID         string `json:"customer_id"`
	CustomerExternalID string `json:"customer_external_id"`
	Currency           string `json:"currency"`
	TotalMinor         Int64  `json:"total_minor"`
	Reason             string `json:"reason"`
	// Disposition is credit or refund.
	Disposition string `json:"disposition"`
	RefundState string `json:"refund_state,omitempty"`
	RefundError string `json:"refund_error,omitempty"`
}

// SubscriptionEventData is the data of subscription.* events.
type SubscriptionEventData struct {
	Subscription           Subscription    `json:"subscription"`
	PreviousSubscriptionID string          `json:"previous_subscription_id,omitempty"`
	Quote                  json.RawMessage `json:"quote,omitempty"`
	ToProvider             string          `json:"to_provider,omitempty"`
	CreditedMinor          Int64           `json:"credited_minor,omitempty"`
}

// PaymentLinkEventData is the data of payment_link.* events.
type PaymentLinkEventData struct {
	LinkID             string `json:"link_id"`
	Kind               string `json:"kind"`
	InvoiceID          string `json:"invoice_id,omitempty"`
	InvoiceNumber      string `json:"invoice_number,omitempty"`
	AmountMinor        Int64  `json:"amount_minor,omitempty"`
	TotalMinor         Int64  `json:"total_minor,omitempty"`
	Currency           string `json:"currency,omitempty"`
	Description        string `json:"description,omitempty"`
	CustomerExternalID string `json:"customer_external_id,omitempty"`
	SingleUse          bool   `json:"single_use,omitempty"`
	PaidCount          int    `json:"paid_count,omitempty"`
	// ExpiresAt is an RFC 3339 time.
	ExpiresAt string `json:"expires_at,omitempty"`
}

// CreditsEventData is the data of credits.purchased and credits.low.
type CreditsEventData struct {
	CustomerID         string `json:"customer_id"`
	CustomerExternalID string `json:"customer_external_id"`
	Balance            Int64  `json:"balance"`
	// Threshold is set on credits.low.
	Threshold Int64 `json:"threshold,omitempty"`
	// The rest is set on credits.purchased.
	Credits       Int64  `json:"credits,omitempty"`
	BonusCredits  Int64  `json:"bonus_credits,omitempty"`
	Quantity      int    `json:"quantity,omitempty"`
	PackID        string `json:"pack_id,omitempty"`
	PackName      string `json:"pack_name,omitempty"`
	LinkID        string `json:"link_id,omitempty"`
	InvoiceID     string `json:"invoice_id,omitempty"`
	InvoiceNumber string `json:"invoice_number,omitempty"`
	Currency      string `json:"currency,omitempty"`
	AmountMinor   Int64  `json:"amount_minor,omitempty"`
	// ExpiresAt is an RFC 3339 time, absent when the credits never expire.
	ExpiresAt string `json:"expires_at,omitempty"`
}
