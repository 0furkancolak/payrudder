package paddle

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0furkancolak/quotify/pkg/payments"
)

type call struct {
	method, path, query string
	body                map[string]any
}

// router answers by "METHOD path" and records every request.
type router struct {
	calls  []call
	routes map[string]string
}

func newRouter(t *testing.T, routes map[string]string) (*router, *Client) {
	t.Helper()
	rt := &router{routes: routes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c := call{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery}
		_ = json.Unmarshal(raw, &c.body)
		rt.calls = append(rt.calls, c)
		reply, ok := rt.routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"error":{"type":"request_error","code":"not_found","detail":"no route in the test"}}`)
			return
		}
		if strings.HasPrefix(reply, "!") {
			w.WriteHeader(422)
			_, _ = io.WriteString(w, reply[1:])
			return
		}
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Settings{APIKey: "pdl_key", BaseURL: srv.URL, WebhookSecret: "whsec", UsageProductID: "pro_usage"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return rt, c
}

func (r *router) find(method, path string) []call {
	var out []call
	for _, c := range r.calls {
		if c.method == method && c.path == path {
			out = append(out, c)
		}
	}
	return out
}

func TestSyncCatalogueCreatesTheProductAndEachPrice(t *testing.T) {
	rt, c := newRouter(t, map[string]string{
		"POST /products": `{"data":{"id":"pro_01"}}`,
		"POST /prices":   `{"data":{"id":"pri_01"}}`,
	})
	res, err := c.SyncCatalogue(context.Background(), payments.CatalogueRequest{
		ProductName: "Pro", QuotifyPlanCode: "pro",
		Prices: []payments.PriceSpec{
			{Key: "pv|month|USD", Currency: "USD", AmountMinor: 1900, Interval: "month", Name: "Pro (monthly)"},
			{Key: "pv|year|EUR", Currency: "EUR", AmountMinor: 19000, Interval: "year"},
			{Key: "pv|month|TRY", Currency: "TRY", AmountMinor: 59900, Interval: "month"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ProductRef != "pro_01" || len(res.Prices) != 2 || res.Prices["pv|month|USD"] != "pri_01" || res.Skipped["pv|month|TRY"] == "" {
		t.Fatalf("result = %+v", res)
	}
	p := rt.find("POST", "/products")[0].body
	if p["name"] != "Pro" || p["tax_category"] != "saas" || p["custom_data"].(map[string]any)["quotify_plan_code"] != "pro" {
		t.Errorf("product = %v", p)
	}
	prices := rt.find("POST", "/prices")
	if len(prices) != 2 {
		t.Fatalf("%d prices created; want 2 (TRY is skipped)", len(prices))
	}
	b := prices[1].body
	bc := b["billing_cycle"].(map[string]any)
	up := b["unit_price"].(map[string]any)
	if b["product_id"] != "pro_01" || bc["interval"] != "year" || bc["frequency"].(float64) != 1 || up["amount"] != "19000" || up["currency_code"] != "EUR" ||
		b["custom_data"].(map[string]any)["quotify_key"] != "pv|year|EUR" || b["quantity"].(map[string]any)["maximum"].(float64) != 1 {
		t.Errorf("price = %v", b)
	}
}

func TestSyncCatalogueReusesAProductAndReportsRefusals(t *testing.T) {
	rt, c := newRouter(t, map[string]string{"POST /prices": `!{"error":{"type":"request_error","code":"invalid_field","detail":"nope"}}`})
	_, err := c.SyncCatalogue(context.Background(), payments.CatalogueRequest{ProductRef: "pro_9", Prices: []payments.PriceSpec{{Key: "k", Currency: "USD", AmountMinor: 1, Interval: "month"}}})
	var pe *Error
	if !errors.As(err, &pe) || pe.Status != 422 {
		t.Fatalf("err = %v; a refused price is a permanent error", err)
	}
	if len(rt.find("POST", "/products")) != 0 {
		t.Error("a product that exists was created again")
	}
}

func TestEnsureCustomerFindsOrCreates(t *testing.T) {
	t.Run("new customer with address and business", func(t *testing.T) {
		rt, c := newRouter(t, map[string]string{
			"GET /customers":                    `{"data":[]}`,
			"POST /customers":                   `{"data":{"id":"ctm_01"}}`,
			"GET /customers/ctm_01/addresses":   `{"data":[]}`,
			"POST /customers/ctm_01/addresses":  `{"data":{"id":"add_01"}}`,
			"GET /customers/ctm_01/businesses":  `{"data":[]}`,
			"POST /customers/ctm_01/businesses": `{"data":{"id":"biz_01"}}`,
		})
		refs, err := c.EnsureCustomer(context.Background(), payments.CustomerSpec{QuotifyCustomerID: "cus_1", Email: "a+b@example.com", Name: "Ada", Country: "DE", PostalCode: "10115", TaxID: "DE123", Business: "Ada GmbH"})
		if err != nil {
			t.Fatal(err)
		}
		if refs.CustomerRef != "ctm_01" || refs.AddressRef != "add_01" || refs.BusinessRef != "biz_01" {
			t.Fatalf("refs = %+v", refs)
		}
		if q := rt.find("GET", "/customers")[0].query; q != "email=a%2Bb%40example.com" {
			t.Errorf("search query = %q", q)
		}
		cb := rt.find("POST", "/customers")[0].body
		if cb["email"] != "a+b@example.com" || cb["custom_data"].(map[string]any)["quotify_customer_id"] != "cus_1" {
			t.Errorf("customer = %v", cb)
		}
		if ab := rt.find("POST", "/customers/ctm_01/addresses")[0].body; ab["country_code"] != "DE" || ab["postal_code"] != "10115" {
			t.Errorf("address = %v", ab)
		}
		if bb := rt.find("POST", "/customers/ctm_01/businesses")[0].body; bb["name"] != "Ada GmbH" || bb["tax_identifier"] != "DE123" {
			t.Errorf("business = %v", bb)
		}
	})
	t.Run("a customer that exists is reused, with its address and business", func(t *testing.T) {
		rt, c := newRouter(t, map[string]string{
			"GET /customers":                   `{"data":[{"id":"ctm_07"}]}`,
			"GET /customers/ctm_07/addresses":  `{"data":[{"id":"add_07","country_code":"DE"}]}`,
			"GET /customers/ctm_07/businesses": `{"data":[{"id":"biz_07","tax_identifier":"DE123"}]}`,
		})
		refs, err := c.EnsureCustomer(context.Background(), payments.CustomerSpec{Email: "a@example.com", Country: "DE", TaxID: "DE123"})
		if err != nil || refs.CustomerRef != "ctm_07" || refs.AddressRef != "add_07" || refs.BusinessRef != "biz_07" {
			t.Fatalf("refs = %+v, %v", refs, err)
		}
		for _, cl := range rt.calls {
			if cl.method == "POST" {
				t.Errorf("created %s although everything existed", cl.path)
			}
		}
	})
	t.Run("an e-mail address is needed", func(t *testing.T) {
		_, c := newRouter(t, nil)
		if _, err := c.EnsureCustomer(context.Background(), payments.CustomerSpec{}); err == nil {
			t.Error("a customer without an e-mail address was accepted")
		}
	})
}

func TestChangeMapsToPaddlesProrationModes(t *testing.T) {
	for _, tc := range []struct{ timing, proration, want string }{
		{"immediately", "", "prorated_immediately"},
		{"immediately", "prorate", "prorated_immediately"},
		{"immediately", "full", "full_immediately"},
		{"immediately", "none", "full_next_billing_period"},
	} {
		rt, c := newRouter(t, map[string]string{"PATCH /subscriptions/sub_01": `{"data":{}}`})
		err := c.ApplyChange(context.Background(), "sub_01", payments.ProviderChange{Items: []payments.ChangeItem{{PriceRef: "pri_9"}}, Timing: tc.timing, Proration: tc.proration})
		if err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		b := rt.find("PATCH", "/subscriptions/sub_01")[0].body
		item := b["items"].([]any)[0].(map[string]any)
		if b["proration_billing_mode"] != tc.want || b["on_payment_failure"] != "prevent_change" || item["price_id"] != "pri_9" || item["quantity"].(float64) != 1 {
			t.Errorf("%+v: body = %v", tc, b)
		}
	}
	t.Run("a change for the end of the period is refused, not approximated", func(t *testing.T) {
		rt, c := newRouter(t, nil)
		err := c.ApplyChange(context.Background(), "sub_01", payments.ProviderChange{Items: []payments.ChangeItem{{PriceRef: "pri_9"}}, Timing: "period_end"})
		if !errors.Is(err, payments.ErrUnsupported) || len(rt.calls) != 0 {
			t.Fatalf("err = %v after %d call(s)", err, len(rt.calls))
		}
	})
	t.Run("bad input never reaches Paddle", func(t *testing.T) {
		rt, c := newRouter(t, nil)
		ctx := context.Background()
		for name, err := range map[string]error{
			"no items":       c.ApplyChange(ctx, "sub_01", payments.ProviderChange{Timing: "immediately"}),
			"not a price":    c.ApplyChange(ctx, "sub_01", payments.ProviderChange{Items: []payments.ChangeItem{{PriceRef: "x"}}, Timing: "immediately"}),
			"not a sub":      c.ApplyChange(ctx, "x", payments.ProviderChange{Items: []payments.ChangeItem{{PriceRef: "pri_1"}}, Timing: "immediately"}),
			"unknown prorat": c.ApplyChange(ctx, "sub_01", payments.ProviderChange{Items: []payments.ChangeItem{{PriceRef: "pri_1"}}, Timing: "immediately", Proration: "half"}),
		} {
			if err == nil {
				t.Errorf("%s: accepted", name)
			}
		}
		if len(rt.calls) != 0 {
			t.Errorf("%d request(s) reached Paddle", len(rt.calls))
		}
	})
}

func TestPreviewChangeReadsPaddlesSummary(t *testing.T) {
	rt, c := newRouter(t, map[string]string{"PATCH /subscriptions/sub_01/preview": `{"data":{"currency_code":"USD","next_billed_at":"2026-11-01T00:00:00Z",
		"update_summary":{"credit":{"amount":"500","currency_code":"USD"},"charge":{"amount":"1200","currency_code":"USD"},"result":{"action":"charge","amount":"700"}},
		"recurring_transaction_details":{"totals":{"total":"2900"}}}}`})
	p, err := c.PreviewChange(context.Background(), "sub_01", payments.ProviderChange{Items: []payments.ChangeItem{{PriceRef: "pri_9", Quantity: 1}}, Timing: "immediately"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Currency != "USD" || p.CreditMinor != 500 || p.ChargeMinor != 1200 || p.ResultMinor != 700 || p.NextAmountMinor != 2900 || p.NextBilledAt == nil || p.NextBilledAt.Year() != 2026 {
		t.Fatalf("preview = %+v", p)
	}
	if len(rt.find("PATCH", "/subscriptions/sub_01")) != 0 {
		t.Error("a preview changed the subscription")
	}
	rt2, c2 := newRouter(t, map[string]string{"PATCH /subscriptions/sub_01/preview": `{"data":{"update_summary":{"credit":{"amount":"900","currency_code":"EUR"},"charge":{"amount":"100","currency_code":"EUR"},"result":{"action":"credit","amount":"800"}}}}`})
	p, err = c2.PreviewChange(context.Background(), "sub_01", payments.ProviderChange{Items: []payments.ChangeItem{{PriceRef: "pri_9"}}, Timing: "immediately"})
	if err != nil || p.ResultMinor != -800 || p.Currency != "EUR" {
		t.Fatalf("credit preview = %+v, %v", p, err)
	}
	_ = rt2
}

func TestPauseResumeAndLinks(t *testing.T) {
	rt, c := newRouter(t, map[string]string{
		"POST /subscriptions/sub_01/pause":                            `{"data":{}}`,
		"POST /subscriptions/sub_01/resume":                           `{"data":{}}`,
		"GET /subscriptions/sub_01/update-payment-method-transaction": `{"data":{"checkout":{"url":"https://pay.example/upd"}}}`,
		"POST /customers/ctm_01/portal-sessions":                      `{"data":{"urls":{"general":{"overview":"https://portal.example/x"}}}}`,
		"GET /transactions/txn_01/invoice":                            `{"data":{"url":"https://files.example/inv.pdf"}}`,
	})
	ctx := context.Background()
	resume := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if err := c.Pause(ctx, "sub_01", true, &resume); err != nil {
		t.Fatal(err)
	}
	if b := rt.find("POST", "/subscriptions/sub_01/pause")[0].body; b["effective_from"] != "next_billing_period" || b["resume_at"] != "2026-12-01T00:00:00Z" {
		t.Errorf("pause = %v", b)
	}
	if err := c.Pause(ctx, "sub_01", false, nil); err != nil {
		t.Fatal(err)
	}
	if b := rt.find("POST", "/subscriptions/sub_01/pause")[1].body; b["effective_from"] != "immediately" || b["resume_at"] != nil {
		t.Errorf("immediate pause = %v", b)
	}
	if err := c.Resume(ctx, "sub_01"); err != nil {
		t.Fatal(err)
	}
	if b := rt.find("POST", "/subscriptions/sub_01/resume")[0].body; b["effective_from"] != "immediately" {
		t.Errorf("resume = %v", b)
	}
	if u, err := c.PaymentMethodLink(ctx, "sub_01"); err != nil || u != "https://pay.example/upd" {
		t.Errorf("payment method link = %q, %v", u, err)
	}
	if u, err := c.PortalLink(ctx, "ctm_01", []string{"sub_01"}); err != nil || u != "https://portal.example/x" {
		t.Errorf("portal link = %q, %v", u, err)
	}
	if b := rt.find("POST", "/customers/ctm_01/portal-sessions")[0].body; b["subscription_ids"].([]any)[0] != "sub_01" {
		t.Errorf("portal body = %v", b)
	}
	if u, err := c.InvoiceURL(ctx, "txn_01"); err != nil || u != "https://files.example/inv.pdf" {
		t.Errorf("invoice url = %q, %v", u, err)
	}
	if _, err := c.PortalLink(ctx, "x", nil); err == nil {
		t.Error("a portal link for a customer that is not a Paddle id")
	}
}

func TestCreateDiscount(t *testing.T) {
	rt, c := newRouter(t, map[string]string{"POST /discounts": `{"data":{"id":"dsc_01","code":"WELCOME10"}}`})
	exp := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	refs, err := c.CreateDiscount(context.Background(), payments.DiscountSpec{Description: "Welcome", Type: "percentage", Amount: "10", Code: "WELCOME10", Recurring: true, MaxIntervals: 3, UsageLimit: 100, ExpiresAt: &exp})
	if err != nil || refs.DiscountRef != "dsc_01" || refs.Code != "WELCOME10" {
		t.Fatalf("refs = %+v, %v", refs, err)
	}
	b := rt.find("POST", "/discounts")[0].body
	if b["type"] != "percentage" || b["amount"] != "10" || b["recur"] != true || b["maximum_recurring_intervals"].(float64) != 3 || b["usage_limit"].(float64) != 100 ||
		b["expires_at"] != "2027-01-01T00:00:00Z" || b["code"] != "WELCOME10" || b["currency_code"] != nil {
		t.Errorf("discount = %v", b)
	}
	if _, err := c.CreateDiscount(context.Background(), payments.DiscountSpec{Type: "flat", Amount: "500"}); err == nil {
		t.Error("a flat discount without a currency")
	}
	if _, err := c.CreateDiscount(context.Background(), payments.DiscountSpec{Type: "bogus"}); err == nil {
		t.Error("an unknown discount type")
	}
	rt2, c2 := newRouter(t, map[string]string{"POST /discounts": `{"data":{"id":"dsc_02"}}`})
	if _, err := c2.CreateDiscount(context.Background(), payments.DiscountSpec{Type: "flat", Amount: "500", Currency: "USD"}); err != nil {
		t.Fatal(err)
	}
	if rt2.find("POST", "/discounts")[0].body["currency_code"] != "USD" {
		t.Error("a flat discount carries its currency")
	}
}
