// Package stripe is the Stripe adapter of payments.Provider (https://docs.stripe.com/api).
//
// Stripe is not the merchant of record: Payrudder owns the subscription, the invoice, the renewal and the retries, and Stripe is the card processor. So:
//
//   - Charge: a PaymentIntent confirmed off session for a saved payment method ("pm_...") of the customer, with Stripe's own Idempotency-Key header;
//   - Status: finds the PaymentIntent that carries Payrudder's key in its metadata, through Stripe's search;
//   - Refund: a refund of the PaymentIntent (the charge id), partial or whole;
//   - CreateInvoiceCheckout: a hosted Checkout Session for one invoice, tagged with the key of the charge;
//   - ConfirmNotification: verifies the Stripe-Signature of a webhook (HMAC-SHA256 over "t.body" with the endpoint's signing secret) and turns the event into
//     payments.Notification.
//
// It was written from Stripe's public documentation and tested against a fake server. Behaviour the documentation does not settle is marked UNVERIFIED; no test
// has run against a real Stripe account. A connected account's mode (live or test) is told by its key (sk_live_ / sk_test_), because Stripe has one host for both.
package stripe

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
const Name = "stripe"

// LiveURL is Stripe's API host, for live and test keys alike.
const LiveURL = "https://api.stripe.com"

// Settings is the JSON a tenant's Stripe account is stored as.
type Settings struct {
	// APIKey is a secret or restricted key: sk_live_..., sk_test_..., rk_live_... or rk_test_.... Its prefix says whether the account is live or test.
	APIKey string `json:"api_key"`
	// WebhookSecret is the signing secret (whsec_...) of the webhook endpoint Stripe sends to Payrudder's webhook URL.
	WebhookSecret string `json:"webhook_secret"`
	// BaseURL defaults to Stripe's API host. Another host is for tests and is refused when a person connects the account themselves.
	BaseURL string `json:"base_url,omitempty"`
	// WebhookTolerance is how old a signed webhook may be, in seconds. Default 300, Stripe's own default.
	WebhookTolerance int `json:"webhook_tolerance_seconds,omitempty"`
}

// Client is a Stripe account.
type Client struct {
	s    Settings
	http *http.Client
	now  func() time.Time
}

