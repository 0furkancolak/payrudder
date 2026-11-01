// Package iyzico is the iyzico adapter of payments.Provider (https://docs.iyzico.com).
//
// What it does for Payrudder: charges a stored card (cardUserKey and cardToken, the "pm" token of the customer) through
// POST /payment/auth, refunds a payment through POST /v2/payment/refund, looks a payment up through POST /payment/detail
// and confirms webhook notifications by looking the payment up (a hosted-page payment by its token, through the checkout form's retrieve call). A payment
// is paid only when iyzico says SUCCESS and its fraud filters say 1 (approved): 0 is a payment under review, which is pending, and -1 is one the filters
// rejected. iyzico does not schedule renewals or retry failed payments for us: Payrudder drives both (Capabilities).
//
// It was written from iyzico's public documentation and tested against recorded fixtures. Behaviour that the documentation
// does not settle is marked "UNVERIFIED" below and is checked by the sandbox test, which is skipped unless
// IYZICO_SANDBOX_API_KEY and IYZICO_SANDBOX_SECRET_KEY are set.
package iyzico

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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

	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/payments"
)

// Name is the registry name.
const Name = "iyzico"

// Endpoints.
const (
	LiveURL    = "https://api.iyzipay.com"
	SandboxURL = "https://sandbox-api.iyzipay.com"
)

// Buyer is what iyzico wants to know about the payer. iyzico requires these fields on every payment, also on a stored-card one.
// Payrudder does not hold them per customer yet, so the account's defaults are sent; real values per customer are a later addition.
type Buyer struct {
	Name           string `json:"name"`
	Surname        string `json:"surname"`
	IdentityNumber string `json:"identity_number"`
	Address        string `json:"address"`
	City           string `json:"city"`
	Country        string `json:"country"`
	ZipCode        string `json:"zip_code"`
	IP             string `json:"ip"`
	GSMNumber      string `json:"gsm_number"`
	Email          string `json:"email"`
}

// Settings is the JSON a tenant's iyzico account is stored as.
type Settings struct {
	APIKey    string `json:"api_key"`
	SecretKey string `json:"secret_key"`
	// BaseURL defaults to the live endpoint; use SandboxURL for a sandbox account.
	BaseURL string `json:"base_url,omitempty"`
	Buyer   Buyer  `json:"buyer"`
	// NotFoundCodes are the errorCode values that mean "no payment with this conversation id" in a /payment/detail answer.
	// UNVERIFIED: the documentation does not list the code, so none is assumed. Until the sandbox settles it, an
	// unknown outcome is reported as an error (never as "not found"), so Payrudder cannot charge a second time.
	NotFoundCodes []string `json:"not_found_codes,omitempty"`
	// SystemErrorCodes are errorCode values that mean a failure at iyzico worth retrying instead of a decline, on top of the ones iyzico documents as "try again".
	SystemErrorCodes []string `json:"system_error_codes,omitempty"`
	// RequireWebhookSignature refuses a notification that carries no X-IYZ-SIGNATURE-V3 header. Set it once iyzico has enabled the signature on the account
	// (it is a feature to ask for: entegrasyon@iyzico.com, docs.iyzico.com/en/advanced/webhook, read 2026-11-02). A header that is there is always checked.
	RequireWebhookSignature bool `json:"require_webhook_signature,omitempty"`
}

// Client is an iyzico account.
type Client struct {
	s      Settings
	http   *http.Client
	random func() string
}

// New returns a client with the given per-request timeout (default 10 s).
func New(s Settings, timeout time.Duration) (*Client, error) {
	if s.APIKey == "" || s.SecretKey == "" {
		return nil, errors.New("iyzico: api_key and secret_key are required")
	}
	if s.BaseURL == "" {
		s.BaseURL = LiveURL
	}
	if _, err := url.ParseRequestURI(s.BaseURL); err != nil {
		return nil, fmt.Errorf("iyzico: base_url: %w", err)
	}
	s.BaseURL = strings.TrimRight(s.BaseURL, "/")
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{s: s, http: &http.Client{Timeout: timeout}, random: randomKey}, nil
}

// Factory builds a Client from stored settings, for payments.Registry.
func Factory(settings []byte) (payments.Provider, error) {
	var s Settings
	if err := json.Unmarshal(settings, &s); err != nil {
		return nil, fmt.Errorf("iyzico: settings: %w", err)
	}
	return New(s, 0)
}

