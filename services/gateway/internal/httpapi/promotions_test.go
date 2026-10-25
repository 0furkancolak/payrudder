package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
)

// QuoteLinkPromotion of the fake billing-svc: SAVE10 takes 10 % off 250.00, BONUS adds credits to the pack, anything else is refused with its key.
func (fakeBilling) QuoteLinkPromotion(_ context.Context, req *billingv1.QuoteLinkPromotionRequest) (*billingv1.QuoteLinkPromotionResponse, error) {
	switch strings.ToUpper(req.GetPromotionCode()) {
	case "":
		return &billingv1.QuoteLinkPromotionResponse{}, nil
	case "SAVE10":
		return &billingv1.QuoteLinkPromotionResponse{Applied: true, PromotionCode: "SAVE10", DiscountMinor: 2500, TotalMinor: 22500}, nil
	case "BONUS":
		return &billingv1.QuoteLinkPromotionResponse{Applied: true, PromotionCode: "BONUS", BonusCredits: 5000, TotalMinor: 1000}, nil
	case "FREE":
		return nil, errkey.New(codes.FailedPrecondition, "billing.payment_link.free_total", "free")
	}
	return nil, errkey.New(codes.FailedPrecondition, "coupon.expired", "expired")
}

func TestPromotionCodeFieldAndQuote(t *testing.T) {
	ts, _ := newTestServer(t)
	_, page := get(t, ts, "/i/"+tokAdhoc)
	for _, want := range []string{`action="/i/` + tokAdhoc + `/promo"`, `name="code"`, "Promotion code", "Apply", "250.00 EUR"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}

	// A code that works: the page shows the price, the discount and the total, and Continue carries the code.
	r, body := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokAdhoc+"/promo", url.Values{"code": {" save10 "}}, nil)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("promo = %d\n%s", r.StatusCode, body)
	}
	for _, want := range []string{"Discount", "-25.00 EUR", "To pay", "225.00 EUR", `<input type="hidden" name="code" value="save10">`, `value="save10"`} {
		if !strings.Contains(body, want) {
			t.Errorf("quoted page lacks %q:\n%s", want, body)
		}
	}
	// Bonus credits on a credit-pack link are shown without a discount.
	_, pack := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokPack+"/promo", url.Values{"code": {"bonus"}}, nil)
	if !strings.Contains(pack, "+5,000 Bonus credits") && !strings.Contains(pack, "+5000 Bonus credits") && !strings.Contains(pack, "Bonus credits") {
		t.Errorf("bonus not shown:\n%s", pack)
	}

	// A code that does not work: the reason in the page's language, status 422, the code still in the field, and no hidden code on Continue.
	for lang, want := range map[string]string{"en": "This code has expired.", "tr": "Bu kodun süresi dolmuş.", "de": "Dieser Code ist abgelaufen.", "fr": "Ce code a expiré.", "es": "Este código ha caducado.",
		"ar": "انتهت صلاحية هذا الرمز."} {
		r, body := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokAdhoc+"/promo?lang="+lang, url.Values{"code": {"old"}}, nil)
		if r.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, want) || !strings.Contains(body, `value="old"`) || strings.Contains(body, `type="hidden" name="code"`) {
			t.Errorf("%s: %d\n%s", lang, r.StatusCode, body)
		}
	}
	ts, _ = newTestServer(t) // the pages allow 10 POSTs a minute per address
	if r, body := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokAdhoc+"/promo", url.Values{"code": {"free"}}, nil); r.StatusCode != http.StatusUnprocessableEntity ||
		!strings.Contains(body, "leaves nothing to pay") {
		t.Errorf("free total = %d\n%s", r.StatusCode, body)
	}
	// An empty field shows the plain page; an unknown link is the usual not-found page; a hostile code is escaped.
	if r, body := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokAdhoc+"/promo", url.Values{"code": {""}}, nil); r.StatusCode != http.StatusOK || strings.Contains(body, "Discount") {
		t.Errorf("empty code = %d", r.StatusCode)
	}
	if r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/nope/promo", url.Values{"code": {"save10"}}, nil); r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown link = %d", r.StatusCode)
	}
	if _, body := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokAdhoc+"/promo", url.Values{"code": {`"><script>alert(1)</script>`}}, nil); strings.Contains(body, "<script>alert(1)") {
		t.Error("the typed code was printed unescaped")
	}
	// A GET never claims or quotes.
	if r, _ := get(t, ts, "/i/"+tokAdhoc+"/promo"); r.StatusCode == http.StatusOK || r.StatusCode == http.StatusSeeOther {
		t.Errorf("GET promo = %d", r.StatusCode)
	}
}

