package mailx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/0furkancolak/payrudder/pkg/strx"
)

// PermanentError means the provider refused the message and will refuse it again (a wrong key, an address it does not accept, a sender domain that is not
// verified): the caller must not retry it as it is.
type PermanentError struct {
	Status int
	Detail string
}

func (e *PermanentError) Error() string {
	return fmt.Sprintf("mail: refused by the provider (%d): %s", e.Status, e.Detail)
}

// Resend sends through a Resend-compatible HTTP API: POST {BaseURL}/emails with the key as a Bearer token. Nafru Email is one (its official SDK is Resend's with
// another base URL). The sender is built per tenant from the tenant's own account, so BaseURL is the tenant's choice and Client must be one that refuses private
// addresses (safehttp.NewClient).
type Resend struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

type resendBody struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html,omitempty"`
	Text    string   `json:"text,omitempty"`
}

func (s Resend) endpoint(path string) (string, error) {
	base, err := url.Parse(strings.TrimRight(s.BaseURL, "/"))
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") {
		return "", errors.New("mail: the provider's base URL is not a URL")
	}
	return base.String() + path, nil
}

// Send implements Sender. The dedup key of the message is the Idempotency-Key, so a retry after a lost answer does not send a second mail. A refusal that
// retrying cannot change is a *PermanentError; a failure before the request left (the name did not resolve, the connection was refused) is a *NotSentError.
func (s Resend) Send(ctx context.Context, m Message) (string, error) {
	if strings.ContainsAny(m.To+m.From+m.Subject, "\r\n") {
		return "", fmt.Errorf("mail: header injection attempt in %q", m.To)
	}
	endpoint, err := s.endpoint("/emails")
	if err != nil {
		return "", &NotSentError{err}
	}
	payload, err := json.Marshal(resendBody{From: m.From, To: []string{m.To}, Subject: m.Subject, HTML: m.HTML, Text: m.Text})
	if err != nil {
		return "", &NotSentError{err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", &NotSentError{err}
	}
	req.Header.Set("Authorization", "Bearer "+s.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "payrudder-notifier")
	if m.ID != "" {
		req.Header.Set("Idempotency-Key", m.ID)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		// Whether the request reached the provider is unknown for a timeout after the connect; only a dial failure is certainly "not sent".
		var op interface{ Timeout() bool }
		if errors.As(err, &op) && op.Timeout() {
			return "", fmt.Errorf("mail: provider did not answer: %w", err)
		}
		return "", &NotSentError{fmt.Errorf("provider unreachable: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode/100 == 2:
		var out struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &out)
		if out.ID == "" {
			return "", fmt.Errorf("mail: provider accepted the message without an id")
		}
		return out.ID, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return "", fmt.Errorf("mail: provider answered %d", resp.StatusCode)
	default:
		return "", &PermanentError{Status: resp.StatusCode, Detail: detail(body)}
	}
}

// Check asks the provider whether the key works, without sending anything: GET /domains answers 200 for a valid key. It returns a *PermanentError for a key
// the provider refuses and another error when it could not be reached.
func (s Resend) Check(ctx context.Context) error {
	endpoint, err := s.endpoint("/domains")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.APIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "payrudder-notifier")
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("provider unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 == 2 {
		return nil
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return fmt.Errorf("provider answered %d", resp.StatusCode)
	}
	return &PermanentError{Status: resp.StatusCode, Detail: detail(body)}
}

func (s Resend) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

// detail is the provider's own explanation, short and on one line, for the log; it never holds the key (the provider does not echo it).
func detail(body []byte) string {
	var e struct {
		Message string `json:"message"`
		Error   any    `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil {
		if e.Message != "" {
			return truncate(e.Message)
		}
		if s, ok := e.Error.(string); ok {
			return truncate(s)
		}
	}
	return truncate(strings.Join(strings.Fields(string(body)), " "))
}

// maxDetail is the most of a provider's answer an error carries.
const maxDetail = 200

func truncate(s string) string { return strx.Truncate(s, maxDetail) }
