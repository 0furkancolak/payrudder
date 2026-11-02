// Package mailadmin is the gateway's way to the notifier's signed HTTP routes (NOTIFIER_EVENTS_ADDR): the tenant's suppression list, its message log and the preview of
// a notice. The notifier has no gRPC server; it answers requests signed with the shared secret (pkg/webhooksig, v2: the signature covers the method, path and query too) on the listener its operator switched on. The gateway
// always names the tenant itself, from the credential, so nothing a client sends reaches another tenant's list.
package mailadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/webhooksig"
)

// ErrNotFound is the answer for an address that is not on the list.
var ErrNotFound = errors.New("mailadmin: not found")

// BadRequestError is a request the notifier refused, with its reason.
type BadRequestError struct{ Message string }

func (e BadRequestError) Error() string { return e.Message }

// Suppression is an address the tenant's mail is not sent to, as the API shows it (the provider's message id and the operator's name stay in the notifier).
type Suppression struct {
	Address    string    `json:"address"`
	Reason     string    `json:"reason"`
	Detail     string    `json:"detail"`
	CreateTime time.Time `json:"create_time"`
}

// Message is a line of the tenant's message log.
type Message struct {
	Key       string     `json:"key"`
	Kind      string     `json:"kind"`
	Template  string     `json:"template"`
	Recipient string     `json:"recipient"`
	State     string     `json:"state"`
	ClaimTime time.Time  `json:"claim_time"`
	SendTime  *time.Time `json:"send_time"`
}

// PriceChangePreview is what the preview of the price-change notice is made of.
type PriceChangePreview struct {
	Locale          string    `json:"locale"`
	CustomerName    string    `json:"customer_name"`
	TenantName      string    `json:"tenant_name"`
	PlanCode        string    `json:"plan_code"`
	EffectiveTime   time.Time `json:"effective_time"`
	NewRenewalMinor int64     `json:"new_renewal_minor"`
	Currency        string    `json:"currency"`
	Timezone        string    `json:"timezone"`
}

// Rendered is a mail as it would be sent.
type Rendered struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
}

// Client talks to the notifier.
type Client struct {
	base   *url.URL
	secret string
	http   *http.Client
	now    func() time.Time
}

// New returns a client for the notifier at rawURL ("http://notifier:9111"), signing with secret.
func New(rawURL, secret string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("mailadmin: %q is not an http(s) address", rawURL)
	}
	if len(secret) < 16 {
		return nil, errors.New("mailadmin: the shared secret is at least 16 characters")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{base: u, secret: secret, http: hc, now: time.Now}, nil
}

// do sends one signed request and decodes a 200 answer into out (nil: ignored).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	u := *c.base
	u.Path, u.RawQuery = path, query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set(webhooksig.Header(), webhooksig.SignRequest(c.now(), method, req.URL, body, c.secret))
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		if out == nil {
			return nil
		}
		return json.Unmarshal(raw, out)
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusBadRequest:
		var p struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &p)
		return BadRequestError{Message: p.Error}
	}
	return fmt.Errorf("mailadmin: the notifier answered %d", resp.StatusCode)
}

func (c *Client) paged(size int, after string) url.Values {
	q := url.Values{}
	if size > 0 {
		q.Set("page_size", strconv.Itoa(size))
	}
	if after != "" {
		q.Set("after", after)
	}
	return q
}

// Suppressions lists the tenant's suppressed addresses after the given one, in address order, and the address to continue after ("" on the last page).
func (c *Client) Suppressions(ctx context.Context, tenantID uuid.UUID, after string, size int) ([]Suppression, string, error) {
	q := c.paged(size, after)
	q.Set("tenant_id", tenantID.String())
	var out struct {
		Suppressions []struct {
			Address   string    `json:"address"`
			Reason    string    `json:"reason"`
			Detail    string    `json:"detail"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"suppressions"`
		NextAfter string `json:"next_after"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/mail-suppressions", q, nil, &out); err != nil {
		return nil, "", err
	}
	rows := make([]Suppression, len(out.Suppressions))
	for i, s := range out.Suppressions {
		rows[i] = Suppression{Address: s.Address, Reason: s.Reason, Detail: s.Detail, CreateTime: s.CreatedAt}
	}
	return rows, out.NextAfter, nil
}

// Unsuppress lifts the suppression of an address; ErrNotFound when it is not on the list.
func (c *Client) Unsuppress(ctx context.Context, tenantID uuid.UUID, address string) error {
	return c.do(ctx, http.MethodDelete, "/v1/mail-suppressions/"+tenantID.String()+"/"+address, nil, nil, nil)
}

// Messages lists the tenant's recent mail, newest first, and the token to continue after ("" on the last page).
func (c *Client) Messages(ctx context.Context, tenantID uuid.UUID, after string, size int) ([]Message, string, error) {
	q := c.paged(size, after)
	q.Set("tenant_id", tenantID.String())
	var out struct {
		Messages  []Message `json:"messages"`
		NextAfter string    `json:"next_after"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/mail-messages", q, nil, &out); err != nil {
		return nil, "", err
	}
	if out.Messages == nil {
		out.Messages = []Message{}
	}
	return out.Messages, out.NextAfter, nil
}

// PreviewPriceChange renders the price-change notice the way the notifier would send it.
func (c *Client) PreviewPriceChange(ctx context.Context, in PriceChangePreview) (Rendered, error) {
	var out Rendered
	err := c.do(ctx, http.MethodPost, "/v1/mail-previews/price-change", nil, in, &out)
	return out, err
}
