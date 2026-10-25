// Package payments defines the PaymentProvider that payments-worker talks to, and the client for mock-psp. A real PSP
// adapter is a non-goal; this interface is where one would plug in (docs/03-services/workers.md#payments-worker).
package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// ChargeRequest asks for a payment. IdempotencyKey is mandatory: the provider returns the original result for a key it
// has seen, so a retried or re-sent charge never charges twice.
type ChargeRequest struct {
	IdempotencyKey string `json:"-"`
	InvoiceID      string `json:"invoice_id"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	PaymentMethod  string `json:"payment_method"`
	// InvoiceNumber is the number printed on the invoice, for a provider that asks the customer to quote it (a bank transfer). Not sent to mock-psp.
	InvoiceNumber string `json:"-"`
}

// ChargeResult is a successful charge.
type ChargeResult struct {
	ChargeID string `json:"id"`
}

// RefundRequest asks for a refund of a charge.
type RefundRequest struct {
	IdempotencyKey string `json:"-"`
	InvoiceID      string `json:"invoice_id"`
	ChargeID       string `json:"charge_id,omitempty"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
}

// RefundResult is a successful refund.
type RefundResult struct {
	RefundID string `json:"id"`
}

// State of a charge at the provider.
type State string

const (
	StateNotFound  State = "not_found"
	StateSucceeded State = "succeeded"
	StateDeclined  State = "declined"
)

// StatusResult answers "did my charge with this idempotency key go through?".
type StatusResult struct {
	State       State
	ChargeID    string
	DeclineCode string
}

// PaymentProvider is a payment service provider.
type PaymentProvider interface {
	Name() string
	// Charge returns *DeclinedError when the payment was refused, *TransientError for a failure worth retrying, and
	// ErrTimeout when the outcome is unknown.
	Charge(ctx context.Context, r ChargeRequest) (ChargeResult, error)
	Refund(ctx context.Context, r RefundRequest) (RefundResult, error)
	Status(ctx context.Context, idempotencyKey string) (StatusResult, error)
}

// DeclinedError is a permanent refusal (HTTP 402).
type DeclinedError struct{ Code string }

func (e *DeclinedError) Error() string { return "payment declined: " + e.Code }

// TransientError is a failure that may succeed when retried (5xx, connection reset).
type TransientError struct{ Err error }

func (e *TransientError) Error() string { return "transient payment provider error: " + e.Err.Error() }
func (e *TransientError) Unwrap() error { return e.Err }

// ErrTimeout means the provider did not answer in time: the charge may or may not have happened. Callers must not
// retry blindly; they ask Status.
var ErrTimeout = errors.New("payments: provider timed out; the outcome is unknown")

// MockPSP is a PaymentProvider over mock-psp's HTTP API.
type MockPSP struct {
	BaseURL string
	Client  *http.Client
}

// NewMockPSP returns a client with the given per-request timeout (default 5 s).
func NewMockPSP(baseURL string, timeout time.Duration) *MockPSP {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &MockPSP{BaseURL: baseURL, Client: &http.Client{Timeout: timeout}}
}

// Name implements PaymentProvider.
func (m *MockPSP) Name() string { return "mockpsp" }

// do sends a request. A 404 is an answer only when the caller says so (allowNotFound, for Status: "no charge under this key"); for a charge or a refund it is
// a wrong URL or path, never a payment that went through.
func (m *MockPSP) do(ctx context.Context, method, path, key string, in, out any, allowNotFound bool) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.BaseURL+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := m.Client.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) {
			return 0, ErrTimeout
		}
		return 0, &TransientError{Err: err}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusPaymentRequired {
		var e struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return resp.StatusCode, &DeclinedError{Code: e.Error.Code}
	}
	if resp.StatusCode >= 500 {
		return resp.StatusCode, &TransientError{Err: fmt.Errorf("psp answered %d", resp.StatusCode)}
	}
	if resp.StatusCode >= 400 && (!allowNotFound || resp.StatusCode != http.StatusNotFound) {
		return resp.StatusCode, fmt.Errorf("psp rejected the request: %d %s", resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("psp answered garbage: %w", err)
		}
	}
	return resp.StatusCode, nil
}

// Charge implements PaymentProvider.
func (m *MockPSP) Charge(ctx context.Context, r ChargeRequest) (ChargeResult, error) {
	if r.IdempotencyKey == "" {
		return ChargeResult{}, errors.New("payments: idempotency key is mandatory")
	}
	var out ChargeResult
	if _, err := m.do(ctx, http.MethodPost, "/v1/charges", r.IdempotencyKey, r, &out, false); err != nil {
		return ChargeResult{}, err
	}
	return out, nil
}

// Refund implements PaymentProvider.
func (m *MockPSP) Refund(ctx context.Context, r RefundRequest) (RefundResult, error) {
	var out RefundResult
	if _, err := m.do(ctx, http.MethodPost, "/v1/refunds", r.IdempotencyKey, r, &out, false); err != nil {
		return RefundResult{}, err
	}
	return out, nil
}

// Status implements PaymentProvider.
func (m *MockPSP) Status(ctx context.Context, key string) (StatusResult, error) {
	var out struct {
		Found       bool   `json:"found"`
		Status      string `json:"status"`
		ID          string `json:"id"`
		DeclineCode string `json:"decline_code"`
	}
	code, err := m.do(ctx, http.MethodGet, "/v1/charges?idempotency_key="+url.QueryEscape(key), "", nil, &out, true)
	if err != nil {
		return StatusResult{}, err
	}
	if code == http.StatusNotFound || !out.Found {
		return StatusResult{State: StateNotFound}, nil
	}
	switch out.Status {
	case "succeeded":
		return StatusResult{State: StateSucceeded, ChargeID: out.ID}, nil
	case "declined":
		return StatusResult{State: StateDeclined, DeclineCode: out.DeclineCode}, nil
	}
	return StatusResult{}, fmt.Errorf("psp reported unknown status %q", out.Status)
}