// Name implements payments.PaymentProvider.
func (c *Client) Name() string { return Name }

// Livemode implements payments.LiveModer: the documented live and sandbox hosts are told apart, any other endpoint is not known.
func (c *Client) Livemode() (live, known bool) {
	switch c.s.BaseURL {
	case LiveURL:
		return true, true
	case SandboxURL:
		return false, true
	}
	return false, false
}

// KeyMode implements payments.KeyModer: the keys of an iyzico sandbox account start with "sandbox-". A key without it is not known to be live (the sandbox host
// and iyzico itself refuse it), so only the sandbox format is told.
func (c *Client) KeyMode() (live, known bool) {
	if strings.HasPrefix(c.s.APIKey, "sandbox-") {
		return false, true
	}
	return false, false
}

// Capabilities implements payments.Provider. iyzico charges and refunds; Payrudder schedules renewals, retries failed payments and
// issues the invoice. The currency list is the intersection of iyzico's and Payrudder's.
func (c *Client) Capabilities() payments.Capabilities { return Caps() }

// Caps are iyzico's capabilities, which do not depend on an account: routing rules are checked against them before any account exists. Neither charges nor
// refunds are idempotent (a repeated refund is a second refund, docs/iyzico-compatibility-matrix.md), so payments-worker guards both itself.
func Caps() payments.Capabilities {
	return payments.Capabilities{Name: Name, Refunds: true, HostedCheckout: true, Currencies: []string{"TRY", "USD", "EUR", "GBP", "CHF"}}
}

func randomKey() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return strconv.FormatInt(time.Now().UnixMilli(), 10) + hex.EncodeToString(b[:])
}

// Authorization builds the IYZWSv2 header value: base64("apiKey:K&randomKey:R&signature:S") with S the hex HMAC-SHA256 of
// randomKey + path + body under the secret key. An empty body signs randomKey + path.
func Authorization(apiKey, secretKey, randomKey, path string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(randomKey + path))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	return "IYZWSv2 " + base64.StdEncoding.EncodeToString([]byte("apiKey:"+apiKey+"&randomKey:"+randomKey+"&signature:"+sig))
}

// text is a JSON string or number read as its text: iyzico documents an error code as a string, but its own example of the answer to a request over the limit
// has errorCode 50000 as a number (docs.iyzico.com/en/getting-started/preliminaries/limiters, read 2026-11-02), and a notification sends a payment id as a number.
type text string

func (t *text) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*t = text(v)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*t = text(n.String())
	return nil
}

// response is the part common to every iyzico answer.
type response struct {
	Status         string `json:"status"`
	ErrorCode      text   `json:"errorCode"`
	ErrorMessage   string `json:"errorMessage"`
	ErrorGroup     string `json:"errorGroup"`
	ConversationID string `json:"conversationId"`
	PaymentID      string `json:"paymentId"`
	PaymentStatus  string `json:"paymentStatus"`
	BasketID       string `json:"basketId"`
	Token          string `json:"token"`
	// FraudStatus is iyzico's fraud filter verdict on a payment: 1 approved, 0 under review (wait for the notification), -1 rejected. Nil when the answer has none.
	FraudStatus *int   `json:"fraudStatus"`
	Retryable   bool   `json:"retryable"`
	RefundRef   string `json:"refundHostReference"`
	Price       any    `json:"price"`
}

func (r response) ok() bool { return strings.EqualFold(r.Status, "success") }

// Fraud filter verdicts (docs.iyzico.com/en/payment-methods/api/non-3ds/non-3ds-implementation/create-payment, read 2026-11-02: "ship only when the value is 1; for 0, wait for notification").
const (
	fraudRejected = -1
	fraudReview   = 0
)

// fraud is the verdict of the fraud filters, or approved when the answer has none.
func (r response) fraud() int {
	if r.FraudStatus == nil {
		return 1
	}
	return *r.FraudStatus
}

