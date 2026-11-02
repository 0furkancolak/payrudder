package returns

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

var (
	tenant  = uuid.New()
	invoice = uuid.New()
	key     = "link:" + invoice.String() + ":1"
)

// memory is the attempts and the ledger of one hosted payment, in memory.
type memory struct {
	attempt store.Attempt
	known   bool
	rows    []store.LedgerEntry
	pending string
}

func (m *memory) ChargeByKey(context.Context, string) (store.Attempt, bool, error) {
	return m.attempt, m.known, nil
}

func (m *memory) ChargeFact(_ context.Context, _ uuid.UUID, k string) (store.Fact, bool, error) {
	for _, r := range m.rows {
		if r.IdempotencyKey == k && (r.Status == store.StatusSucceeded || r.Status == store.StatusDeclined) {
			return store.Fact{Status: r.Status, ProviderRef: r.ProviderRef, Detail: r.Detail}, true, nil
		}
	}
	return store.Fact{}, false, nil
}

func (m *memory) PendingRef(context.Context, uuid.UUID, string) (string, bool, error) {
	return m.pending, m.pending != "", nil
}

func (m *memory) Append(_ context.Context, e store.LedgerEntry) (bool, error) {
	for _, r := range m.rows {
		if r.Provider == e.Provider && r.Kind == e.Kind && r.ProviderRef == e.ProviderRef && r.Status == e.Status {
			return false, nil
		}
	}
	m.rows = append(m.rows, e)
	return true, nil
}

type provider struct {
	payments.Provider
	req   payments.ReturnRequest
	calls int
	st    payments.StatusResult
	err   error
}

func (p *provider) Name() string                        { return "iyzico" }
func (p *provider) Capabilities() payments.Capabilities { return payments.Capabilities{Name: "iyzico"} }
func (p *provider) Charge(context.Context, payments.ChargeRequest) (payments.ChargeResult, error) {
	return payments.ChargeResult{}, nil
}
func (p *provider) Refund(context.Context, payments.RefundRequest) (payments.RefundResult, error) {
	return payments.RefundResult{}, nil
}
func (p *provider) Status(context.Context, string) (payments.StatusResult, error) {
	return payments.StatusResult{}, nil
}
func (p *provider) ConfirmReturn(_ context.Context, r payments.ReturnRequest) (payments.StatusResult, error) {
	p.calls++
	p.req = r
	return p.st, p.err
}

type resolver struct{ p payments.Provider }

func (r resolver) Named(context.Context, uuid.UUID, string, string) (payments.Provider, error) {
	return r.p, nil
}

type billing struct {
	settled []*billingv1.SettleInvoicePaymentRequest
	answer  bool
	err     error
}

func (b *billing) SettleInvoicePayment(_ context.Context, _ uuid.UUID, req *billingv1.SettleInvoicePaymentRequest) (*billingv1.SettleInvoicePaymentResponse, error) {
	if b.err != nil {
		return nil, b.err
	}
	b.settled = append(b.settled, req)
	return &billingv1.SettleInvoicePaymentResponse{Settled: b.answer}, nil
}

