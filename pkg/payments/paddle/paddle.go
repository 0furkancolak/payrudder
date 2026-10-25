// Package paddle is the Paddle Billing adapter of payments.Provider (https://developer.paddle.com).
//
// Paddle is the merchant of record: it collects the payment, works out and remits tax, issues the receipt, and runs the
// subscriptions it sells, renewing them and retrying failed renewals itself. So Payrudder does not charge a card here. What it does:
//
//   - CreateCheckout: a hosted page where the customer subscribes (a transaction for a catalogue price, tied to our subscription by custom data);
//   - Charge: a one-time charge on the customer's Paddle subscription (Payrudder's usage invoice), as a non-catalogue price. Paddle settles it asynchronously, so
//     Charge reports payments.PendingError and the outcome arrives as a notification;
//   - Refund: a full refund adjustment of a transaction;
//   - ConfirmNotification: verifies the Paddle-Signature of a webhook and turns the event into payments.Notification.
//
// It was written from Paddle's public documentation and its Go SDK's types, and tested against fixtures. Behaviour the documentation does
// not settle is marked UNVERIFIED; the sandbox test is skipped unless PADDLE_SANDBOX_API_KEY is set.
package paddle

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

// Name is the registry name.
const Name = "paddle"

// Endpoints.
const (
	LiveURL    = "https://api.paddle.com"
	SandboxURL = "https://sandbox-api.paddle.com"
)

// Settings is the JSON a tenant's Paddle account is stored as.
type Settings struct {
	// APIKey is a Paddle API key (Bearer).
	APIKey string `json:"api_key"`
	// BaseURL defaults to the live API; use SandboxURL for a sandbox account.
	BaseURL string `json:"base_url,omitempty"`
	// WebhookSecret is the secret key of the notification destination Paddle sends to Payrudder's webhook URL.
	WebhookSecret string `json:"webhook_secret"`
	// UsageProductID is the catalogue product (pro_...) Payrudder's usage invoices are billed under, as non-catalogue prices.
	UsageProductID string `json:"usage_product_id"`
	// TaxMode of those charges: account_setting (default), external, internal or location.
	TaxMode string `json:"tax_mode,omitempty"`
	// WebhookTolerance is how old a signed webhook may be, in seconds. Default 300: Paddle retries a failed delivery.
	WebhookTolerance int `json:"webhook_tolerance_seconds,omitempty"`
}

// Client is a Paddle account.
type Client struct {
	s    Settings
	http *http.Client
	now  func() time.Time
}

// New returns a client with the given per-request timeout (default 10 s).
func New(s Settings, timeout time.Duration) (*Client, error) {
	if s.APIKey == "" || s.WebhookSecret == "" || s.UsageProductID == "" {
		return nil, errors.New("paddle: api_key, webhook_secret and usage_product_id are required")
	}
	if s.BaseURL == "" {
		s.BaseURL = LiveURL
	}
	if _, err := url.ParseRequestURI(s.BaseURL); err != nil {
		return nil, fmt.Errorf("paddle: base_url: %w", err)
	}
	s.BaseURL = strings.TrimRight(s.BaseURL, "/")
	if s.TaxMode == "" {
		s.TaxMode = "account_setting"
	}
	switch s.TaxMode {
	case "account_setting", "external", "internal", "location":
	default:
		return nil, fmt.Errorf("paddle: tax_mode %q is not one of account_setting, external, internal, location", s.TaxMode)
	}
	if s.WebhookTolerance <= 0 {
		s.WebhookTolerance = 300
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{s: s, http: &http.Client{Timeout: timeout}, now: time.Now}, nil
}

// Factory builds a Client from stored settings, for payments.Registry.
func Factory(settings []byte) (payments.Provider, error) {
	var s Settings
	if err := json.Unmarshal(settings, &s); err != nil {
		return nil, fmt.Errorf("paddle: settings: %w", err)
	}
	return New(s, 0)
}

// Name implements payments.PaymentProvider.
func (c *Client) Name() string { return Name }

// Capabilities implements payments.Provider: Paddle runs renewals and retries, calculates tax and hosts the checkout.
// Charges are not idempotent by key. Paddle bills in more currencies than Payrudder lists; the intersection is declared.
func (c *Client) Capabilities() payments.Capabilities { return Caps() }

// Caps are Paddle's capabilities, which do not depend on an account.
func Caps() payments.Capabilities {
	return payments.Capabilities{Name: Name, OwnsRenewal: true, OwnsDunning: true, CalculatesTax: true, HostedCheckout: true, SendsOwnReceipts: true, Refunds: true,
		Currencies: []string{"USD", "EUR", "GBP", "CHF", "JPY"}}
}

// apiError is Paddle's error body.
type apiError struct {
	Error struct {
		Type   string `json:"type"`
		Code   string `json:"code"`
		Detail string `json:"detail"`
	} `json:"error"`
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.s.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.s.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Paddle-Version", "1")
	resp, err := c.http.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) {
			return payments.ErrTimeout
		}
		return &payments.TransientError{Err: err}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return &payments.TransientError{Err: fmt.Errorf("paddle answered %d", resp.StatusCode)}
	case resp.StatusCode >= 400:
		var e apiError
		_ = json.Unmarshal(raw, &e)
		return &Error{Status: resp.StatusCode, Code: e.Error.Code, Detail: e.Error.Detail}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("paddle answered garbage: %w", err)
		}
	}
	return nil
}