// state reads the final state of a payment from a /payment/detail or checkout form answer. Only an explicit SUCCESS that the fraud filters approved is a success,
// and then the payment has an id (a refund needs it); a SUCCESS under review is pending, one the filters rejected is a decline, and an answer that says nothing
// about the payment is an error, never a guess.
func (r response) state() (payments.StatusResult, error) {
	switch strings.ToUpper(r.PaymentStatus) {
	case "SUCCESS":
		if r.PaymentID == "" {
			return payments.StatusResult{}, errors.New("iyzico: a successful payment without a paymentId")
		}
		switch r.fraud() {
		case fraudReview:
			return payments.StatusResult{}, &payments.PendingError{Ref: r.PaymentID}
		case fraudRejected:
			return payments.StatusResult{State: payments.StateDeclined, ChargeID: r.PaymentID, DeclineCode: "fraudulent"}, nil
		}
		return payments.StatusResult{State: payments.StateSucceeded, ChargeID: r.PaymentID}, nil
	case "FAILURE":
		return payments.StatusResult{State: payments.StateDeclined, ChargeID: r.PaymentID, DeclineCode: "FAILURE"}, nil
	}
	return payments.StatusResult{}, fmt.Errorf("iyzico: payment %s is %q, not final", r.PaymentID, r.PaymentStatus)
}

func (c *Client) do(ctx context.Context, path string, in any) (response, error) {
	var out response
	err := c.doInto(ctx, path, in, &out)
	return out, err
}

// doInto signs and sends a request and decodes the answer into out.
func (c *Client) doInto(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	rnd := c.random()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.s.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-iyzi-rnd", rnd)
	req.Header.Set("Authorization", Authorization(c.s.APIKey, c.s.SecretKey, rnd, path, body))
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
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return &payments.TransientError{Err: fmt.Errorf("iyzico answered %d", resp.StatusCode)}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("iyzico answered garbage (%d): %w", resp.StatusCode, err)
	}
	return nil
}

// price renders minor units as iyzico's decimal string in the currency's exponent: 1116 TRY is "11.16", 1116 JPY "1116".
func price(minor int64, currency string) (string, error) {
	cur, ok := money.LookupCurrency(currency)
	if !ok {
		return "", fmt.Errorf("iyzico: unsupported currency %q", currency)
	}
	if minor < 0 {
		return "", errors.New("iyzico: negative amount")
	}
	return money.FormatMinor(minor, cur.Exponent), nil
}

// SplitPaymentMethod reads a customer's payment method token "cardUserKey:cardToken".
func SplitPaymentMethod(pm string) (userKey, token string, err error) {
	userKey, token, ok := strings.Cut(pm, ":")
	if !ok || userKey == "" || token == "" {
		return "", "", errors.New(`iyzico: payment method must be "cardUserKey:cardToken"`)
	}
	return userKey, token, nil
}

// Error is a request iyzico refused that is no decline of the payment: a request or credentials Payrudder got wrong (the authentication, general and validation
// codes of docs.iyzico.com/en/add-ons/error-codes). Retrying the same request is refused the same, and the customer is not to blame, so it is not a decline.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return "iyzico refused the request (" + e.Code + "): " + e.Message }

// issuerDeclines are the card-issuer error groups iyzico documents (docs.iyzico.com/en/add-ons/error-codes, read 2026-11-02, "Bank Error Codes"; the page says it
// lists the commonly met ones) in Payrudder's decline vocabulary, the snake_case one mock-psp and the Stripe adapter use. A group or code that is not here is
// passed on as iyzico spells it.
var issuerDeclines = []struct{ group, code, decline string }{
	{"DO_NOT_HONOUR", "10005", "do_not_honor"},
	{"FRAUD_SUSPECT", "10034", "fraudulent"},
	{"PICKUP_CARD", "10041", "pickup_card"},
	{"LOST_CARD", "10043", "lost_card"},
	{"NOT_SUFFICIENT_FUNDS", "10051", "insufficient_funds"},
	{"EXPIRED_CARD", "10054", "expired_card"},
	{"INVALID_CVC2", "10084", "incorrect_cvc"},
	{"INVALID_CARD_NUMBER", "10215", "incorrect_number"},
	{"DEBIT_CARDS_REQUIRES_3DS", "10217", "authentication_required"}, // the customer must come back and authenticate, as with Stripe's code of the same name
	{"", "6001", "fraudulent"},                                       // validation error 6001: "Payment request has not passed Fraud check"
}