func setup(p *provider) (Deps, *memory, *billing) {
	m := &memory{known: true, pending: "tok-1", attempt: store.Attempt{TenantID: tenant, InvoiceID: invoice, Environment: "live", Provider: "iyzico", AmountMinor: 1116, Currency: "TRY"}}
	b := &billing{answer: true}
	return Deps{Attempts: m, Ledger: m, Resolver: resolver{p}, Billing: b, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, m, b
}

func status(err error) int {
	var r *refuse.Error
	if errors.As(err, &r) {
		return r.Status
	}
	return 0
}

func TestAPaidReturnIsRecordedAndSettlesTheInvoice(t *testing.T) {
	p := &provider{st: payments.StatusResult{State: payments.StateSucceeded, ChargeID: "77"}}
	d, m, b := setup(p)
	form := url.Values{"token": {"tok-1"}}
	out, err := Complete(context.Background(), d, key, form)
	if err != nil || out.State != StatePaid {
		t.Fatalf("= %+v, %v", out, err)
	}
	if p.req.IdempotencyKey != key || p.req.InvoiceID != invoice.String() || p.req.AmountMinor != 1116 || p.req.Currency != "TRY" || p.req.ProviderRef != "tok-1" || p.req.Form.Get("token") != "tok-1" {
		t.Errorf("the provider was asked %+v: the attempt's own records, not the form's", p.req)
	}
	if len(m.rows) != 1 || m.rows[0].Status != store.StatusSucceeded || m.rows[0].ProviderRef != "77" || m.rows[0].IdempotencyKey != key || m.rows[0].Provider != "iyzico" {
		t.Fatalf("ledger = %+v", m.rows)
	}
	if len(b.settled) != 1 || b.settled[0].GetInvoiceId() != invoice.String() || b.settled[0].GetProviderRef() != "77" || b.settled[0].GetAmountMinor() != 1116 || b.settled[0].GetProvider() != "iyzico" {
		t.Errorf("billing was told %v", b.settled)
	}

	// The same form posted again (the payer reloads the page) is answered from the ledger: the provider is not asked, billing is told again and is idempotent.
	out, err = Complete(context.Background(), d, key, form)
	if err != nil || out.State != StatePaid || p.calls != 1 || len(m.rows) != 1 || len(b.settled) != 2 {
		t.Errorf("repeat = %+v, %v, provider calls %d, rows %d, settles %d", out, err, p.calls, len(m.rows), len(b.settled))
	}
}

func TestADeclinedReturnIsRecordedAndSettlesNothing(t *testing.T) {
	p := &provider{st: payments.StatusResult{State: payments.StateDeclined, DeclineCode: "insufficient_funds"}}
	d, m, b := setup(p)
	out, err := Complete(context.Background(), d, key, url.Values{"token": {"tok-1"}})
	if err != nil || out.State != StateDeclined || out.DeclineCode != "insufficient_funds" {
		t.Fatalf("= %+v, %v", out, err)
	}
	if len(m.rows) != 1 || m.rows[0].Status != store.StatusDeclined || m.rows[0].Detail != "insufficient_funds" || len(b.settled) != 0 {
		t.Errorf("ledger %+v, settled %v", m.rows, b.settled)
	}
	// And the page it leaves behind is answered from the ledger when posted again.
	out, err = Complete(context.Background(), d, key, url.Values{"token": {"tok-1"}})
	if err != nil || out.State != StateDeclined || out.DeclineCode != "insufficient_funds" || p.calls != 1 {
		t.Errorf("repeat = %+v, %v, calls %d", out, err, p.calls)
	}
}

func TestAReturnThatIsNotConfirmedWritesNothing(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		state  string
	}{
		"a form that is not the attempt's":      {fmtErr(payments.ErrUnverified), http.StatusUnauthorized, ""},
		"a provider that cannot be asked":       {&payments.TransientError{Err: errors.New("boom")}, http.StatusServiceUnavailable, ""},
		"no answer":                             {payments.ErrTimeout, http.StatusServiceUnavailable, ""},
		"a payment held for a fraud review":     {&payments.PendingError{Ref: "77"}, 0, StatePending},
		"an outcome the provider does not know": {errors.New("not final"), http.StatusServiceUnavailable, ""},
	}
	for name, c := range cases {
		p := &provider{err: c.err}
		d, m, b := setup(p)
		out, err := Complete(context.Background(), d, key, url.Values{"token": {"tok-1"}})
		if status(err) != c.status || out.State != c.state || len(m.rows) != 0 || len(b.settled) != 0 {
			t.Errorf("%s: %+v, %v (status %d), ledger %v, settled %v", name, out, err, status(err), m.rows, b.settled)
		}
	}
}

func fmtErr(err error) error { return errors.Join(err, errors.New("the token is another attempt's")) }

func TestAReturnForNoSuchAttemptIsNotFound(t *testing.T) {
	p := &provider{}
	d, m, _ := setup(p)
	m.known = false
	if _, err := Complete(context.Background(), d, key, nil); status(err) != http.StatusNotFound || p.calls != 0 {
		t.Errorf("unknown key: %v, calls %d", err, p.calls)
	}
	m.known = true
	// Only a hosted payment has a return: a charge of the saga (inv:charge:1) is settled by its own command and notification.
	if _, err := Complete(context.Background(), d, invoice.String()+":charge:1", nil); status(err) != http.StatusNotFound || p.calls != 0 {
		t.Errorf("a saga charge: %v, calls %d", err, p.calls)
	}
}

func TestAnInvoiceThatCannotBeSettledIsRetried(t *testing.T) {
	p := &provider{st: payments.StatusResult{State: payments.StateSucceeded, ChargeID: "77"}}
	d, m, b := setup(p)
	b.err = errors.New("billing-svc is down")
	if _, err := Complete(context.Background(), d, key, url.Values{"token": {"tok-1"}}); err == nil {
		t.Fatal("a payment that could not be settled must not be told to the payer as done")
	}
	// The row is there; the next post is answered from it and settles.
	b.err = nil
	out, err := Complete(context.Background(), d, key, url.Values{"token": {"tok-1"}})
	if err != nil || out.State != StatePaid || len(m.rows) != 1 || len(b.settled) != 1 || p.calls != 1 {
		t.Errorf("retry = %+v, %v, rows %d, settled %d, provider calls %d", out, err, len(m.rows), len(b.settled), p.calls)
	}
}

func TestTheEndpointHandsTheFormToTheCompletion(t *testing.T) {
	p := &provider{st: payments.StatusResult{State: payments.StateSucceeded, ChargeID: "77"}}
	d, _, _ := setup(p)
	mux := http.NewServeMux()
	(&Handler{Deps: d, Log: d.Log}).Register(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/admin/invoice-payments/return", "application/json", strings.NewReader(`{"key":"`+key+`","form":{"token":["tok-1"],"lang":["tr"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"state":"paid"`) || p.req.Form.Get("token") != "tok-1" {
		t.Errorf("= %d %s, form %v", resp.StatusCode, body, p.req.Form)
	}
}
