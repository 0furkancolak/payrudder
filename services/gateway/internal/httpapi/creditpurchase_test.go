package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

var (
	purchaseMu   sync.Mutex
	lastPurchase *billingv1.StartPortalPurchaseRequest
	lastConsume  *billingv1.ConsumeCreditsRequest
	lastSetting  *billingv1.SetCreditSettingsRequest
)

func (fakeBilling) StartPortalPurchase(_ context.Context, req *billingv1.StartPortalPurchaseRequest) (*billingv1.StartPortalPurchaseResponse, error) {
	purchaseMu.Lock()
	lastPurchase = req
	purchaseMu.Unlock()
	switch {
	case req.GetPortalToken() != portalTok:
		return nil, errkey.New(codes.NotFound, "billing.invoice_link.not_found", "this link does not exist or has expired")
	case req.GetPackId() == "pack-withdrawn":
		return nil, errkey.New(codes.FailedPrecondition, "billing.credits.pack_inactive", "this credit pack is not on sale")
	case req.GetPackId() == "pack-broken":
		return nil, status.Error(codes.Unavailable, "down")
	}
	return &billingv1.StartPortalPurchaseResponse{InvoiceToken: tokOpenSandbox}, nil
}

func (fakeBilling) GetCreditSettings(context.Context, *billingv1.GetCreditSettingsRequest) (*billingv1.GetCreditSettingsResponse, error) {
	return &billingv1.GetCreditSettingsResponse{LowThreshold: 50}, nil
}

func (fakeBilling) SetCreditSettings(_ context.Context, req *billingv1.SetCreditSettingsRequest) (*billingv1.SetCreditSettingsResponse, error) {
	purchaseMu.Lock()
	lastSetting = req
	purchaseMu.Unlock()
	if req.GetLowThreshold() < 0 {
		return nil, errkey.New(codes.InvalidArgument, "billing.credits.bad_threshold", "bad threshold")
	}
	return &billingv1.SetCreditSettingsResponse{LowThreshold: req.GetLowThreshold()}, nil
}

func TestCreditPackLinkPageShowsWhatIsBought(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, page := get(t, ts, "/i/"+tokPack)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for _, want := range []string{"You get", "20,000 credits", "Valid for 30 days", "Quantity 2", "10.00 USD", "Starter &lt;i&gt;pack&lt;/i&gt;", `action="/i/` + tokPack + `/start"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Contains(page, "<i>pack</i>") {
		t.Error("the pack name was printed unescaped")
	}
	if _, de := get(t, ts, "/i/"+tokPack+"?lang=de"); !strings.Contains(de, "Sie erhalten") || !strings.Contains(de, "Tage gültig") {
		t.Error("the German page is not translated")
	}
}

func TestPortalShowsCreditsAndBuysAPack(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, page := get(t, ts, "/c/"+portalTok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for _, want := range []string{"Credit balance", "12,500", "2,500 / 10,000", "2026-12-31", "Never expires", "Buy credits", "Starter &lt;b&gt;pack&lt;/b&gt;", "5.00 USD", "40.00 USD", "Valid for 30 days",
		"The credits do not expire", `action="/c/` + portalTok + `/credits/buy"`, `name="pack_id" value="pack-1"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the portal lacks %q", want)
		}
	}
	if strings.Contains(page, "<b>pack</b>") {
		t.Error("the pack name was printed unescaped")
	}
	if _, fr := get(t, ts, "/c/"+portalTok+"?lang=fr"); !strings.Contains(fr, "Acheter des crédits") || !strings.Contains(fr, `action="/c/`+portalTok+`/credits/buy?lang=fr"`) {
		t.Error("the French portal keeps its language through the buy form")
	}

	// Buy: the form carries only the pack and a quantity; the price is billing's.
	r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/credits/buy", url.Values{"pack_id": {"pack-1"}, "quantity": {"3"}, "price_minor": {"1"}}, nil)
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/i/"+tokOpenSandbox {
		t.Fatalf("buy = %d %v", r.StatusCode, r.Header)
	}
	purchaseMu.Lock()
	got := lastPurchase
	purchaseMu.Unlock()
	if got.GetPortalToken() != portalTok || got.GetPackId() != "pack-1" || got.GetQuantity() != 3 {
		t.Errorf("billing was asked %v", got)
	}
	if r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/credits/buy?lang=tr", url.Values{"pack_id": {"pack-1"}}, nil); r.Header.Get("Location") != "/i/"+tokOpenSandbox+"?lang=tr" {
		t.Errorf("the language is lost: %v", r.Header)
	}
	// A bad quantity is passed on as it is for billing to refuse.
	_, _ = send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/credits/buy", url.Values{"pack_id": {"pack-1"}, "quantity": {"abc"}}, nil)
	purchaseMu.Lock()
	bad := lastPurchase.GetQuantity()
	purchaseMu.Unlock()
	if bad != -1 {
		t.Errorf("an unparsable quantity must not become a valid one: %d", bad)
	}

	if r, body := send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/credits/buy", url.Values{"pack_id": {"pack-withdrawn"}}, nil); r.StatusCode != http.StatusConflict || !strings.Contains(body, "Credits cannot be bought right now") {
		t.Errorf("a withdrawn pack: %d %q", r.StatusCode, body)
	}
	if r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/credits/buy", url.Values{"pack_id": {"pack-broken"}}, nil); r.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("billing down: %d", r.StatusCode)
	}
	if r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/c/nope-0123456789abcdefghijklmnopqrstuvwxyz/credits/buy", url.Values{"pack_id": {"pack-1"}}, nil); r.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown portal: %d", r.StatusCode)
	}
}