// tryAgain are the groups and codes iyzico documents as a failure to try again later: the bank or a system could not answer ("Error occurred at the bank,
// please try again later"), codes 1 and 2 ("System error occurred. Try again"), and 50000, the answer to a request over the limit.
var tryAgain = map[string]bool{
	"COMMUNICATION_OR_SYSTEM_ERROR": true, "NO_SUCH_ISSUER": true, "REQUEST_TIMEOUT": true, "ISSUER_OR_SWITCH_INOPERATIVE": true, "REMOTE_ERROR": true, "LOCAL_ERROR": true,
	"10214": true, "10216": true, "10219": true, "10228": true, "10241": true, "10242": true,
	"1": true, "2": true, "50000": true,
}

// requestError reports whether a code is one of iyzico's general (3 to 27), authentication (1000 to 1009) or validation (5001 and up, but not 6001) codes. Their table
// lists "retry: true" for all of them, which means "after fixing the request", so the response's retryable flag is not read for them.
func requestError(code string) bool {
	n, err := strconv.Atoi(code)
	return err == nil && (n >= 3 && n <= 27 || n >= 1000 && n <= 1009 || n >= 5001 && n < 6000)
}

// classify maps a failed answer to Payrudder's error kinds.
func (c *Client) classify(r response) error {
	code := string(r.ErrorCode)
	transient := func() error { return &payments.TransientError{Err: fmt.Errorf("iyzico %s: %s", code, r.ErrorMessage)} }
	for _, sys := range c.s.SystemErrorCodes {
		if code == sys {
			return transient()
		}
	}
	if tryAgain[code] || tryAgain[r.ErrorGroup] {
		return transient()
	}
	if requestError(code) {
		return &Error{Code: code, Message: r.ErrorMessage}
	}
	for _, d := range issuerDeclines {
		if d.group != "" && d.group == r.ErrorGroup || d.code == code {
			return &payments.DeclinedError{Code: d.decline}
		}
	}
	if r.Retryable {
		return transient()
	}
	if r.ErrorGroup != "" {
		code = r.ErrorGroup // e.g. NOT_PERMITTED_TO_CARDHOLDER: more useful to dunning and to a human than the number
	}
	return &payments.DeclinedError{Code: code}
}

// Charge implements payments.PaymentProvider. The idempotency key is sent as the conversationId and is how Status finds the payment
// again; iyzico does not reject a repeated conversationId, so a charge is only retried after Status says it did not happen.
func (c *Client) Charge(ctx context.Context, r payments.ChargeRequest) (payments.ChargeResult, error) {
	if r.IdempotencyKey == "" {
		return payments.ChargeResult{}, errors.New("payments: idempotency key is mandatory")
	}
	userKey, token, err := SplitPaymentMethod(r.PaymentMethod)
	if err != nil {
		return payments.ChargeResult{}, err
	}
	amount, err := price(r.AmountMinor, r.Currency)
	if err != nil {
		return payments.ChargeResult{}, err
	}
	b := c.s.Buyer
	addr := map[string]string{"address": b.Address, "zipCode": b.ZipCode, "contactName": b.Name + " " + b.Surname, "city": b.City, "country": b.Country}
	req := map[string]any{
		"locale": "en", "conversationId": r.IdempotencyKey, "price": amount, "paidPrice": amount, "currency": r.Currency,
		"installment": 1, "basketId": r.InvoiceID, "paymentChannel": "WEB", "paymentGroup": "SUBSCRIPTION",
		"paymentCard": map[string]string{"cardUserKey": userKey, "cardToken": token},
		"buyer": map[string]string{
			"id": r.InvoiceID, "name": b.Name, "surname": b.Surname, "identityNumber": b.IdentityNumber, "email": b.Email,
			"gsmNumber": b.GSMNumber, "registrationAddress": b.Address, "city": b.City, "country": b.Country, "zipCode": b.ZipCode, "ip": b.IP,
		},
		"shippingAddress": addr, "billingAddress": addr,
		"basketItems": []map[string]string{{"id": r.InvoiceID, "name": "Invoice " + r.InvoiceID, "category1": "Subscription", "itemType": "VIRTUAL", "price": amount}},
	}
	out, err := c.do(ctx, "/payment/auth", req)
	if err != nil {
		return payments.ChargeResult{}, err
	}
	if !out.ok() {
		return payments.ChargeResult{}, c.classify(out)
	}
	if out.PaymentID == "" {
		return payments.ChargeResult{}, errors.New("iyzico: a successful payment without a paymentId")
	}
	switch out.fraud() {
	case fraudReview: // taken but not released: the merchant waits for iyzico's notification
		return payments.ChargeResult{}, &payments.PendingError{Ref: out.PaymentID}
	case fraudRejected:
		return payments.ChargeResult{}, &payments.DeclinedError{Code: "fraudulent"}
	}
	return payments.ChargeResult{ChargeID: out.PaymentID}, nil
}

