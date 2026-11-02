// Package stripe is the Stripe adapter of payments.Provider (https://docs.stripe.com/api).
//
// Stripe is not the merchant of record: Payrudder owns the subscription, the invoice, the renewal and the retries, and Stripe is the card processor. So:
//
//   - Charge: a PaymentIntent confirmed off session for a saved payment method ("pm_...") of the customer, with Stripe's own Idempotency-Key header;
//   - Status: finds the PaymentIntent that carries Payrudder's key in its metadata, through Stripe's search;
//   - Refund: a refund of the PaymentIntent (the charge id), partial or whole;
//   - CreateSetupCheckout: a hosted Checkout Session in setup mode, where the customer saves a card on a Stripe Customer for the off-session charges above; the
//     notification that it was saved (checkout.session.completed, then the SetupIntent) gives the "cus_...:pm_..." Charge takes;
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

// APIVersion is the Stripe API version every request is made with (Stripe-Version), so an account whose default version moves does not change what the adapter
// reads. 2026-09-30.endive is the first version of the Endive release; the versions after it "will include only additive changes"
// (docs.stripe.com/changelog/endive, read 2026-11-02), so it is as safe as the newer ones and never ahead of what the matrix was read against. The version of the
// webhook payloads is the webhook endpoint's, not this header's: create the endpoint with this version (docs/stripe-compatibility-matrix.md).
const APIVersion = "2026-09-30.endive"

// Settings is the JSON a tenant's Stripe account is stored as.
type Settings struct {
	// APIKey is a secret or restricted key: sk_live_..., sk_test_..., rk_live_... or rk_test_.... Its prefix says whether the account is live or test.
	APIKey string `json:"api_key"`
	// WebhookSecret is the signing secret (whsec_...) of the webhook endpoint Stripe sends to Payrudder's webhook URL. It can be left out when the account is connected
	// and filled in by RegisterWebhook, which creates the endpoint and stores the secret Stripe shows then; until it is there no notification is believed.
	WebhookSecret string `json:"webhook_secret,omitempty"`
	// BaseURL defaults to Stripe's API host. Another host is for tests and is refused when a person connects the account themselves.
	BaseURL string `json:"base_url,omitempty"`
	// WebhookTolerance is how old a signed webhook may be, in seconds. Default 300, Stripe's own default.
	WebhookTolerance int `json:"webhook_tolerance_seconds,omitempty"`
	// APIVersion overrides the Stripe-Version of the requests (default APIVersion). Stripe's own advice is to name the version in code rather than rely on the account's default.
	APIVersion string `json:"api_version,omitempty"`
	// SaveCustomer makes an invoice Checkout create a Stripe Customer and save the payment method on it for off-session use (customer_creation=always and
	// payment_intent_data.setup_future_usage=off_session), which is what a later off-session charge needs. Off by default: saving a card is the customer's agreement
	// to make, and nothing in Payrudder yet reads the customer and payment method back from the completed session; they are in the Stripe account.
	SaveCustomer bool `json:"save_customer,omitempty"`
}

// Client is a Stripe account.
type Client struct {
	s    Settings
	http *http.Client
	now  func() time.Time
}