func TestCreditTextsExistInEverySupportedLanguage(t *testing.T) {
	for _, lang := range hostedLangs {
		for key := range creditStrings["en"] {
			if creditStrings[lang][key] == "" {
				t.Errorf("%s lacks the credit text %q", lang, key)
			}
		}
		if !strings.Contains(validText(hostedText(lang), 30), "30") {
			t.Errorf("%s: the validity text has no number", lang)
		}
	}
}

func TestCreditPackLinkAndSettingsThroughTheTenantAPI(t *testing.T) {
	ts, _ := newTestServer(t)
	// pack_id and quantity reach billing; the amount is not the client's to set, so the gateway has nothing to forward for it.
	_, _ = do(t, ts, http.MethodPost, "/v1/payment-links", "live-key", `{"kind":"credit_pack","pack_id":"3f6c1b8e-0d3f-4b53-9a5a-2f1f3a1d9a11","customer_external_id":"cus_1","quantity":2}`)
	billingMu.Lock()
	req := lastCreate
	billingMu.Unlock()
	if req.GetKind() != "credit_pack" || req.GetPackId() != "3f6c1b8e-0d3f-4b53-9a5a-2f1f3a1d9a11" || req.GetQuantity() != 2 || req.GetCustomerExternalId() != "cus_1" {
		t.Errorf("billing was asked %v", req)
	}

	// credits.low names the customer the way the tenant knows them.
	_, _ = do(t, ts, http.MethodPost, "/v1/customers/cus_1/credits/consume", "live-key", `{"credits":5,"idempotency_key":"k1"}`)
	purchaseMu.Lock()
	consumed := lastConsume
	purchaseMu.Unlock()
	if consumed.GetCustomerExternalId() != "cus_1" {
		t.Errorf("billing was not told the customer's external id: %v", consumed)
	}

	resp, m := do(t, ts, http.MethodGet, "/v1/credits/settings", "live-key", "")
	if resp.StatusCode != http.StatusOK || m["low_threshold"] != "50" {
		t.Fatalf("get settings: %d %v", resp.StatusCode, m)
	}
	resp, m = do(t, ts, http.MethodPut, "/v1/credits/settings", "live-key", `{"low_threshold":"120"}`)
	if resp.StatusCode != http.StatusOK || m["low_threshold"] != "120" {
		t.Fatalf("set settings: %d %v", resp.StatusCode, m)
	}
	purchaseMu.Lock()
	set := lastSetting
	purchaseMu.Unlock()
	if set.GetLowThreshold() != 120 {
		t.Errorf("billing was asked %v", set)
	}
	if resp, m := do(t, ts, http.MethodPut, "/v1/credits/settings", "live-key", `{"low_threshold":"-1"}`); resp.StatusCode != http.StatusBadRequest || errKey(m) != "billing.credits.bad_threshold" {
		t.Errorf("a negative threshold: %d %v", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, http.MethodPut, "/v1/credits/settings", "", `{"low_threshold":"1"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("without a key: %d", resp.StatusCode)
	}
}