// Error is a request Paddle refused. It is permanent: a request the same will be refused the same.
type Error struct {
	Status int
	Code   string
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("paddle refused the request (%d %s): %s", e.Status, e.Code, e.Detail)
}

type money struct {
	Amount       string `json:"amount"`
	CurrencyCode string `json:"currency_code"`
}

// Charge bills Payrudder's invoice to the customer's Paddle subscription (PaymentMethod is the subscription id, sub_...). Paddle
// answers when it has accepted the charge, not when the customer has paid: the result is payments.PendingError, and the transaction's
// notification settles it. The idempotency key rides along as custom data on the price, which is how that notification finds its invoice.
func (c *Client) Charge(ctx context.Context, r payments.ChargeRequest) (payments.ChargeResult, error) {
	if r.IdempotencyKey == "" {
		return payments.ChargeResult{}, errors.New("payments: idempotency key is mandatory")
	}
	if !strings.HasPrefix(r.PaymentMethod, "sub_") {
		return payments.ChargeResult{}, errors.New(`paddle: payment method must be the customer's Paddle subscription id ("sub_...")`)
	}
	if r.AmountMinor <= 0 {
		return payments.ChargeResult{}, errors.New("paddle: nothing to charge")
	}
	name := "Usage invoice"
	req := map[string]any{
		"effective_from": "immediately",
		"items": []map[string]any{{
			"quantity": 1,
			"price": map[string]any{
				"description": "Payrudder invoice " + r.InvoiceID,
				"name":        name,
				"product_id":  c.s.UsageProductID,
				"tax_mode":    c.s.TaxMode,
				"unit_price":  money{Amount: strconv.FormatInt(r.AmountMinor, 10), CurrencyCode: r.Currency},
				"quantity":    map[string]int{"minimum": 1, "maximum": 1},
				"custom_data": map[string]string{"payrudder_key": r.IdempotencyKey, "payrudder_invoice_id": r.InvoiceID},
			},
		}},
		"on_payment_failure": "prevent_change",
	}
	if err := c.do(ctx, http.MethodPost, "/subscriptions/"+url.PathEscape(r.PaymentMethod)+"/charge", req, nil); err != nil {
		return payments.ChargeResult{}, err
	}
	return payments.ChargeResult{}, &payments.PendingError{Ref: r.IdempotencyKey}
}

// Refund refunds the whole transaction (ChargeID is its id, txn_...) with a full adjustment. Paddle may hold a refund for approval; the
// adjustment's notification reports the outcome. UNVERIFIED: that the amount of a partial refund is not needed by Payrudder: the saga
// refunds the whole invoice.
func (c *Client) Refund(ctx context.Context, r payments.RefundRequest) (payments.RefundResult, error) {
	if !strings.HasPrefix(r.ChargeID, "txn_") {
		return payments.RefundResult{}, errors.New(`paddle: a refund needs the transaction id ("txn_...") as the charge id`)
	}
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	err := c.do(ctx, http.MethodPost, "/adjustments", map[string]any{
		"action": "refund", "type": "full", "transaction_id": r.ChargeID, "reason": "Payrudder refund " + r.IdempotencyKey,
	}, &out)
	if err != nil {
		return payments.RefundResult{}, err
	}
	if out.Data.ID == "" {
		return payments.RefundResult{}, errors.New("paddle: an adjustment without an id")
	}
	return payments.RefundResult{RefundID: out.Data.ID}, nil
}

// Status cannot be told from Paddle's API by Payrudder's key (the key lives in custom data of a transaction's price, which cannot be searched).
// The answer comes from the notification: the worker reads the ledger first. Until it is there, Status says so with an error, and never
// "not found": a second charge would be a second bill.
func (c *Client) Status(context.Context, string) (payments.StatusResult, error) {
	return payments.StatusResult{}, &payments.PendingError{Ref: "awaiting Paddle's notification"}
}