// New returns a client with the given per-request timeout (default 10 s).
func New(s Settings, timeout time.Duration) (*Client, error) {
	if s.APIKey == "" {
		return nil, errors.New("stripe: api_key is required")
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
	if s.APIVersion == "" {
		s.APIVersion = APIVersion
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
// retries and tax. Stripe charges in more currencies than Payrudder lists, so none is declared and Stripe refuses what it cannot take. Refunds are idempotent too:
// the refund POST carries the key, which Stripe keeps for at least 24 hours (docs/stripe-compatibility-matrix.md, row 10).
func Caps() payments.Capabilities {
	return payments.Capabilities{Name: Name, HostedCheckout: true, Refunds: true, IdempotentCharges: true, IdempotentRefunds: true}
}

// CheckHealth implements payments.HealthChecker: GET /v1/balance needs a valid key and changes nothing. The balance says which mode the key is in (livemode,
// docs.stripe.com/api/balance/balance_retrieve, read 2026-11-02), which is compared with what the key's prefix claims: a key that does not match its prefix is refused.
func (c *Client) CheckHealth(ctx context.Context) error {
	var out struct {
		Livemode *bool `json:"livemode"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/balance", nil, "", &out); err != nil {
		return err
	}
	if live, known := c.KeyMode(); known && out.Livemode != nil && *out.Livemode != live {
		return fmt.Errorf("%w: the key's prefix says %s, Stripe says the account is %s", payments.ErrLivemodeMismatch, mode(live), mode(*out.Livemode))
	}
	return nil
}

func mode(live bool) string {
	if live {
		return "live"
	}
	return "test"
}

// apiError is Stripe's error body.
type apiError struct {
	Error struct {
		Type        string `json:"type"`
		Code        string `json:"code"`
		DeclineCode string `json:"decline_code"`
		AdviceCode  string `json:"advice_code"`
		Message     string `json:"message"`
		// PaymentIntent is "the PaymentIntent object for errors returned on a request involving a PaymentIntent" (docs.stripe.com/api/errors, read 2026-11-02).
		PaymentIntent *struct {
			ID string `json:"id"`
		} `json:"payment_intent"`
	} `json:"error"`
}

func (e apiError) intentID() string {
	if e.Error.PaymentIntent != nil {
		return e.Error.PaymentIntent.ID
	}
	return ""
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
	req.Header.Set("Stripe-Version", c.s.APIVersion)
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
	if resp.StatusCode >= 400 {
		return c.refusal(resp, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("stripe answered garbage: %w", err)
		}
	}
	return nil
}

// refusal maps an HTTP answer of 400 or more to Payrudder's error kinds. A card error is a decline. The rest is retried when Stripe says so
// (Stripe-Should-Retry: true), never when it says not to (false), and otherwise by status (docs.stripe.com/error-low-level, read 2026-11-02): 5xx and 429 (a rate
// limit, or a lock timeout), 409 (idempotency_key_in_use: another request with the same key is still running, "wait and retry with that key and the same
// parameters", which Charge does, the key being the same) and 424 (a dependency outside Stripe failed). A 5xx on a POST made with a key is cached by Stripe, so a
// retry with the key gets the same answer until Stripe drops it. UNVERIFIED: whether a 424 was executed; the retry carries the same key, so it cannot charge twice.
func (c *Client) refusal(resp *http.Response, raw []byte) error {
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
		if code = normalizeDecline(code); code == authRequired {
			return &payments.ActionRequiredError{Ref: e.intentID()}
		}
		return &payments.DeclinedError{Code: code, Final: doNotRetry(e.Error.AdviceCode), Ref: e.intentID()}
	}
	switch retry := resp.Header.Get("Stripe-Should-Retry"); {
	case retry == "false":
	case retry == "true", resp.StatusCode >= 500, resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusConflict, resp.StatusCode == http.StatusFailedDependency:
		msg := fmt.Sprintf("stripe answered %d %s", resp.StatusCode, e.Error.Code)
		if why := resp.Header.Get("Stripe-Rate-Limited-Reason"); why != "" {
			msg += " (" + why + ")"
		}
		return &payments.TransientError{Err: errors.New(strings.TrimSpace(msg))}
	}
	return &Error{Status: resp.StatusCode, Type: e.Error.Type, Code: e.Error.Code, Message: e.Error.Message}
}

// doNotRetry reads Stripe's advice on a declined card payment. Of the three advice codes only do_not_try_again says not to retry: "The card was declined and you
// shouldn't use it again for the same transaction" (docs.stripe.com/declines/card.md, read 2026-11-02); try_again_later and confirm_card_data do not. The decline
// codes themselves never say it: even stolen_card, lost_card and fraudulent only say what not to tell the customer (docs.stripe.com/declines/codes.md), so no decline code
// is classified here. UNVERIFIED: that "the same transaction" covers a later dunning attempt of the same invoice (Payrudder treats it so), and that Stripe sends the
// advice at all for an off-session charge; no account has been used.
func doNotRetry(advice string) bool { return advice == "do_not_try_again" }

// normalizeDecline gives one code to "the customer must come back and authenticate": authentication_required, and authentication_not_handled, the decline an
// issuer gives when the customer tried to go on without it (docs.stripe.com/declines/codes, read 2026-11-02). The Stripe-vocabulary codes otherwise pass as they are.
func normalizeDecline(code string) string {
	if code == "authentication_not_handled" {
		return authRequired
	}
	return code
}

// authRequired is the decline of a payment that needs the customer to authenticate (3-D Secure): not a refusal of the card, so Charge reports it as
// payments.ActionRequiredError and the saga asks the customer to come back instead of retrying.
const authRequired = "authentication_required"

// threeDecimal are the currencies Stripe takes with three decimals but only in whole tens of the smallest unit (the last digit must be 0).
var threeDecimal = map[string]bool{"BHD": true, "JOD": true, "KWD": true, "OMR": true, "TND": true}

// twoDecimalWhole are the currencies Stripe takes as a two-decimal amount whose decimals are always 00, though they have no minor unit (docs.stripe.com/currencies,
// read 2026-11-02: ISK, UGX). Payrudder's minor unit for them is the whole unit, so they would be charged 100 times too little; they are refused until the
// conversion is built in both directions (a notification's amount comes back the other way).
var twoDecimalWhole = map[string]bool{"ISK": true, "UGX": true}

// amount checks and returns the amount as Stripe takes it. Stripe counts in the smallest unit of the currency, like Payrudder, but refuses a three-decimal
// amount whose last digit is not 0 instead of rounding it, so Payrudder refuses it first and never sends a different amount than the invoice's.
func amount(minor int64, currency string) (string, error) {
	if minor <= 0 {
		return "", errors.New("stripe: nothing to charge")
	}
	if twoDecimalWhole[strings.ToUpper(currency)] {
		return "", fmt.Errorf("stripe: %s is not supported yet: Stripe counts it as a two-decimal amount ending in 00 (5 %s is 500), Payrudder counts it whole, so the amount would be 100 times too small", strings.ToUpper(currency), strings.ToUpper(currency))
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
		AdviceCode  string `json:"advice_code"`
	} `json:"last_payment_error"`
}

// final reports Stripe's advice not to try the payment again (last_payment_error.advice_code, docs.stripe.com/api/payment_intents/object.md, read 2026-11-02).
func (p paymentIntent) final() bool {
	return p.LastPaymentError != nil && doNotRetry(p.LastPaymentError.AdviceCode)
}

func (p paymentIntent) declineCode() string {
	if e := p.LastPaymentError; e != nil {
		if e.DeclineCode != "" {
			return normalizeDecline(e.DeclineCode)
		}
		if e.Code != "" {
			return normalizeDecline(e.Code)
		}
	}
	return "payment_failed"
}

// Charge confirms a PaymentIntent off session for the customer's saved payment method (r.PaymentMethod). Stripe's Idempotency-Key makes a retry return the first
// answer. A payment method that is attached to a Stripe Customer must be charged with that customer (docs.stripe.com/api/payment_intents/create, read 2026-11-02:
// "If the payment method is attached to a Customer, you must also provide the ID of that Customer"), so the method is either "pm_..." or "cus_...:pm_...", the
// customer first. A payment that needs the customer present (3-D Secure) is not a decline but a payments.ActionRequiredError: nobody is there to approve it, and the
// saga asks the customer to come back (docs/plans/08). Stripe documents no hosted page for authenticating an off-session card payment: next_action.use_stripe_sdk "is
// only intended to be used by Stripe.js" and redirect_to_url belongs to redirect methods (docs.stripe.com/api/payment_intents/object, read 2026-11-02), so the customer
// is sent to Payrudder's own invoice page, whose Pay button starts Stripe's hosted Checkout.
func (c *Client) Charge(ctx context.Context, r payments.ChargeRequest) (payments.ChargeResult, error) {
	if r.IdempotencyKey == "" {
		return payments.ChargeResult{}, errors.New("payments: idempotency key is mandatory")
	}
	customer, method, hasCustomer := strings.Cut(r.PaymentMethod, ":")
	if !hasCustomer {
		customer, method = "", r.PaymentMethod
	}
	if !strings.HasPrefix(method, "pm_") || hasCustomer && !strings.HasPrefix(customer, "cus_") {
		return payments.ChargeResult{}, errors.New(`stripe: the payment method must be a Stripe PaymentMethod id ("pm_...") or a customer and a payment method ("cus_...:pm_...")`)
	}
	amt, err := amount(r.AmountMinor, r.Currency)
	if err != nil {
		return payments.ChargeResult{}, err
	}
	form := url.Values{
		"amount": {amt}, "currency": {strings.ToLower(r.Currency)}, "payment_method": {method},
		"confirm": {"true"}, "off_session": {"true"},
		"description":                    {"Payrudder invoice " + r.InvoiceID},
		"metadata[payrudder_key]":        {r.IdempotencyKey},
		"metadata[payrudder_invoice_id]": {r.InvoiceID},
	}
	if hasCustomer {
		form.Set("customer", customer)
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
		return payments.ChargeResult{}, &payments.ActionRequiredError{Ref: pi.ID}
	default: // requires_payment_method, requires_confirmation, canceled
		if code := pi.declineCode(); code != authRequired {
			return payments.ChargeResult{}, &payments.DeclinedError{Code: code, Final: pi.final(), Ref: pi.ID}
		}
		return payments.ChargeResult{}, &payments.ActionRequiredError{Ref: pi.ID}
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
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/refunds", form, r.IdempotencyKey, &out); err != nil {
		return payments.RefundResult{}, err
	}
	if out.ID == "" {
		return payments.RefundResult{}, errors.New("stripe: a refund without an id")
	}
	// Stripe documents the refund's status as pending, requires_action, succeeded, failed or canceled (docs.stripe.com/api/refunds/object, read 2026-10-29). A refund
	// that failed or was canceled moved no money and must not be booked as done; the others are accepted (a pending one settles later).
	if out.Status == "failed" || out.Status == "canceled" {
		return payments.RefundResult{}, fmt.Errorf("stripe: the refund %s is %s", out.ID, out.Status)
	}
	return payments.RefundResult{RefundID: out.ID}, nil
}

// Status finds the PaymentIntent that carries Payrudder's key in its metadata. Stripe's search is eventually consistent: "Don't use search in read-after-write flows
// where strict consistency is necessary. Under normal operating conditions, data is searchable in less than a minute. Occasionally, propagation of new or updated
// data can be up to an hour behind during outages" (docs.stripe.com/api/payment_intents/search, read 2026-11-02). So "not found" is only "not found yet": a caller
// treats a not-found younger than a minute, and surely one during an outage, as unknown (payments-worker does, PAYRUDDER_NOTFOUND_GRACE), and Charge relies on
// Stripe's idempotency, not on this: a retried charge returns the first answer whether or not the search has caught up, for at least 24 hours. Search is not
// available to merchants in India. UNVERIFIED against a real account.
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
	return out.Data[0].result()
}

// StatusByRef implements payments.RefStatuser: it retrieves the PaymentIntent by the id the first response of the charge gave (GET /v1/payment_intents/{id}, docs.stripe.com/api/payment_intents/retrieve),
// which is read-after-write consistent, unlike the search Status falls back to. The intent must carry the charge's key in its metadata: a reference that belongs to another
// charge is an error, never an answer.
func (c *Client) StatusByRef(ctx context.Context, ref, key string) (payments.StatusResult, error) {
	if !strings.HasPrefix(ref, "pi_") {
		return payments.StatusResult{}, fmt.Errorf("stripe: %q is not a PaymentIntent id", ref)
	}
	var pi paymentIntent
	if err := c.do(ctx, http.MethodGet, "/v1/payment_intents/"+url.PathEscape(ref), nil, "", &pi); err != nil {
		var e *Error
		if errors.As(err, &e) && e.Status == http.StatusNotFound {
			return payments.StatusResult{State: payments.StateNotFound}, nil
		}
		return payments.StatusResult{}, err
	}
	if pi.Metadata["payrudder_key"] != key {
		return payments.StatusResult{}, fmt.Errorf("stripe: PaymentIntent %s does not belong to the charge %q", ref, key)
	}
	return pi.result()
}

// result is what a PaymentIntent says about its charge: processing is not an answer yet (an error the caller retries), requires_action is waiting for the customer.
func (pi paymentIntent) result() (payments.StatusResult, error) {
	switch pi.Status {
	case "succeeded":
		return payments.StatusResult{State: payments.StateSucceeded, ChargeID: pi.ID}, nil
	case "requires_payment_method", "canceled":
		if code := pi.declineCode(); code == authRequired {
			return payments.StatusResult{State: payments.StateActionRequired, ChargeID: pi.ID}, nil // the customer has not come back to authenticate yet
		}
		return payments.StatusResult{State: payments.StateDeclined, ChargeID: pi.ID, DeclineCode: pi.declineCode(), Final: pi.final()}, nil
	case "requires_action":
		return payments.StatusResult{State: payments.StateActionRequired, ChargeID: pi.ID}, nil
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
	if c.s.SaveCustomer {
		form.Set("customer_creation", "always")
		form.Set("payment_intent_data[setup_future_usage]", "off_session")
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

// CreateSetupCheckout makes a hosted Checkout Session in setup mode: the customer enters a card, nothing is charged, and Stripe "automatically attaches the resulting
// payment method to that Customer" (docs.stripe.com/payments/save-and-reuse.md, read 2026-11-02, the Customers v1 variant). The Customer is made first
// (POST /v1/customers) unless r.CustomerRef is known, because the session takes an existing one; currency is required in setup mode "when allowed_payment_method_types
// is not set", which this never sets. The Customer's key is the Payrudder customer's, so a second click inside 24 hours makes no second Customer. The session carries
// payrudder_customer_id and payrudder_external_id in its metadata, which ConfirmNotification reads back from checkout.session.completed. The seller's terms for saving
// and later charging a card (the agreement the guide's "Compliance" section asks for, with the customer's consent) are not collected here: they belong to the page the
// customer comes from. UNVERIFIED: that Checkout's own text suffices as that consent, and everything against a real account.
func (c *Client) CreateSetupCheckout(ctx context.Context, r payments.SetupRequest) (payments.SetupCheckout, error) {
	if r.IdempotencyKey == "" || r.ReturnURL == "" || len(r.Currency) != 3 || r.PayrudderCustomerID == "" || r.ExternalID == "" {
		return payments.SetupCheckout{}, errors.New("stripe: a setup checkout needs a key, a return address, a currency and the customer")
	}
	ref := r.CustomerRef
	if ref == "" {
		form := url.Values{"metadata[payrudder_customer_id]": {r.PayrudderCustomerID}, "metadata[payrudder_external_id]": {r.ExternalID}}
		if r.Email != "" {
			form.Set("email", r.Email)
		}
		if r.Name != "" {
			form.Set("name", r.Name)
		}
		var cus struct {
			ID string `json:"id"`
		}
		if err := c.do(ctx, http.MethodPost, "/v1/customers", form, "payrudder-customer-"+r.PayrudderCustomerID, &cus); err != nil {
			return payments.SetupCheckout{}, err
		}
		if cus.ID == "" {
			return payments.SetupCheckout{}, errors.New("stripe: a customer without an id")
		}
		ref = cus.ID
	}
	form := url.Values{
		"mode": {"setup"}, "customer": {ref}, "currency": {strings.ToLower(r.Currency)}, "success_url": {r.ReturnURL}, "cancel_url": {r.ReturnURL},
		"metadata[payrudder_customer_id]": {r.PayrudderCustomerID}, "metadata[payrudder_external_id]": {r.ExternalID},
	}
	var out struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/checkout/sessions", form, r.IdempotencyKey, &out); err != nil {
		return payments.SetupCheckout{}, err
	}
	if out.URL == "" {
		return payments.SetupCheckout{}, errors.New("stripe: the checkout session has no url")
	}
	return payments.SetupCheckout{URL: out.URL, Ref: out.ID, CustomerRef: ref}, nil
}

// savedMethod turns a completed setup-mode Checkout Session into the notification that a method was saved. The session names the SetupIntent, which is retrieved for
// its payment_method ("Retrieve the SetupIntent to get the payment_method ID", docs.stripe.com/payments/save-and-reuse.md, read 2026-11-02): a SetupIntent that did
// not succeed, a session Payrudder did not make (no ids in its metadata) and a method on no customer are ignored, because nothing could be charged with them.
func (c *Client) savedMethod(ctx context.Context, n payments.Notification, customer, setupIntent string, meta map[string]string) (payments.Notification, error) {
	if setupIntent == "" || meta["payrudder_customer_id"] == "" || meta["payrudder_external_id"] == "" {
		return n, nil
	}
	var si struct {
		Status        string `json:"status"`
		PaymentMethod string `json:"payment_method"`
		Customer      string `json:"customer"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/setup_intents/"+url.PathEscape(setupIntent), nil, "", &si); err != nil {
		return n, err
	}
	if si.Customer != "" {
		customer = si.Customer
	}
	if si.Status != "succeeded" || si.PaymentMethod == "" || customer == "" {
		return n, nil
	}
	n.Kind, n.ProviderRef = payments.KindPaymentMethod, si.PaymentMethod
	n.PaymentMethod = &payments.SavedPaymentMethod{CustomerRef: customer, Token: customer + ":" + si.PaymentMethod,
		PayrudderCustomerID: meta["payrudder_customer_id"], ExternalID: meta["payrudder_external_id"]}
	return n, nil
}

// disputeOutcome reads a Dispute's status (docs.stripe.com/api/disputes/object, read 2026-11-02). warning_* are inquiries: no money is taken back, and Stripe closes an
// unanswered one after 120 days with warning_closed (docs.stripe.com/disputes/how-disputes-work). prevented is "a dispute that was prevented from becoming a formal
// chargeback". ok is false for a status this code does not know: the event is ignored rather than guessed at.
func disputeOutcome(status string) (outcome payments.DisputeOutcome, formal, ok bool) {
	switch status {
	case "needs_response", "under_review", "appeal_needs_response", "appeal_under_review":
		return payments.DisputeOpen, true, true
	case "warning_needs_response", "warning_under_review":
		return payments.DisputeOpen, false, true
	case "won":
		return payments.DisputeWon, true, true
	case "lost":
		return payments.DisputeLost, true, true
	case "warning_closed", "prevented":
		return payments.DisputeWon, false, true
	}
	return "", false, false
}

// dispute turns the five dispute events (charge.dispute.created, .updated, .closed, .funds_withdrawn, .funds_reinstated: docs.stripe.com/api/events/types) into one
// notification about the case as the object says it now. Which payment it is about is the PaymentIntent, the reference the ledger holds for a Stripe charge: the
// dispute names it, or, when "payment_intent" is null, the Charge does and is retrieved ("If payment_intent is null, retrieve the Charge and use its payment_intent
// field", docs.stripe.com/billing/subscriptions/webhooks, read 2026-11-02). A charge with no PaymentIntent is not one of ours: the event is ignored.
func (c *Client) dispute(ctx context.Context, n payments.Notification, object json.RawMessage) (payments.Notification, error) {
	var d struct {
		ID              string  `json:"id"`
		Status          string  `json:"status"`
		Reason          string  `json:"reason"`
		Amount          int64   `json:"amount"`
		Currency        string  `json:"currency"`
		Charge          string  `json:"charge"`
		PaymentIntent   *string `json:"payment_intent"`
		EvidenceDetails struct {
			DueBy int64 `json:"due_by"`
		} `json:"evidence_details"`
	}
	if err := json.Unmarshal(object, &d); err != nil {
		return n, fmt.Errorf("stripe: dispute: %w", err)
	}
	outcome, formal, ok := disputeOutcome(d.Status)
	if !ok || d.ID == "" {
		return n, nil
	}
	pi := ""
	if d.PaymentIntent != nil {
		pi = *d.PaymentIntent
	}
	if pi == "" && d.Charge != "" {
		var ch struct {
			PaymentIntent string `json:"payment_intent"`
		}
		if err := c.do(ctx, http.MethodGet, "/v1/charges/"+url.PathEscape(d.Charge), nil, "", &ch); err != nil {
			return n, err
		}
		pi = ch.PaymentIntent
	}
	if pi == "" {
		return n, nil
	}
	ev := &payments.DisputeEvent{ID: d.ID, Status: d.Status, Outcome: outcome, Formal: formal, Reason: d.Reason}
	if d.EvidenceDetails.DueBy > 0 {
		ev.EvidenceDue = time.Unix(d.EvidenceDetails.DueBy, 0).UTC()
	}
	n.Kind, n.ProviderRef, n.Currency, n.AmountMinor, n.Dispute = payments.KindDispute, pi, strings.ToUpper(d.Currency), d.Amount, ev
	return n, nil
}

// WebhookEvents are the event types the adapter turns into notifications (ConfirmNotification); the endpoint RegisterWebhook creates is enabled for exactly these.
var WebhookEvents = []string{
	"payment_intent.succeeded", "payment_intent.payment_failed",
	"checkout.session.completed", "checkout.session.async_payment_succeeded", "checkout.session.async_payment_failed",
	"refund.updated", "refund.failed",
	"charge.dispute.created", "charge.dispute.updated", "charge.dispute.closed", "charge.dispute.funds_withdrawn", "charge.dispute.funds_reinstated",
}

// RegisterWebhook creates the webhook endpoint at Stripe (POST /v1/webhook_endpoints, docs.stripe.com/api/webhook_endpoints/create, read 2026-11-02): enabled for
// WebhookEvents and generating its events with the API version the adapter reads (api_version, one of the documented values), so a payload is shaped as
// ConfirmNotification expects whatever the account's own default is. The endpoint is an account endpoint (connect defaults to false) and sends full objects
// ("snapshot"): the call has no parameter for the thin payloads of event destinations. The signing secret is "populated" in this answer (the page says
// nothing of a later retrieval, so it is taken now). The Idempotency-Key derives from the address, the events and the version: a repeated click inside Stripe's
// 24 hours gets the first answer instead of a second endpoint; after that a second call creates a second endpoint, and the old one is deleted at Stripe.
// UNVERIFIED: the permission a restricted key needs for it; behaviour against a real account.
func (c *Client) RegisterWebhook(ctx context.Context, r payments.WebhookRegistration) (payments.WebhookEndpoint, error) {
	u, err := url.Parse(r.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return payments.WebhookEndpoint{}, errors.New("stripe: the webhook address must be an https:// URL")
	}
	form := url.Values{"url": {r.URL}, "api_version": {c.s.APIVersion}, "description": {"Payrudder"}}
	for i, e := range WebhookEvents {
		form.Set("enabled_events["+strconv.Itoa(i)+"]", e)
	}
	sum := sha256.Sum256([]byte(r.URL + "|" + c.s.APIVersion + "|" + strings.Join(WebhookEvents, ",")))
	var out struct {
		ID            string   `json:"id"`
		Secret        string   `json:"secret"`
		EnabledEvents []string `json:"enabled_events"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/webhook_endpoints", form, "payrudder-webhook-"+hex.EncodeToString(sum[:16]), &out); err != nil {
		return payments.WebhookEndpoint{}, err
	}
	if out.ID == "" || out.Secret == "" {
		return payments.WebhookEndpoint{}, errors.New("stripe: the webhook endpoint came back without an id or a signing secret")
	}
	return payments.WebhookEndpoint{ID: out.ID, Secret: out.Secret, Events: out.EnabledEvents}, nil
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
	if c.s.WebhookSecret == "" {
		return fmt.Errorf("%w: this account has no webhook secret yet: register the webhook endpoint", ErrBadSignature)
	}
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
func (c *Client) ConfirmNotification(ctx context.Context, body []byte, header http.Header) (payments.Notification, error) {
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
			Mode          string            `json:"mode"`
			Customer      string            `json:"customer"`
			SetupIntent   string            `json:"setup_intent"`
			PaymentIntent string            `json:"payment_intent"`
			PaymentStatus string            `json:"payment_status"`
			AmountTotal   int64             `json:"amount_total"`
			Currency      string            `json:"currency"`
			Metadata      map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(e.Data.Object, &s); err != nil {
			return n, fmt.Errorf("stripe: checkout session: %w", err)
		}
		if s.Mode == "setup" {
			// A card saved: no payment, so payment_status is no_payment_required and nothing below applies.
			if e.Type != "checkout.session.completed" {
				return n, nil
			}
			return c.savedMethod(ctx, n, s.Customer, s.SetupIntent, s.Metadata)
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
	case "charge.dispute.created", "charge.dispute.updated", "charge.dispute.closed", "charge.dispute.funds_withdrawn", "charge.dispute.funds_reinstated":
		return c.dispute(ctx, n, e.Data.Object)
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
	_ payments.WebhookRegistrar        = (*Client)(nil)
	_ payments.SetupProvider           = (*Client)(nil)
)