// Refund implements payments.PaymentProvider: the whole amount given, against the payment (ChargeID).
func (c *Client) Refund(ctx context.Context, r payments.RefundRequest) (payments.RefundResult, error) {
	if r.ChargeID == "" {
		return payments.RefundResult{}, errors.New("iyzico: a refund needs the charge id (the paymentId)")
	}
	amount, err := price(r.AmountMinor, r.Currency)
	if err != nil {
		return payments.RefundResult{}, err
	}
	out, err := c.do(ctx, "/v2/payment/refund", map[string]any{
		"locale": "en", "conversationId": r.IdempotencyKey, "paymentId": r.ChargeID, "price": amount, "currency": r.Currency, "ip": c.s.Buyer.IP,
	})
	if err != nil {
		return payments.RefundResult{}, err
	}
	if !out.ok() {
		return payments.RefundResult{}, c.classify(out)
	}
	id := out.RefundRef
	if id == "" {
		id = out.PaymentID
	}
	return payments.RefundResult{RefundID: id}, nil
}

// Status implements payments.PaymentProvider: did the charge made under this idempotency key (the conversationId) go through?
// UNVERIFIED: that /payment/detail accepts paymentConversationId without paymentId. See Settings.NotFoundCodes.
func (c *Client) Status(ctx context.Context, key string) (payments.StatusResult, error) {
	out, err := c.do(ctx, "/payment/detail", map[string]any{"locale": "en", "conversationId": key, "paymentConversationId": key})
	if err != nil {
		return payments.StatusResult{}, err
	}
	if out.ok() {
		return out.state()
	}
	for _, code := range c.s.NotFoundCodes {
		if string(out.ErrorCode) == code {
			return payments.StatusResult{State: payments.StateNotFound}, nil
		}
	}
	return payments.StatusResult{}, fmt.Errorf("iyzico: status lookup failed: %s %s", out.ErrorCode, out.ErrorMessage)
}

// healthBIN is the card BIN the health check looks up: the first 8 digits of a card on iyzico's published test-card list (5528790000000008, docs.iyzico.com/en/add-ons/test-cards,
// read 2026-11-02), a public number that identifies no cardholder. The BIN service takes 8 digits (docs.iyzico.com/en/advanced/installment-and-bin-service, same date).
const healthBIN = "55287900"

// CheckHealth implements payments.HealthChecker with /payment/bin/check, a signed, read-only lookup that charges nothing: iyzico answers "failure" with an
// authentication code (1000 to 1009) when the signature (the api key and secret key) is not accepted. An answer that says the BIN is not found (5066, 10213) was
// given to a request iyzico accepted, so it is healthy. UNVERIFIED against iyzico's sandbox or live host (the tests run against a fake); the BIN service is limited to 50 requests a minute.
func (c *Client) CheckHealth(ctx context.Context) error {
	out, err := c.do(ctx, "/payment/bin/check", map[string]any{"locale": "en", "conversationId": "payrudder-health", "binNumber": healthBIN})
	if err != nil {
		return err
	}
	if out.ok() || out.ErrorCode == "5066" || out.ErrorCode == "10213" {
		return nil
	}
	return c.classify(out)
}

// WebhookEvent is an iyzico payment notification, in either of its two payment formats (docs.iyzico.com/en/advanced/webhook, read 2026-11-02). A payment made through
// the API (non-3DS, 3-D Secure) names its payment in paymentId. One made on a hosted page (the checkout form, Pay with iyzico) has no paymentId: it names the checkout
// form's token and the payment in iyziPaymentId.
type WebhookEvent struct {
	Type           string `json:"iyziEventType"`
	Time           int64  `json:"iyziEventTime"`
	Reference      string `json:"iyziReferenceCode"`
	PaymentID      text   `json:"paymentId"`
	IyziPaymentID  text   `json:"iyziPaymentId"`
	Token          string `json:"token"`
	ConversationID string `json:"paymentConversationId"`
	Status         string `json:"status"`
}

