// Package iyzico is the iyzico adapter of payments.Provider (https://docs.iyzico.com).
//
// What it does for Quotify: charges a stored card (cardUserKey and cardToken, the "pm" token of the customer) through
// POST /payment/auth, refunds a payment through POST /v2/payment/refund, looks a payment up through POST /payment/detail
// and confirms webhook notifications by looking the payment up. iyzico does not schedule renewals or retry failed
// payments for us: Quotify drives both (Capabilities).
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

	"github.com/0furkancolak/quotify/pkg/money"
	"github.com/0furkancolak/quotify/pkg/payments"
)

// Name is the registry name.
const Name = "iyzico"

// Endpoints.
const (
	LiveURL    = "https://api.iyzipay.com"
	SandboxURL = "https://sandbox-api.iyzipay.com"
)

// Buyer is what iyzico wants to know about the payer. iyzico requires these fields on every payment, also on a stored-card one.
// Quotify does not hold them per customer yet, so the account's defaults are sent; real values per customer are a later addition.
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
	// unknown outcome is reported as an error (never as "not found"), so Quotify cannot charge a second time.
	NotFoundCodes []string `json:"not_found_codes,omitempty"`
	// SystemErrorCodes are errorCode values that mean a failure at iyzico worth retrying instead of a decline.
	SystemErrorCodes []string `json:"system_error_codes,omitempty"`
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

// Capabilities implements payments.Provider. iyzico charges and refunds; Quotify schedules renewals, retries failed payments and
// issues the invoice. The currency list is the intersection of iyzico's and Quotify's.
func (c *Client) Capabilities() payments.Capabilities { return Caps() }

// Caps are iyzico's capabilities, which do not depend on an account: routing rules are checked against them before any account exists.
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

// response is the part common to every iyzico answer.
type response struct {
	Status         string `json:"status"`
	ErrorCode      string `json:"errorCode"`
	ErrorMessage   string `json:"errorMessage"`
	ErrorGroup     string `json:"errorGroup"`
	ConversationID string `json:"conversationId"`
	PaymentID      string `json:"paymentId"`
	PaymentStatus  string `json:"paymentStatus"`
	BasketID       string `json:"basketId"`
	Retryable      bool   `json:"retryable"`
	RefundRef      string `json:"refundHostReference"`
	Price          any    `json:"price"`
}

func (r response) ok() bool { return strings.EqualFold(r.Status, "success") }

// state reads the final state of a payment from a /payment/detail answer. Only an explicit SUCCESS is a success, and then the payment has an id (a refund needs
// it); an answer that says nothing about the payment is an error, never a guess.
func (r response) state() (payments.StatusResult, error) {
	switch strings.ToUpper(r.PaymentStatus) {
	case "SUCCESS":
		if r.PaymentID == "" {
			return payments.StatusResult{}, errors.New("iyzico: a successful payment without a paymentId")
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
	s := strconv.FormatInt(minor, 10)
	if cur.Exponent == 0 {
		return s, nil
	}
	if len(s) <= cur.Exponent {
		s = strings.Repeat("0", cur.Exponent-len(s)+1) + s
	}
	return s[:len(s)-cur.Exponent] + "." + s[len(s)-cur.Exponent:], nil
}

// SplitPaymentMethod reads a customer's payment method token "cardUserKey:cardToken".
func SplitPaymentMethod(pm string) (userKey, token string, err error) {
	userKey, token, ok := strings.Cut(pm, ":")
	if !ok || userKey == "" || token == "" {
		return "", "", errors.New(`iyzico: payment method must be "cardUserKey:cardToken"`)
	}
	return userKey, token, nil
}

// classify maps a failed answer to Quotify's error kinds.
func (c *Client) classify(r response) error {
	for _, code := range c.s.SystemErrorCodes {
		if r.ErrorCode == code {
			return &payments.TransientError{Err: fmt.Errorf("iyzico %s: %s", r.ErrorCode, r.ErrorMessage)}
		}
	}
	if r.Retryable {
		return &payments.TransientError{Err: fmt.Errorf("iyzico %s: %s", r.ErrorCode, r.ErrorMessage)}
	}
	code := r.ErrorCode
	if r.ErrorGroup != "" {
		code = r.ErrorGroup // e.g. NOT_SUFFICIENT_FUNDS: more useful to dunning and to a human than the number
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
		if out.ErrorCode == code {
			return payments.StatusResult{State: payments.StateNotFound}, nil
		}
	}
	return payments.StatusResult{}, fmt.Errorf("iyzico: status lookup failed: %s %s", out.ErrorCode, out.ErrorMessage)
}

// WebhookEvent is an iyzico payment notification.
type WebhookEvent struct {
	Type           string `json:"iyziEventType"`
	Time           int64  `json:"iyziEventTime"`
	Reference      string `json:"iyziReferenceCode"`
	PaymentID      any    `json:"paymentId"`
	ConversationID string `json:"paymentConversationId"`
	Status         string `json:"status"`
}

// ID returns the paymentId as a string (iyzico sends a number).
func (e WebhookEvent) ID() string {
	switch v := e.PaymentID.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	}
	return ""
}

// ParseWebhook decodes a notification body.
func ParseWebhook(body []byte) (WebhookEvent, error) {
	var e WebhookEvent
	if err := json.Unmarshal(body, &e); err != nil {
		return e, fmt.Errorf("iyzico: webhook body: %w", err)
	}
	if e.Reference == "" || e.ID() == "" {
		return e, errors.New("iyzico: webhook without iyziReferenceCode or paymentId")
	}
	return e, nil
}

// Confirm asks iyzico what the notified payment really is. The webhook signature scheme of API payments is not settled by the
// documentation, which tells merchants without notification validation to retrieve the payment instead, so Quotify never acts on
// the body of a notification alone.
//
// The body is unsigned, so its conversation id is only a claim: the payment it names must be the charge of that conversation. Charge sends the invoice id as
// the basketId and the idempotency key ("<invoice id>:charge:<attempt>") as the conversationId, so the payment's basketId must be the invoice of the key.
// Otherwise a real payment of one invoice could be notified as the payment of another (UNVERIFIED: that /payment/detail answers with the basketId; one that
// does not is refused rather than believed).
func (c *Client) Confirm(ctx context.Context, e WebhookEvent) (payments.StatusResult, error) {
	invoice, _, ok := strings.Cut(e.ConversationID, ":")
	if !ok || invoice == "" {
		return payments.StatusResult{}, fmt.Errorf("%w: the conversation id %q is not one of Quotify's charges", payments.ErrUnverified, e.ConversationID)
	}
	out, err := c.do(ctx, "/payment/detail", map[string]any{"locale": "en", "conversationId": e.ConversationID, "paymentId": e.ID()})
	if err != nil {
		return payments.StatusResult{}, err
	}
	if !out.ok() {
		return payments.StatusResult{}, fmt.Errorf("iyzico: cannot confirm payment %s: %s %s", e.ID(), out.ErrorCode, out.ErrorMessage)
	}
	if out.BasketID != invoice {
		return payments.StatusResult{}, fmt.Errorf("%w: payment %s is not a payment of conversation %q", payments.ErrUnverified, e.ID(), e.ConversationID)
	}
	return out.state()
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

// ConfirmNotification implements payments.Notifier: the notification is confirmed by retrieving the payment (Confirm).
func (c *Client) ConfirmNotification(ctx context.Context, body []byte, _ http.Header) (payments.Notification, error) {
	e, err := ParseWebhook(body)
	if err != nil {
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
