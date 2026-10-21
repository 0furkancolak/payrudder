package httpapi

import (
	"html/template"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
)

// createInvoiceLink is POST /v1/invoices/{id}/links: an unguessable link to the invoice for the tenant to send to its customer.
func (s *Server) createInvoiceLink(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.CreateInvoiceLinkRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.InvoiceId = r.PathValue("id")
	resp, err := s.d.Billing.CreateInvoiceLink(r.Context(), req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	path := "/i/" + resp.GetToken()
	writeJSONValue(w, http.StatusCreated, map[string]any{
		"token": resp.GetToken(), "url": s.d.PublicURL + path, "expires_at": resp.GetExpireTime().AsTime().UTC().Format(time.RFC3339),
	})
}

// revokeInvoiceLinks is DELETE /v1/invoices/{id}/links: every link to the invoice stops working.
func (s *Server) revokeInvoiceLinks(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.RevokeInvoiceLinks(r.Context(), &billingv1.RevokeInvoiceLinksRequest{InvoiceId: r.PathValue("id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"revoked": resp.GetRevoked()})
}

// hostedLimiter lets a visitor load a handful of pages a minute. A token is 256 random bits, so guessing one is hopeless; this keeps scrapers and
// accidents from hammering billing-svc through a public route. It counts by the connection's address (a proxy in front should pass it on as such).
type hostedLimiter struct {
	mu     sync.Mutex
	window map[string]*hostedWindow
	last   time.Time
}

type hostedWindow struct {
	start time.Time
	n     int
}

const (
	hostedPerMinute = 60
	// hostedPaysPerMinute limits the POSTs that start a payment, per address.
	hostedPaysPerMinute = 10
	hostedMaxKeys       = 50000
)

func (l *hostedLimiter) allow(key string, now time.Time) bool {
	return l.allowN(key, now, hostedPerMinute)
}

// allowN counts a request of key and reports whether it is within perMinute requests in the current minute.
func (l *hostedLimiter) allowN(key string, now time.Time, perMinute int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.window == nil {
		l.window = map[string]*hostedWindow{}
	}
	if now.Sub(l.last) > time.Minute || len(l.window) > hostedMaxKeys {
		for k, w := range l.window {
			if now.Sub(w.start) > time.Minute {
				delete(l.window, k)
			}
		}
		l.last = now
	}
	w := l.window[key]
	if w == nil || now.Sub(w.start) > time.Minute {
		w = &hostedWindow{start: now}
		l.window[key] = w
	}
	w.n++
	return w.n <= perMinute
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) hostedAllowed(w http.ResponseWriter, r *http.Request) bool {
	if s.hosted.allow(clientIP(r), time.Now()) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	http.Error(w, "too many requests", http.StatusTooManyRequests)
	return false
}

// hostedPayAllowed is hostedAllowed for the POSTs that start a payment: they also count against the stricter limit.
func (s *Server) hostedPayAllowed(w http.ResponseWriter, r *http.Request) bool {
	if s.hosted.allow(clientIP(r), time.Now()) && s.hostedPays.allowN(clientIP(r), time.Now(), hostedPaysPerMinute) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	http.Error(w, "too many requests", http.StatusTooManyRequests)
	return false
}

// hostedHeaders makes a page that is private to whoever holds the link: not indexed, not cached, not leaking the link in a Referer, and unable to load anything.
func hostedHeaders(w http.ResponseWriter) {
	hostedHeadersCSP(w, "form-action 'none'")
}

// hostedHeadersCSP is hostedHeaders with the form-action of the page: the pages with a pay form post to themselves, and the provider's checkout is an https redirect of
// that post. img-src 'self' is for the seller's logo, which the gateway serves itself.
func hostedHeadersCSP(w http.ResponseWriter, formAction string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; base-uri 'none'; "+formAction+"; frame-ancestors 'none'")
}

func hostedNotFound(w http.ResponseWriter, lang string) {
	hostedHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_ = hostedGone.Execute(w, map[string]any{"L": hostedText(lang), "Lang": lang, "Dir": hostedDir(lang)})
}

// formatMinor prints minor units as a decimal: 123456 with exponent 2 is 1,234.56.
func formatMinor(v int64, exponent int) string {
	neg := v < 0
	if neg {
		v = -v
	}
	digits := strconv.FormatInt(v, 10)
	for len(digits) <= exponent {
		digits = "0" + digits
	}
	whole, frac := digits[:len(digits)-exponent], digits[len(digits)-exponent:]
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String()
	if exponent > 0 {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

var hostedLangs = []string{"en", "tr", "de", "fr", "es", "ar"}

// pickLang chooses the page language: ?lang= first, then the browser's Accept-Language, else English.
func pickLang(r *http.Request) string {
	want := []string{r.URL.Query().Get("lang")}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		want = append(want, strings.ToLower(strings.SplitN(tag, "-", 2)[0]))
	}
	for _, w := range want {
		for _, l := range hostedLangs {
			if w == l {
				return l
			}
		}
	}
	return "en"
}

func hostedDir(lang string) string {
	if lang == "ar" {
		return "rtl"
	}
	return "ltr"
}

func hostedText(lang string) map[string]string {
	out := map[string]string{}
	for _, src := range []map[string]string{hostedStrings["en"], paymentStrings["en"], creditStrings["en"], hostedStrings[lang], paymentStrings[lang], creditStrings[lang]} {
		for k, v := range src {
			out[k] = v
		}
	}
	return out
}

var hostedStrings = map[string]map[string]string{
	"en": {"invoice": "Invoice", "from": "From", "to": "Billed to", "date": "Date", "status": "Status", "description": "Description", "amount": "Amount", "subtotal": "Subtotal", "total": "Total",
		"pdf": "Download PDF", "gone": "This link does not exist or has expired.", "goneHint": "Ask the sender for a new link.",
		"payBy": "Pay by bank transfer", "bank": "Bank", "holder": "Account holder", "iban": "IBAN", "bic": "BIC / SWIFT", "bankRef": "Payment reference", "referenceHint": "Write this reference on your transfer so that we can match the payment to this invoice.",
		"paid": "Paid", "open": "Open", "void": "Void", "uncollectible": "Uncollectible", "draft": "Draft"},
	"tr": {"invoice": "Fatura", "from": "Düzenleyen", "to": "Fatura edilen", "date": "Tarih", "status": "Durum", "description": "Açıklama", "amount": "Tutar", "subtotal": "Ara toplam", "total": "Toplam",
		"pdf": "PDF indir", "gone": "Bu bağlantı yok ya da süresi dolmuş.", "goneHint": "Gönderenden yeni bir bağlantı isteyin.",
		"payBy": "Havale / EFT ile ödeyin", "bank": "Banka", "holder": "Hesap sahibi", "iban": "IBAN", "bic": "BIC / SWIFT", "bankRef": "Ödeme referansı", "referenceHint": "Ödemenizi bu faturayla eşleştirebilmemiz için havale açıklamasına bu referansı yazın.",
		"paid": "Ödendi", "open": "Açık", "void": "İptal", "uncollectible": "Tahsil edilemedi", "draft": "Taslak"},
	"de": {"invoice": "Rechnung", "from": "Von", "to": "Rechnungsempfänger", "date": "Datum", "status": "Status", "description": "Beschreibung", "amount": "Betrag", "subtotal": "Zwischensumme", "total": "Gesamt",
		"pdf": "PDF herunterladen", "gone": "Dieser Link existiert nicht oder ist abgelaufen.", "goneHint": "Bitten Sie den Absender um einen neuen Link.",
		"payBy": "Per Überweisung zahlen", "bank": "Bank", "holder": "Kontoinhaber", "iban": "IBAN", "bic": "BIC / SWIFT", "bankRef": "Zahlungsreferenz", "referenceHint": "Geben Sie diese Referenz bei der Überweisung an, damit wir die Zahlung dieser Rechnung zuordnen können.",
		"paid": "Bezahlt", "open": "Offen", "void": "Storniert", "uncollectible": "Uneinbringlich", "draft": "Entwurf"},
	"fr": {"invoice": "Facture", "from": "De", "to": "Facturé à", "date": "Date", "status": "Statut", "description": "Description", "amount": "Montant", "subtotal": "Sous-total", "total": "Total",
		"pdf": "Télécharger le PDF", "gone": "Ce lien n'existe pas ou a expiré.", "goneHint": "Demandez un nouveau lien à l'expéditeur.",
		"payBy": "Payer par virement", "bank": "Banque", "holder": "Titulaire du compte", "iban": "IBAN", "bic": "BIC / SWIFT", "bankRef": "Référence de paiement", "referenceHint": "Indiquez cette référence sur votre virement afin que nous puissions rattacher le paiement à cette facture.",
		"paid": "Payée", "open": "Ouverte", "void": "Annulée", "uncollectible": "Irrécouvrable", "draft": "Brouillon"},
	"es": {"invoice": "Factura", "from": "De", "to": "Facturado a", "date": "Fecha", "status": "Estado", "description": "Descripción", "amount": "Importe", "subtotal": "Subtotal", "total": "Total",
		"pdf": "Descargar PDF", "gone": "Este enlace no existe o ha caducado.", "goneHint": "Pide un enlace nuevo a quien te lo envió.",
		"payBy": "Pagar por transferencia", "bank": "Banco", "holder": "Titular de la cuenta", "iban": "IBAN", "bic": "BIC / SWIFT", "bankRef": "Referencia de pago", "referenceHint": "Indica esta referencia en tu transferencia para que podamos asociar el pago a esta factura.",
		"paid": "Pagada", "open": "Abierta", "void": "Anulada", "uncollectible": "Incobrable", "draft": "Borrador"},
	"ar": {"invoice": "فاتورة", "from": "من", "to": "الفاتورة إلى", "date": "التاريخ", "status": "الحالة", "description": "الوصف", "amount": "المبلغ", "subtotal": "المجموع الفرعي", "total": "الإجمالي",
		"pdf": "تنزيل PDF", "gone": "هذا الرابط غير موجود أو انتهت صلاحيته.", "goneHint": "اطلب رابطًا جديدًا من المرسل.",
		"payBy": "الدفع بالتحويل المصرفي", "bank": "البنك", "holder": "صاحب الحساب", "iban": "IBAN", "bic": "BIC / SWIFT", "bankRef": "مرجع الدفع", "referenceHint": "اكتب هذا المرجع في التحويل حتى نتمكن من مطابقة الدفعة مع هذه الفاتورة.",
		"paid": "مدفوعة", "open": "مفتوحة", "void": "ملغاة", "uncollectible": "غير قابلة للتحصيل", "draft": "مسودة"},
}

const hostedCSS = `body{font:16px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;margin:0;background:#f6f7f9;color:#14171c}
main{max-width:760px;margin:0 auto;padding:24px 16px}
.card{background:#fff;border:1px solid #e3e6eb;border-radius:12px;padding:24px}
h1{font-size:1.4rem;margin:0 0 4px}.muted{color:#5b6472;font-size:.9rem}
.meta{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:16px;margin:20px 0}
.meta b{display:block;font-size:.8rem;color:#5b6472;font-weight:600;text-transform:uppercase;letter-spacing:.04em}
table{width:100%;border-collapse:collapse}th,td{padding:10px 8px;border-bottom:1px solid #eceef2;text-align:start;vertical-align:top}
th{font-size:.8rem;color:#5b6472;text-transform:uppercase;letter-spacing:.04em}td.n,th.n{text-align:end;font-variant-numeric:tabular-nums;white-space:nowrap}
tfoot td{font-weight:600;border-bottom:0}.neg{color:#087443}
.badge{display:inline-block;padding:2px 10px;border-radius:999px;font-size:.8rem;background:#e8ebf0}.badge.paid{background:#d9f3e4;color:#075c35}.badge.void,.badge.uncollectible{background:#fde2e1;color:#8a1c17}
.pay{margin-top:20px;padding:16px;border:1px solid #e3e6eb;border-radius:8px}.pay b{display:block;margin-bottom:8px}.pay dl{display:grid;grid-template-columns:max-content 1fr;gap:4px 16px;margin:0}.pay dt{color:#5b6472;font-size:.9rem}.pay dd{margin:0}.mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
a.btn{display:inline-block;margin-top:20px;padding:10px 16px;border-radius:8px;background:#14171c;color:#fff;text-decoration:none}
@media (prefers-color-scheme:dark){body{background:#0f1216;color:#e8eaee}.card{background:#171b21;border-color:#262c35}.pay{border-color:#262c35}.pay dt{color:#9aa4b2}th,td{border-color:#262c35}.muted,.meta b,th{color:#9aa4b2}.badge{background:#262c35}a.btn{background:#e8eaee;color:#14171c}}`

var hostedGone = template.Must(template.New("gone").Parse(`<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow">
<title>404</title><style>` + hostedCSS + `</style></head><body><main><div class="card"><h1>{{.L.gone}}</h1><p class="muted">{{.L.goneHint}}</p></div></main></body></html>`))
