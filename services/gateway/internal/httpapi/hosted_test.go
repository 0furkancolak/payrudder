package httpapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
)

const goodToken = "0123456789abcdefghijklmnopqrstuvwxyz0123456"

// transferToken is the link of an open invoice paid by bank transfer.
const transferToken = "transfer56789abcdefghijklmnopqrstuvwxyz01234"

func (fakeBilling) CreateInvoiceLink(_ context.Context, req *billingv1.CreateInvoiceLinkRequest) (*billingv1.CreateInvoiceLinkResponse, error) {
	if req.GetInvoiceId() == "draft" {
		return nil, status.Error(codes.FailedPrecondition, "a draft invoice has no number yet and cannot be shared")
	}
	return &billingv1.CreateInvoiceLinkResponse{Token: goodToken, ExpireTime: timestamppb.New(time.Date(2027, 1, 20, 0, 0, 0, 0, time.UTC))}, nil
}

func (fakeBilling) RevokeInvoiceLinks(context.Context, *billingv1.RevokeInvoiceLinksRequest) (*billingv1.RevokeInvoiceLinksResponse, error) {
	return &billingv1.RevokeInvoiceLinksResponse{Revoked: 2}, nil
}

func (fakeBilling) GetHostedInvoice(_ context.Context, req *billingv1.GetHostedInvoiceRequest) (*billingv1.GetHostedInvoiceResponse, error) {
	if req.GetToken() == transferToken {
		return &billingv1.GetHostedInvoiceResponse{
			Invoice:    &billingv1.Invoice{Number: "INV-2026-000009", Currency: "TRY", SubtotalMinor: 150000, TotalMinor: 150000, Status: "open"},
			SellerName: "Acme", CustomerName: "Ada", CurrencyExponent: 2,
			PaymentInstructions: &billingv1.BankTransferInstructions{BankName: "Example <Bank>", AccountHolder: "Acme Ltd", Iban: "TR330006100519786457841326", Bic: "TCZBTR2A", Reference: "INV-2026-000009"},
		}, nil
	}
	if req.GetToken() != goodToken {
		return nil, status.Error(codes.NotFound, "this link does not exist or has expired")
	}
	return &billingv1.GetHostedInvoiceResponse{
		Invoice: &billingv1.Invoice{Number: "INV-2026-000007", Currency: "USD", SubtotalMinor: 123456, TotalMinor: -5, Status: "paid", FinalizeTime: timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
			Lines: []*billingv1.InvoiceLine{
				{Description: `Base fee <script>alert(1)</script>`, AmountMinor: 123456, ServiceFrom: timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)), ServiceTo: timestamppb.New(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))},
				{Description: "Credit", AmountMinor: -123461},
			}},
		SellerName: "Acme & Sons", CustomerName: "Ada", PdfAvailable: true, CurrencyExponent: 2,
		SellerAddressLines: []string{"1 Market St", "London", "GB"}, CustomerAddressLines: []string{"Atatürk Cd. 1", "34000 İstanbul", "TR"},
	}, nil
}

func (fakeBilling) GetHostedInvoicePdfUrl(_ context.Context, req *billingv1.GetHostedInvoicePdfUrlRequest) (*billingv1.GetHostedInvoicePdfUrlResponse, error) {
	if req.GetToken() != goodToken {
		return nil, status.Error(codes.NotFound, "this link does not exist or has expired")
	}
	return &billingv1.GetHostedInvoicePdfUrlResponse{Url: "https://files.example/inv.pdf?sig=1"}, nil
}

func TestFormatMinor(t *testing.T) {
	for _, c := range []struct {
		v    int64
		exp  int
		want string
	}{{123456, 2, "1,234.56"}, {5, 2, "0.05"}, {-5, 2, "-0.05"}, {1000, 0, "1,000"}, {1234567, 3, "1,234.567"}, {0, 2, "0.00"}, {-123461, 2, "-1,234.61"}} {
		if got := formatMinor(c.v, c.exp); got != c.want {
			t.Errorf("formatMinor(%d, %d) = %q, want %q", c.v, c.exp, got, c.want)
		}
	}
}

