package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/0furkancolak/payrudder/pkg/env"
)

// The gateway composes two things from payments-worker's internal HTTP API for the hosted pages and the operator's customer payments: the ledger rows
// of some invoices, and the payment methods / the start of a payment for an invoice. It always sends the shared internal token and never a visitor's token.

var errPaymentsDown = errors.New("payments-worker is not reachable")

var paymentsHTTP = &http.Client{Timeout: 8 * time.Second}

// paymentsDo calls payments-worker. It returns the status code and decodes a JSON body into out when out is not nil and the status is 2xx.
func (s *Server) paymentsDo(ctx context.Context, method, path string, query url.Values, in, out any) (int, error) {
	if s.d.PaymentsURL == nil {
		return 0, errPaymentsDown
	}
	u := *s.d.PaymentsURL
	u.Path, u.RawPath, u.RawQuery = path, "", query.Encode()
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", requestID(ctx))
	if s.d.PaymentsToken != "" {
		req.Header.Set("X-Internal-Token", s.d.PaymentsToken)
	}
	resp, err := paymentsHTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", errPaymentsDown, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode/100 == 2 && out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("payments-worker answered garbage: %w", err)
		}
	}
	return resp.StatusCode, nil
}

const maxLedgerLookup = 200

// ledgerLookup returns the charge and refund rows of the invoices, oldest first. Without a payments-worker it returns none and no error: the
// pages then show what billing alone knows.
func (s *Server) ledgerLookup(ctx context.Context, tenantID string, invoiceIDs []string) ([]ledgerRow, error) {
	if s.d.PaymentsURL == nil || len(invoiceIDs) == 0 {
		return nil, nil
	}
	if len(invoiceIDs) > maxLedgerLookup {
		invoiceIDs = invoiceIDs[:maxLedgerLookup]
	}
	var out struct {
		Rows []ledgerRow `json:"rows"`
	}
	code, err := s.paymentsDo(ctx, http.MethodPost, "/admin/tenants/"+url.PathEscape(tenantID)+"/ledger/lookup", nil, map[string]any{"invoice_ids": invoiceIDs}, &out)
	if err != nil {
		return nil, err
	}
	if code/100 != 2 {
		return nil, fmt.Errorf("%w: ledger lookup answered %d", errPaymentsDown, code)
	}
	rows := make([]ledgerRow, 0, len(out.Rows))
	for _, r := range out.Rows {
		if r.Kind == "charge" || r.Kind == "refund" {
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].id() < rows[j].id() })
	return rows, nil
}

// pendingWindow is how long a pending charge row keeps an invoice "processing". It matches payments-worker's checkoutReuse: the time a handed-out
// checkout is still reused, after which a new one is opened anyway.
const pendingWindow = 30 * time.Minute

// refineState turns billing's payment state into what the ledger adds for an invoice that is not settled: a pending row of the invoice means a payment
// is on its way, a declined or failed latest row means the last attempt did not go through.
//
// A pending row only counts for pendingWindow. Most providers say nothing when a customer closes the checkout, and nothing ever closes the row of
// bank instructions that were only looked at; without a limit the page would say "processing" for ever and never offer the pay buttons again.
func refineState(state string, rows []ledgerRow, invoiceID string, now time.Time) string {
	if state != "open" && state != "failed" {
		return state
	}
	var latest *ledgerRow
	for i := range rows {
		if rows[i].InvoiceID == invoiceID && rows[i].Kind == "charge" && (rows[i].Status != "pending" || now.Sub(rows[i].OccurredAt) <= pendingWindow) {
			latest = &rows[i]
		}
	}
	if latest == nil {
		return state
	}
	switch {
	case latest.Status == "pending":
		return "processing"
	case failedStatus(latest.Status):
		return "failed"
	}
	return state
}

// paymentMethod is one way to pay an invoice, as payments-worker offers it.
type paymentMethod struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
}

func (s *Server) liveMethods(ctx context.Context, tenantID, currency, country, provider string) ([]paymentMethod, error) {
	q := url.Values{"environment": {env.Live.String()}, "currency": {currency}}
	if country != "" {
		q.Set("country", country)
	}
	if provider != "" {
		q.Set("provider", provider)
	}
	var out struct {
		Methods []paymentMethod `json:"methods"`
	}
	code, err := s.paymentsDo(ctx, http.MethodGet, "/admin/tenants/"+url.PathEscape(tenantID)+"/invoice-methods", q, nil, &out)
	if err != nil {
		return nil, err
	}
	if code/100 != 2 {
		return nil, fmt.Errorf("%w: invoice-methods answered %d", errPaymentsDown, code)
	}
	return out.Methods, nil
}

// paymentStart is payments-worker's answer to "start paying this invoice by this method".
type paymentStart struct {
	Kind      string `json:"kind"` // redirect or instructions
	URL       string `json:"url"`
	Reference string `json:"reference"`
	Lines     []struct {
		Label string `json:"label"`
		Value string `json:"value"`
	} `json:"lines"`
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// recentLedger reads the newest ledgerWindowRows rows of one tenant's ledger from payments-worker (newest first, every kind). The tenant id is always the
// caller's own: the gateway takes it from the authenticated principal or from plans-svc's membership-checked answer, never from a request.
func (s *Server) recentLedger(ctx context.Context, tenantID string) ([]ledgerEntry, error) {
	var out struct {
		Entries []ledgerEntry `json:"entries"`
	}
	code, err := s.paymentsDo(ctx, http.MethodGet, "/admin/tenants/"+url.PathEscape(tenantID)+"/ledger", url.Values{"limit": {itoa(ledgerWindowRows)}}, nil, &out)
	if err != nil {
		return nil, err
	}
	if code/100 != 2 {
		return nil, fmt.Errorf("%w: ledger answered %d", errPaymentsDown, code)
	}
	return out.Entries, nil
}