// ID returns the id of the payment as a string (iyzico sends a number): paymentId, or iyziPaymentId when the notification has none.
func (e WebhookEvent) ID() string {
	if e.PaymentID != "" {
		return string(e.PaymentID)
	}
	return string(e.IyziPaymentID)
}

// Hosted reports whether the notification is about a payment on a hosted page: it names a checkout form token instead of a paymentId.
func (e WebhookEvent) Hosted() bool { return e.PaymentID == "" && e.Token != "" }

// ParseWebhook decodes a notification body.
func ParseWebhook(body []byte) (WebhookEvent, error) {
	var e WebhookEvent
	if err := json.Unmarshal(body, &e); err != nil {
		return e, fmt.Errorf("iyzico: webhook body: %w", err)
	}
	if e.Reference == "" || (e.ID() == "" && e.Token == "") {
		return e, errors.New("iyzico: webhook without iyziReferenceCode and a paymentId or token")
	}
	return e, nil
}

// SignatureHeader is the header that carries the notification's signature.
const SignatureHeader = "X-IYZ-SIGNATURE-V3"

// notificationSignature is the signature iyzico puts in X-IYZ-SIGNATURE-V3: the hex HMAC-SHA256, under the secret key, of the secret key followed by iyziEventType,
// the payment id, the conversation id and the status, with the checkout form's token between the payment id and the conversation id for a hosted-page notification.
func notificationSignature(secret string, e WebhookEvent) string {
	msg := secret + e.Type + e.ID() + e.ConversationID + e.Status
	if e.Token != "" {
		msg = secret + e.Type + string(e.IyziPaymentID) + e.Token + e.ConversationID + e.Status
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

// NotificationSignature builds the X-IYZ-SIGNATURE-V3 value of a notification (what iyzico does when it sends). Used to test and to simulate.
func NotificationSignature(secret string, e WebhookEvent) string {
	return notificationSignature(secret, e)
}

// verifySignature checks X-IYZ-SIGNATURE-V3 when the notification has it, and refuses a notification without it when the account says iyzico signs
// (Settings.RequireWebhookSignature). The check is on top of confirming the payment with iyzico, which stays the proof that a payment happened: the signature
// only shows the notification is iyzico's, and an account where iyzico has not enabled the feature sends none.
func (c *Client) verifySignature(e WebhookEvent, header http.Header) error {
	got := header.Get(SignatureHeader)
	if got == "" {
		if c.s.RequireWebhookSignature {
			return fmt.Errorf("%w: no %s header", payments.ErrUnverified, SignatureHeader)
		}
		return nil
	}
	if !hmac.Equal([]byte(strings.ToLower(got)), []byte(notificationSignature(c.s.SecretKey, e))) {
		return fmt.Errorf("%w: the %s header does not match", payments.ErrUnverified, SignatureHeader)
	}
	return nil
}

// Confirm asks iyzico what the notified payment really is. A notification can be checked with its signature (X-IYZ-SIGNATURE-V3, an account feature), but
// the proof that a payment happened stays the lookup: Payrudder never acts on the body of a notification alone. A payment made through the API is looked up with its
// paymentId; one made on a hosted page with the token of its checkout form (the checkout form's retrieve call).
//
// The body's conversation id is only a claim: the payment it names must be the charge of that conversation. Charge sends the invoice id as
// the basketId and the idempotency key ("<invoice id>:charge:<attempt>") as the conversationId, so the payment's basketId must be the invoice of the key.
// Otherwise a real payment of one invoice could be notified as the payment of another (UNVERIFIED: that /payment/detail answers with the basketId; one that
// does not is refused rather than believed).
func (c *Client) Confirm(ctx context.Context, e WebhookEvent) (payments.StatusResult, error) {
	invoice, ok := invoiceOfKey(e.ConversationID)
	if !ok {
		return payments.StatusResult{}, fmt.Errorf("%w: the conversation id %q is not one of Payrudder's charges", payments.ErrUnverified, e.ConversationID)
	}
	var out response
	var err error
	if e.Hosted() {
		out, err = c.do(ctx, retrieveCheckoutFormPath, map[string]any{"locale": "en", "conversationId": e.ConversationID, "token": e.Token})
	} else {
		out, err = c.do(ctx, "/payment/detail", map[string]any{"locale": "en", "conversationId": e.ConversationID, "paymentId": e.ID()})
	}
	if err != nil {
		return payments.StatusResult{}, err
	}
	if !out.ok() {
		return payments.StatusResult{}, fmt.Errorf("iyzico: cannot confirm payment %s: %s %s", e.ID(), out.ErrorCode, out.ErrorMessage)
	}
	if out.BasketID != invoice {
		return payments.StatusResult{}, fmt.Errorf("%w: payment %s is not a payment of conversation %q", payments.ErrUnverified, e.ID(), e.ConversationID)
	}
	if e.Hosted() && out.Token != "" && out.Token != e.Token {
		return payments.StatusResult{}, fmt.Errorf("%w: the checkout form answered for another token", payments.ErrUnverified)
	}
	return out.state()
}

// retrieveCheckoutFormPath is the checkout form's retrieve call (CF-Retrieve), which answers for the token iyzico posts to the callback and names in a notification
// (docs.iyzico.com/en/payment-methods/checkoutform/cf-implementation/cf-retrieve, read 2026-11-02). Its answer has paymentStatus, paymentId, basketId, fraudStatus and
// token; the page does not list the values of paymentStatus, so only SUCCESS and FAILURE (the values of the notification's status and of /payment/detail) are read
// and anything else is "not final" (UNVERIFIED against a checkout form payment in the sandbox).
const retrieveCheckoutFormPath = "/payment/iyzipos/checkoutform/auth/ecom/detail"

// linkKeyPrefix starts the idempotency key of a payment made on a hosted page (payments-worker invoicepayments.LinkKeyPrefix, which this package cannot import).
const linkKeyPrefix = "link:"

// invoiceOfKey is the invoice an idempotency key belongs to: "<invoice>:charge:<attempt>" for a charge Payrudder made, "link:<invoice>:<attempt>" or
// "link:<invoice>:i:<provider>" for a payment on a hosted page. Cutting a link key at its first colon gave "link", which is never the basket of a payment, so every
// payment on a hosted page was refused as unverified.
func invoiceOfKey(key string) (string, bool) {
	invoice, _, ok := strings.Cut(strings.TrimPrefix(key, linkKeyPrefix), ":")
	return invoice, ok && invoice != ""
}

// ParseNotification reads an iyzico webhook for payments.Registry.RegisterParser. iyzico's paymentConversationId is the idempotency
// key Charge sent as its conversationId.
func ParseNotification(body []byte, _ http.Header) (payments.NotificationRef, error) {
	e, err := ParseWebhook(body)
	if err != nil {
		return payments.NotificationRef{}, err
	}
	if e.ConversationID == "" {
		return payments.NotificationRef{}, errors.New("iyzico: webhook without paymentConversationId")
	}
	return payments.NotificationRef{EventID: e.Reference, IdempotencyKey: e.ConversationID}, nil
}

// ConfirmNotification implements payments.Notifier: the signature is checked when there is one, then the notification is confirmed by retrieving the payment (Confirm).
func (c *Client) ConfirmNotification(ctx context.Context, body []byte, header http.Header) (payments.Notification, error) {
	e, err := ParseWebhook(body)
	if err != nil {
		return payments.Notification{}, err
	}
	if err := c.verifySignature(e, header); err != nil {
		return payments.Notification{}, err
	}
	st, err := c.Confirm(ctx, e)
	if err != nil {
		return payments.Notification{}, err
	}
	return payments.Notification{Ref: payments.NotificationRef{EventID: e.Reference, IdempotencyKey: e.ConversationID}, State: st.State,
		ProviderRef: st.ChargeID, DeclineCode: st.DeclineCode}, nil
}

// Register adds iyzico to a registry, with its webhook parser.
func Register(r *payments.Registry) {
	r.Register(Name, Factory)
	r.RegisterParser(Name, ParseNotification)
}