func TestHostedLinksAreMadeByTheTenant(t *testing.T) {
	ts, _ := newTestServer(t)
	r, m := do(t, ts, http.MethodPost, "/v1/invoices/inv-1/links", "live-key", `{"expires_in_days": 30}`)
	if r.StatusCode != http.StatusCreated || m["token"] != goodToken || m["url"] != "https://api.example.test/i/"+goodToken || m["expires_at"] != "2027-01-20T00:00:00Z" {
		t.Fatalf("create = %d %v", r.StatusCode, m)
	}
	if r, _ := do(t, ts, http.MethodPost, "/v1/invoices/draft/links", "live-key", ""); r.StatusCode != http.StatusConflict {
		t.Errorf("a draft: %d", r.StatusCode)
	}
	if r, _ := do(t, ts, http.MethodPost, "/v1/invoices/inv-1/links", "", ""); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("without a key: %d", r.StatusCode)
	}
	d, dm := do(t, ts, http.MethodDelete, "/v1/invoices/inv-1/links", "live-key", "")
	if d.StatusCode != http.StatusOK || dm["revoked"] != float64(2) {
		t.Errorf("revoke = %d %v", d.StatusCode, dm)
	}
}

func TestHostedInvoicePage(t *testing.T) {
	ts, _ := newTestServer(t)
	get := func(path, acceptLang string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+path, nil)
		if acceptLang != "" {
			req.Header.Set("Accept-Language", acceptLang)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	resp, page := get("/i/"+goodToken, "")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("status %d, type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{"INV-2026-000007", "1,234.56", "-1,234.61", "Acme &amp; Sons", "1 Market St", "34000 İstanbul", "2026-10-01 – 2026-11-01", `href="/i/` + goodToken + `/pdf"`, "Paid"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "<script>alert") {
		t.Error("the page printed a script from an invoice line unescaped")
	}
	h := resp.Header
	if h.Get("Cache-Control") != "no-store" || !strings.Contains(h.Get("X-Robots-Tag"), "noindex") || h.Get("Referrer-Policy") != "no-referrer" ||
		!strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("headers = %v", h)
	}

	// The language follows the browser, or ?lang=.
	if _, tr := get("/i/"+goodToken, "tr-TR,tr;q=0.9,en;q=0.8"); !strings.Contains(tr, "Fatura INV-2026-000007") || !strings.Contains(tr, "Ödendi") {
		t.Error("Turkish page expected")
	}
	if _, ar := get("/i/"+goodToken+"?lang=ar", "en"); !strings.Contains(ar, `dir="rtl"`) {
		t.Error("an Arabic page is right-to-left")
	}

	// Anything but a live token is the same 404 page.
	for _, path := range []string{"/i/nope", "/i/" + strings.Repeat("x", 43), "/i/" + goodToken + "x/pdf"} {
		if resp, body := get(path, ""); resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "does not exist or has expired") {
			t.Errorf("%s: %d %q", path, resp.StatusCode, body)
		}
	}

	// The PDF is a redirect to a short-lived link, not a proxy.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/i/"+goodToken+"/pdf", nil)
	pdf, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pdf.Body.Close() }()
	if pdf.StatusCode != http.StatusFound || pdf.Header.Get("Location") != "https://files.example/inv.pdf?sig=1" || pdf.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("pdf = %d %v", pdf.StatusCode, pdf.Header)
	}
}

func TestHostedPageTellsAnInvoicePaidByTransferWhereToPay(t *testing.T) {
	ts, _ := newTestServer(t)
	get := func(path string) string {
		t.Helper()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	en := get("/i/" + transferToken)
	for _, want := range []string{"Pay by bank transfer", "Example &lt;Bank&gt;", "Acme Ltd", "TR33 0006 1005 1978 6457 8413 26", "TCZBTR2A", "Payment reference", "INV-2026-000009", "Write this reference"} {
		if !strings.Contains(en, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if tr := get("/i/" + transferToken + "?lang=tr"); !strings.Contains(tr, "Havale / EFT ile ödeyin") || !strings.Contains(tr, "Ödeme referansı") {
		t.Errorf("the Turkish page is not translated")
	}
	for _, lang := range hostedLangs {
		for _, key := range []string{"payBy", "bank", "holder", "iban", "bic", "reference", "referenceHint"} {
			if hostedText(lang)[key] == "" {
				t.Errorf("%s lacks the text %q", lang, key)
			}
		}
	}
	if paid := get("/i/" + goodToken); strings.Contains(paid, "Pay by bank transfer") {
		t.Error("an invoice that is not paid by transfer has no bank block")
	}
}

func TestHostedPagesAreRateLimitedPerAddress(t *testing.T) {
	var l hostedLimiter
	now := time.Now()
	for i := 0; i < hostedPerMinute; i++ {
		if !l.allow("1.2.3.4", now) {
			t.Fatalf("request %d refused", i+1)
		}
	}
	if l.allow("1.2.3.4", now) {
		t.Error("the request over the limit was allowed")
	}
	if !l.allow("5.6.7.8", now) {
		t.Error("another address shares the limit")
	}
	if !l.allow("1.2.3.4", now.Add(61*time.Second)) {
		t.Error("the window never reopens")
	}
}