// New returns a client with the given per-request timeout (default 10 s).
func New(s Settings, timeout time.Duration) (*Client, error) {
	if s.APIKey == "" || s.WebhookSecret == "" {
		return nil, errors.New("stripe: api_key and webhook_secret are required")
	}
	if !validKey(s.APIKey) {
		return nil, errors.New("stripe: api_key must be a secret or restricted key (sk_live_, sk_test_, rk_live_ or rk_test_)")
	}
	if s.BaseURL == "" {
		s.BaseURL = LiveURL
	}
	if _, err := url.ParseRequestURI(s.BaseURL); err != nil {
		return nil, fmt.Errorf("stripe: base_url: %w", err)
	}
	s.BaseURL = strings.TrimRight(s.BaseURL, "/")
	if s.WebhookTolerance <= 0 {
		s.WebhookTolerance = 300
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{s: s, http: &http.Client{Timeout: timeout}, now: time.Now}, nil
}

func validKey(k string) bool {
	for _, p := range []string{"sk_live_", "sk_test_", "rk_live_", "rk_test_"} {
		if strings.HasPrefix(k, p) && len(k) > len(p) {
			return true
		}
	}
	return false
}

// Factory builds a Client from stored settings, for payments.Registry.
func Factory(settings []byte) (payments.Provider, error) {
	var s Settings
	if err := json.Unmarshal(settings, &s); err != nil {
		return nil, fmt.Errorf("stripe: settings: %w", err)
	}
	return New(s, 0)
}

// Name implements payments.PaymentProvider.
func (c *Client) Name() string { return Name }

// KeyMode implements payments.KeyModer: the key's prefix says whether the account is live or test.
func (c *Client) KeyMode() (live, known bool) {
	switch {
	case strings.HasPrefix(c.s.APIKey, "sk_live_"), strings.HasPrefix(c.s.APIKey, "rk_live_"):
		return true, true
	case strings.HasPrefix(c.s.APIKey, "sk_test_"), strings.HasPrefix(c.s.APIKey, "rk_test_"):
		return false, true
	}
	return false, false
}

// Livemode implements payments.LiveModer. Stripe has one host for both modes, so the host cannot tell; what this answers is whether the host is Stripe's own.
// A person connecting an account may only point it at Stripe: any other host is "not known" and is refused (the platform would send the key there).
func (c *Client) Livemode() (live, known bool) {
	if c.s.BaseURL != LiveURL {
		return false, false
	}
	return c.KeyMode()
}

// Capabilities implements payments.Provider.
func (c *Client) Capabilities() payments.Capabilities { return Caps() }

// Caps are Stripe's capabilities as Payrudder uses it: a card processor with a hosted checkout and refunds, honouring idempotency keys. Payrudder runs renewals,
// retries and tax. Stripe charges in more currencies than Payrudder lists, so none is declared and Stripe refuses what it cannot take.
func Caps() payments.Capabilities {
	return payments.Capabilities{Name: Name, HostedCheckout: true, Refunds: true, IdempotentCharges: true}
}

// CheckHealth implements payments.HealthChecker: GET /v1/balance needs a valid key and changes nothing.
func (c *Client) CheckHealth(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/v1/balance", nil, "", nil)
}

// apiError is Stripe's error body.
type apiError struct {
	Error struct {
		Type        string `json:"type"`
		Code        string `json:"code"`
		DeclineCode string `json:"decline_code"`
		Message     string `json:"message"`
	} `json:"error"`
}

// Error is a request Stripe refused. It is permanent: the same request is refused the same.
type Error struct {
	Status  int
	Type    string
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("stripe refused the request (%d %s %s): %s", e.Status, e.Type, e.Code, e.Message)
}

func (c *Client) do(ctx context.Context, method, path string, form url.Values, idempotencyKey string, out any) error {
	var body io.Reader
	target := c.s.BaseURL + path
	if form != nil {
		if method == http.MethodGet {
			target += "?" + form.Encode()
		} else {
			body = bytes.NewReader([]byte(form.Encode()))
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.s.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
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
		return &payments.TransientError{Err: fmt.Errorf("stripe answered %d", resp.StatusCode)}
	case resp.StatusCode >= 400:
		var e apiError
		_ = json.Unmarshal(raw, &e)
		if e.Error.Type == "card_error" {
			code := e.Error.DeclineCode
			if code == "" {
				code = e.Error.Code
			}
			if code == "" {
				code = "card_declined"
			}
			return &payments.DeclinedError{Code: code}
		}
		return &Error{Status: resp.StatusCode, Type: e.Error.Type, Code: e.Error.Code, Message: e.Error.Message}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("stripe answered garbage: %w", err)
		}
	}
	return nil
}

// threeDecimal are the currencies Stripe takes with three decimals but only in whole tens of the smallest unit (the last digit must be 0).
var threeDecimal = map[string]bool{"BHD": true, "JOD": true, "KWD": true, "OMR": true, "TND": true}

// amount checks and returns the amount as Stripe takes it. Stripe counts in the smallest unit of the currency, like Payrudder, but refuses a three-decimal
// amount whose last digit is not 0 instead of rounding it, so Payrudder refuses it first and never sends a different amount than the invoice's.
func amount(minor int64, currency string) (string, error) {
	if minor <= 0 {
		return "", errors.New("stripe: nothing to charge")
	}
	if threeDecimal[strings.ToUpper(currency)] && minor%10 != 0 {
		return "", fmt.Errorf("stripe: %s amounts must end in 0 (the invoice total %d does not)", strings.ToUpper(currency), minor)
	}
	return strconv.FormatInt(minor, 10), nil
}

type paymentIntent struct {
	ID               string            `json:"id"`
	Status           string            `json:"status"`
	Amount           int64             `json:"amount"`
	AmountReceived   int64             `json:"amount_received"`
	Currency         string            `json:"currency"`
	Metadata         map[string]string `json:"metadata"`
	LastPaymentError *struct {
		Code        string `json:"code"`
		DeclineCode string `json:"decline_code"`
	} `json:"last_payment_error"`
}

func (p paymentIntent) declineCode() string {
	if e := p.LastPaymentError; e != nil {
		if e.DeclineCode != "" {
			return e.DeclineCode
		}
		if e.Code != "" {
			return e.Code
		}
	}
	return "payment_failed"
}

// Charge confirms a PaymentIntent off session for the customer's saved payment method (r.PaymentMethod, "pm_..."). Stripe's Idempotency-Key makes a retry
// return the first answer. A payment that needs the customer present (3-D Secure) is a decline, because nobody is there to approve it.
func (c *Client) Charge(ctx context.Context, r payments.ChargeRequest) (payments.ChargeResult, error) {
	if r.IdempotencyKey == "" {
		return payments.ChargeResult{}, errors.New("payments: idempotency key is mandatory")
	}
	if !strings.HasPrefix(r.PaymentMethod, "pm_") {
		return payments.ChargeResult{}, errors.New(`stripe: the payment method must be a Stripe PaymentMethod id ("pm_...")`)
	}
	amt, err := amount(r.AmountMinor, r.Currency)
	if err != nil {
		return payments.ChargeResult{}, err
	}
	form := url.Values{
		"amount": {amt}, "currency": {strings.ToLower(r.Currency)}, "payment_method": {r.PaymentMethod},
		"confirm": {"true"}, "off_session": {"true"},
		"description":                    {"Payrudder invoice " + r.InvoiceID},
		"metadata[payrudder_key]":        {r.IdempotencyKey},
		"metadata[payrudder_invoice_id]": {r.InvoiceID},
	}
	var pi paymentIntent
	if err := c.do(ctx, http.MethodPost, "/v1/payment_intents", form, r.IdempotencyKey, &pi); err != nil {
		return payments.ChargeResult{}, err
	}
	switch pi.Status {
	case "succeeded":
		return payments.ChargeResult{ChargeID: pi.ID}, nil
	case "processing":
		return payments.ChargeResult{}, &payments.PendingError{Ref: pi.ID}
	case "requires_action":
		return payments.ChargeResult{}, &payments.DeclinedError{Code: "authentication_required"}
	default: // requires_payment_method, requires_confirmation, canceled
		return payments.ChargeResult{}, &payments.DeclinedError{Code: pi.declineCode()}
	}
}

// Refund refunds the PaymentIntent (the charge id) for r.AmountMinor, or for what is left when that is 0. The idempotency key is Stripe's.
func (c *Client) Refund(ctx context.Context, r payments.RefundRequest) (payments.RefundResult, error) {
	if !strings.HasPrefix(r.ChargeID, "pi_") {
		return payments.RefundResult{}, errors.New(`stripe: a refund needs the PaymentIntent id ("pi_...") as the charge id`)
	}
	form := url.Values{"payment_intent": {r.ChargeID}, "metadata[payrudder_key]": {r.IdempotencyKey}, "metadata[payrudder_invoice_id]": {r.InvoiceID}}
	if r.AmountMinor > 0 {
		amt, err := amount(r.AmountMinor, r.Currency)
		if err != nil {
			return payments.RefundResult{}, err
		}
		form.Set("amount", amt)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/refunds", form, r.IdempotencyKey, &out); err != nil {
		return payments.RefundResult{}, err
	}
	if out.ID == "" {
		return payments.RefundResult{}, errors.New("stripe: a refund without an id")
	}
	return payments.RefundResult{RefundID: out.ID}, nil
}

// Status finds the PaymentIntent that carries Payrudder's key in its metadata. UNVERIFIED against a real account: Stripe documents the search as eventually
// consistent (a PaymentIntent can take up to a minute to appear), which is why Charge also relies on Stripe's idempotency: a retried charge returns the first
// answer whether or not the search has caught up.
func (c *Client) Status(ctx context.Context, key string) (payments.StatusResult, error) {
	var out struct {
		Data []paymentIntent `json:"data"`
	}
	q := url.Values{"query": {"metadata['payrudder_key']:'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(key) + "'"}, "limit": {"1"}}
	if err := c.do(ctx, http.MethodGet, "/v1/payment_intents/search", q, "", &out); err != nil {
		return payments.StatusResult{}, err
	}
	if len(out.Data) == 0 {
		return payments.StatusResult{State: payments.StateNotFound}, nil
	}
	pi := out.Data[0]
	switch pi.Status {
	case "succeeded":
		return payments.StatusResult{State: payments.StateSucceeded, ChargeID: pi.ID}, nil
	case "requires_payment_method", "canceled":
		return payments.StatusResult{State: payments.StateDeclined, ChargeID: pi.ID, DeclineCode: pi.declineCode()}, nil
	case "requires_action":
		return payments.StatusResult{State: payments.StateDeclined, ChargeID: pi.ID, DeclineCode: "authentication_required"}, nil
	}
	return payments.StatusResult{}, &payments.PendingError{Ref: pi.ID}
}

// CreateInvoiceCheckout starts a hosted Checkout Session for one invoice, tagged with the key of the charge on the session and on its PaymentIntent, which
// ConfirmNotification reads back from checkout.session.completed.
func (c *Client) CreateInvoiceCheckout(ctx context.Context, r payments.CheckoutInvoiceRequest) (payments.Checkout, error) {
	if r.IdempotencyKey == "" || r.Currency == "" {
		return payments.Checkout{}, errors.New("stripe: an invoice checkout needs a key and a currency")
	}
	amt, err := amount(r.AmountMinor, r.Currency)
	if err != nil {
		return payments.Checkout{}, err
	}
	name := "Invoice"
	if r.Number != "" {
		name += " " + r.Number
	}
	form := url.Values{
		"mode": {"payment"}, "success_url": {r.ReturnURL}, "cancel_url": {r.ReturnURL},
		"line_items[0][quantity]": {"1"}, "line_items[0][price_data][currency]": {strings.ToLower(r.Currency)}, "line_items[0][price_data][unit_amount]": {amt},
		"line_items[0][price_data][product_data][name]": {name},
		"metadata[payrudder_key]":                       {r.IdempotencyKey}, "metadata[payrudder_invoice_id]": {r.InvoiceID},
		"payment_intent_data[metadata][payrudder_key]":        {r.IdempotencyKey},
		"payment_intent_data[metadata][payrudder_invoice_id]": {r.InvoiceID},
	}
	if r.Description != "" {
		form.Set("line_items[0][price_data][product_data][description]", r.Description)
	}
	if r.CustomerEmail != "" {
		form.Set("customer_email", r.CustomerEmail)
	}
	var out struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/checkout/sessions", form, r.IdempotencyKey, &out); err != nil {
		return payments.Checkout{}, err
	}
	if out.URL == "" {
		return payments.Checkout{}, errors.New("stripe: the checkout session has no url")
	}
	return payments.Checkout{URL: out.URL, Ref: out.ID}, nil
}

// SignatureHeader builds a Stripe-Signature header value for body at time ts (what Stripe does when it sends). Used to test and to simulate.
func SignatureHeader(secret string, ts time.Time, body []byte) string {
	unix := strconv.FormatInt(ts.Unix(), 10)
	return "t=" + unix + ",v1=" + sign(secret, unix, body)
}

func sign(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ErrBadSignature: the Stripe-Signature header is missing, malformed, stale or does not match.
var ErrBadSignature = fmt.Errorf("stripe: bad webhook signature: %w", payments.ErrUnverified)

func (c *Client) verify(body []byte, header http.Header) error {
	ts, sigs := "", []string(nil)
	for _, part := range strings.Split(header.Get("Stripe-Signature"), ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		switch {
		case !ok:
		case k == "t":
			ts = v
		case k == "v1":
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

type event struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Created int64  `json:"created"`
	Data    struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

// ParseNotification reads which event a webhook is, without trusting it. Stripe's event id is its identity; the charge it is about is only known after the
// signature has been verified, so no idempotency key is returned here.
func ParseNotification(body []byte, _ http.Header) (payments.NotificationRef, error) {
	var e event
	if err := json.Unmarshal(body, &e); err != nil {
		return payments.NotificationRef{}, fmt.Errorf("stripe: webhook body: %w", err)
	}
	if e.ID == "" || e.Type == "" {
		return payments.NotificationRef{}, errors.New("stripe: webhook without id or type")
	}
	return payments.NotificationRef{EventID: e.ID}, nil
}

// ConfirmNotification implements payments.Notifier. Stripe signs its webhooks, so the signature is the confirmation; nothing is believed that does not verify.
func (c *Client) ConfirmNotification(_ context.Context, body []byte, header http.Header) (payments.Notification, error) {
	if err := c.verify(body, header); err != nil {
		return payments.Notification{}, err
	}
	var e event
	if err := json.Unmarshal(body, &e); err != nil {
		return payments.Notification{}, fmt.Errorf("stripe: webhook body: %w", err)
	}
	n := payments.Notification{Ref: payments.NotificationRef{EventID: e.ID}, Kind: payments.KindIgnored}
	if e.Created > 0 {
		n.OccurredAt = time.Unix(e.Created, 0).UTC()
	}
	switch e.Type {
	case "payment_intent.succeeded", "payment_intent.payment_failed":
		var pi paymentIntent
		if err := json.Unmarshal(e.Data.Object, &pi); err != nil {
			return n, fmt.Errorf("stripe: payment intent: %w", err)
		}
		n.Kind, n.ProviderRef, n.Currency = payments.KindCharge, pi.ID, strings.ToUpper(pi.Currency)
		n.Ref.IdempotencyKey = pi.Metadata["payrudder_key"]
		if e.Type == "payment_intent.succeeded" {
			n.State, n.AmountMinor = payments.StateSucceeded, pi.AmountReceived
			if n.AmountMinor == 0 {
				n.AmountMinor = pi.Amount
			}
		} else {
			n.State, n.DeclineCode, n.AmountMinor = payments.StateDeclined, pi.declineCode(), pi.Amount
		}
	case "checkout.session.completed", "checkout.session.async_payment_succeeded", "checkout.session.async_payment_failed":
		var s struct {
			PaymentIntent string            `json:"payment_intent"`
			PaymentStatus string            `json:"payment_status"`
			AmountTotal   int64             `json:"amount_total"`
			Currency      string            `json:"currency"`
			Metadata      map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(e.Data.Object, &s); err != nil {
			return n, fmt.Errorf("stripe: checkout session: %w", err)
		}
		switch {
		case e.Type == "checkout.session.async_payment_failed":
			n.State, n.DeclineCode = payments.StateDeclined, "payment_failed"
		case s.PaymentStatus == "paid":
			n.State = payments.StateSucceeded
		default: // a delayed method that has not settled yet: the async events decide
			return n, nil
		}
		n.Kind, n.ProviderRef, n.Currency, n.AmountMinor = payments.KindCharge, s.PaymentIntent, strings.ToUpper(s.Currency), s.AmountTotal
		n.Ref.IdempotencyKey = s.Metadata["payrudder_key"]
	case "refund.updated", "refund.failed":
		var r struct {
			ID       string            `json:"id"`
			Status   string            `json:"status"`
			Amount   int64             `json:"amount"`
			Currency string            `json:"currency"`
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(e.Data.Object, &r); err != nil {
			return n, fmt.Errorf("stripe: refund: %w", err)
		}
		switch r.Status {
		case "succeeded":
			n.State = payments.StateSucceeded
		case "failed", "canceled":
			n.State, n.DeclineCode = payments.StateDeclined, r.Status
		default: // pending, requires_action: not final
			return n, nil
		}
		n.Kind, n.ProviderRef, n.Currency, n.AmountMinor = payments.KindRefund, r.ID, strings.ToUpper(r.Currency), r.Amount
		n.Ref.IdempotencyKey = r.Metadata["payrudder_key"]
	}
	return n, nil
}

// Register adds Stripe to a registry, with its webhook parser.
func Register(r *payments.Registry) {
	r.Register(Name, Factory)
	r.RegisterParser(Name, ParseNotification)
}

var (
	_ payments.Notifier                = (*Client)(nil)
	_ payments.InvoiceCheckoutProvider = (*Client)(nil)
	_ payments.HealthChecker           = (*Client)(nil)
	_ payments.KeyModer                = (*Client)(nil)
	_ payments.LiveModer               = (*Client)(nil)
)