func TestPromotionStringsAreInEveryLanguage(t *testing.T) {
	for _, lang := range hostedLangs {
		for key := range promoStrings["en"] {
			if promoStrings[lang][key] == "" {
				t.Errorf("%s lacks %s", lang, key)
			}
		}
	}
	// Every refusal a code can meet has its sentence: a coupon.* key from plans-svc is explained, not shown as a generic failure.
	for _, key := range []string{"coupon.not_found", "coupon.inactive", "coupon.expired", "coupon.exhausted", "coupon.not_applicable", "coupon.currency_mismatch", "coupon.already_used",
		"coupon.first_purchase_only", "coupon.below_minimum", "coupon.customer_required", "coupon.already_applied", "billing.payment_link.free_total"} {
		if got := promoErrorText(hostedText("en"), key); got == hostedText("en")["promo_err_generic"] {
			t.Errorf("%s has no sentence", key)
		}
	}
}

func TestContinueSendsTheTypedCode(t *testing.T) {
	ts, _ := newTestServer(t)
	startedWith = ""
	r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokAdhoc+"/start", url.Values{"code": {"SAVE10"}}, nil)
	if r.StatusCode != http.StatusSeeOther || startedWith != "SAVE10" {
		t.Fatalf("start = %d, billing was given %q", r.StatusCode, startedWith)
	}
	// A refused code shows the page again with the reason and makes no invoice.
	r, body := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokAdhoc+"/start?lang=tr", url.Values{"code": {"old"}}, nil)
	if r.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Bu kodun süresi dolmuş.") || !strings.Contains(body, `action="/i/`+tokAdhoc+`/start?lang=tr"`) {
		t.Errorf("refused start = %d\n%s", r.StatusCode, body)
	}
	// An over-long code is cut, not passed on.
	startedWith = ""
	send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokAdhoc+"/start", url.Values{"code": {strings.Repeat("A", 100)}}, nil)
	if len(startedWith) != 40 {
		t.Errorf("code of %d characters reached billing", len(startedWith))
	}
}

func TestPortalBuyTakesACode(t *testing.T) {
	ts, _ := newTestServer(t)
	_, page := get(t, ts, "/c/"+portalTok)
	if !strings.Contains(page, `name="code"`) || !strings.Contains(page, "Promotion code") {
		t.Errorf("the portal pack forms have no code field:\n%s", page)
	}
	r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/credits/buy", url.Values{"pack_id": {"pack-1"}, "code": {"SAVE10"}}, nil)
	if r.StatusCode != http.StatusSeeOther || lastPurchase.GetPromotionCode() != "SAVE10" {
		t.Errorf("buy = %d, billing was given %q", r.StatusCode, lastPurchase.GetPromotionCode())
	}
}

func TestPaymentLinkAPIPassesThePromotionCode(t *testing.T) {
	ts, _ := newTestServer(t)
	r, m := do(t, ts, http.MethodPost, "/v1/payment-links", "live-key", `{"kind":"adhoc","amount_minor":"2500","currency":"EUR","description":"Setup","promotion_code":"WELCOME"}`)
	billingMu.Lock()
	got := lastCreate.GetPromotionCode()
	billingMu.Unlock()
	if r.StatusCode != http.StatusCreated || got != "WELCOME" {
		t.Errorf("create = %d %v, billing was given %q", r.StatusCode, m, got)
	}
}