// CreateCheckout starts a hosted checkout for a catalogue price. When the customer completes it, Paddle creates the subscription and
// tells Payrudder (subscription.created), carrying PayrudderSubscriptionID in its custom data.
func (c *Client) CreateCheckout(ctx context.Context, r payments.CheckoutRequest) (payments.Checkout, error) {
	if !strings.HasPrefix(r.PriceRef, "pri_") {
		return payments.Checkout{}, errors.New(`paddle: price must be a Paddle price id ("pri_...")`)
	}
	q := r.Quantity
	if q <= 0 {
		q = 1
	}
	req := map[string]any{
		"items":           []map[string]any{{"price_id": r.PriceRef, "quantity": q}},
		"collection_mode": "automatic",
	}
	custom := map[string]string{}
	if r.PayrudderSubscriptionID != "" {
		custom["quotify_subscription_id"] = r.PayrudderSubscriptionID
	}
	if r.PayrudderMigrationID != "" {
		custom["quotify_migration_id"] = r.PayrudderMigrationID
	}
	if len(custom) > 0 {
		req["custom_data"] = custom
	}
	var out struct {
		Data struct {
			ID       string `json:"id"`
			Checkout *struct {
				URL string `json:"url"`
			} `json:"checkout"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/transactions", req, &out); err != nil {
		return payments.Checkout{}, err
	}
	if out.Data.Checkout == nil || out.Data.Checkout.URL == "" {
		return payments.Checkout{}, errors.New("paddle: the transaction has no checkout url (is a default payment link set in Paddle?)")
	}
	return payments.Checkout{URL: out.Data.Checkout.URL, Ref: out.Data.ID}, nil
}

// CancelSubscription cancels a Paddle subscription (sub_...): at the end of the billing period the customer has paid for, or immediately.
func (c *Client) CancelSubscription(ctx context.Context, ref string, atPeriodEnd bool) error {
	if !strings.HasPrefix(ref, "sub_") {
		return errors.New(`paddle: a subscription id ("sub_...") is needed`)
	}
	when := "immediately"
	if atPeriodEnd {
		when = "next_billing_period"
	}
	return c.do(ctx, http.MethodPost, "/subscriptions/"+url.PathEscape(ref)+"/cancel", map[string]string{"effective_from": when}, nil)
}

// SignatureHeader builds a Paddle-Signature header value for body at time ts (what Paddle does when it sends). Used to test and to simulate.
func SignatureHeader(secret string, ts time.Time, body []byte) string {
	unix := strconv.FormatInt(ts.Unix(), 10)
	return "ts=" + unix + ";h1=" + sign(secret, unix, body)
}

func sign(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + ":"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ErrBadSignature: the Paddle-Signature header is missing, malformed, stale or does not match.
var ErrBadSignature = fmt.Errorf("paddle: bad webhook signature: %w", payments.ErrUnverified)

func (c *Client) verify(body []byte, header http.Header) error {
	ts, sigs := "", []string(nil)
	for _, part := range strings.Split(header.Get("Paddle-Signature"), ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		switch {
		case !ok:
		case k == "ts":
			ts = v
		case k == "h1":
			sigs = append(sigs, v)
		}
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(sigs) == 0 {
		return ErrBadSignature
	}
	age := c.now().Sub(time.Unix(unix, 0))
	if age < -time.Minute || age > time.Duration(c.s.WebhookTolerance)*time.Second {
		return fmt.Errorf("%w: timestamp is %s off", ErrBadSignature, age.Round(time.Second))
	}
	want := sign(c.s.WebhookSecret, ts, body)
	for _, s := range sigs {
		if hmac.Equal([]byte(s), []byte(want)) {
			return nil
		}
	}
	return ErrBadSignature
}

type envelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	OccurredAt string          `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

// ParseNotification reads which event a webhook is, without trusting it. Paddle's event id is its identity; the charge a transaction
// belongs to is only known after the signature has been verified, so no idempotency key is returned here.
func ParseNotification(body []byte, _ http.Header) (payments.NotificationRef, error) {
	var e envelope
	if err := json.Unmarshal(body, &e); err != nil {
		return payments.NotificationRef{}, fmt.Errorf("paddle: webhook body: %w", err)
	}
	if e.EventID == "" || e.EventType == "" {
		return payments.NotificationRef{}, errors.New("paddle: webhook without event_id or event_type")
	}
	return payments.NotificationRef{EventID: e.EventID}, nil
}

type customData map[string]any

func (d customData) str(k string) string {
	s, _ := d[k].(string)
	return s
}

type transactionData struct {
	ID           string     `json:"id"`
	Status       string     `json:"status"`
	CurrencyCode string     `json:"currency_code"`
	CustomData   customData `json:"custom_data"`
	Items        []struct {
		Price struct {
			CustomData customData `json:"custom_data"`
		} `json:"price"`
	} `json:"items"`
	Details struct {
		Totals struct {
			GrandTotal string `json:"grand_total"`
			Total      string `json:"total"`
		} `json:"totals"`
	} `json:"details"`
	Payments []struct {
		Status    string  `json:"status"`
		ErrorCode *string `json:"error_code"`
	} `json:"payments"`
}

type subscriptionData struct {
	ID           string     `json:"id"`
	Status       string     `json:"status"`
	CustomData   customData `json:"custom_data"`
	NextBilledAt *string    `json:"next_billed_at"`
	Items        []struct {
		Price struct {
			ID string `json:"id"`
		} `json:"price"`
	} `json:"items"`
}

type adjustmentData struct {
	ID            string `json:"id"`
	Action        string `json:"action"`
	Status        string `json:"status"`
	TransactionID string `json:"transaction_id"`
	CurrencyCode  string `json:"currency_code"`
	Totals        struct {
		Total string `json:"total"`
	} `json:"totals"`
}

func minor(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// ConfirmNotification implements payments.Notifier. Paddle signs its webhooks, so the signature is the confirmation; nothing is
// believed that does not verify.
func (c *Client) ConfirmNotification(_ context.Context, body []byte, header http.Header) (payments.Notification, error) {
	if err := c.verify(body, header); err != nil {
		return payments.Notification{}, err
	}
	var e envelope
	if err := json.Unmarshal(body, &e); err != nil {
		return payments.Notification{}, fmt.Errorf("paddle: webhook body: %w", err)
	}
	n := payments.Notification{Ref: payments.NotificationRef{EventID: e.EventID}, Kind: payments.KindIgnored}
	if t, err := time.Parse(time.RFC3339, e.OccurredAt); err == nil {
		n.OccurredAt = t
	}
	switch {
	case e.EventType == "transaction.completed" || e.EventType == "transaction.payment_failed":
		var t transactionData
		if err := json.Unmarshal(e.Data, &t); err != nil {
			return n, fmt.Errorf("paddle: transaction: %w", err)
		}
		n.Kind, n.ProviderRef, n.Currency = payments.KindCharge, t.ID, t.CurrencyCode
		n.AmountMinor = minor(t.Details.Totals.GrandTotal)
		for _, it := range t.Items {
			if k := it.Price.CustomData.str("payrudder_key"); k != "" {
				n.Ref.IdempotencyKey = k
			}
		}
		if n.Ref.IdempotencyKey == "" {
			n.Ref.IdempotencyKey = t.CustomData.str("payrudder_key")
		}
		if e.EventType == "transaction.completed" {
			n.State = payments.StateSucceeded
		} else {
			n.State = payments.StateDeclined
			n.DeclineCode = "payment_failed"
			for _, p := range t.Payments {
				if p.ErrorCode != nil && *p.ErrorCode != "" {
					n.DeclineCode = *p.ErrorCode
				}
			}
		}
	case strings.HasPrefix(e.EventType, "subscription."):
		var s subscriptionData
		if err := json.Unmarshal(e.Data, &s); err != nil {
			return n, fmt.Errorf("paddle: subscription: %w", err)
		}
		ev := &payments.SubscriptionEvent{ProviderRef: s.ID, Status: s.Status, PayrudderSubscriptionID: s.CustomData.str("quotify_subscription_id"),
			PayrudderMigrationID: s.CustomData.str("quotify_migration_id")}
		if s.NextBilledAt != nil {
			if t, err := time.Parse(time.RFC3339, *s.NextBilledAt); err == nil {
				ev.NextBilledAt = &t
			}
		}
		for _, it := range s.Items {
			if it.Price.ID != "" {
				ev.PriceRefs = append(ev.PriceRefs, it.Price.ID)
			}
		}
		n.Kind, n.ProviderRef, n.Subscription = payments.KindSubscription, s.ID, ev
	case e.EventType == "adjustment.updated" || e.EventType == "adjustment.created":
		var a adjustmentData
		if err := json.Unmarshal(e.Data, &a); err != nil {
			return n, fmt.Errorf("paddle: adjustment: %w", err)
		}
		if a.Action != "refund" {
			return n, nil
		}
		switch a.Status {
		case "approved":
			n.State = payments.StateSucceeded
		case "rejected":
			n.State, n.DeclineCode = payments.StateDeclined, "rejected"
		default: // pending_approval, reversed: not final for us
			return n, nil
		}
		n.Kind, n.ProviderRef, n.Currency, n.AmountMinor = payments.KindRefund, a.ID, a.CurrencyCode, minor(a.Totals.Total)
	}
	return n, nil
}

// Register adds Paddle to a registry, with its webhook parser.
func Register(r *payments.Registry) {
	r.Register(Name, Factory)
	r.RegisterParser(Name, ParseNotification)
}
