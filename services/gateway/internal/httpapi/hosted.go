package httpapi

import (
	"html/template"
	"maps"
	"net"
	"net/http"
	"slices"
	"time"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
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

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) hostedAllowed(w http.ResponseWriter, r *http.Request) bool {
	if s.limits.pages.allow(s.clientAddr(r), s.now()) {
		return true
	}
	rejectRateLimited(w)
	return false
}

// hostedPayAllowed is hostedAllowed for the POSTs that start a payment: they also count against the stricter limit.
func (s *Server) hostedPayAllowed(w http.ResponseWriter, r *http.Request) bool {
	addr, now := s.clientAddr(r), s.now()
	if s.limits.pages.allow(addr, now) && s.limits.pays.allowN(addr, now, hostedPaysPerMinute) {
		return true
	}
	rejectRateLimited(w)
	return false
}

// rejectRateLimited answers a public page a visitor asked for too often: 429, to try again in a minute.
func rejectRateLimited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", retryAfterMinute)
	http.Error(w, "too many requests", http.StatusTooManyRequests)
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

// pickLang chooses the page language: ?lang= first, then the browser's Accept-Language, else English.
func pickLang(r *http.Request) string {
	if want := r.URL.Query().Get("lang"); slices.Contains(hostedLangs, want) {
		return want
	}
	for _, tag := range acceptedTags(r) {
		if l := matchLocale(tag); l != "" {
			return l
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

// hostedTexts is the text of the public pages per language: English first, then the language's own over it, so a text a translation lacks reads in English.
var hostedTexts = func() map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, lang := range hostedLangs {
		merged := map[string]string{}
		for _, src := range []map[string]map[string]string{hostedStrings, paymentStrings, creditStrings, promoStrings, affiliateStrings} {
			maps.Copy(merged, src["en"])
		}
		for _, src := range []map[string]map[string]string{hostedStrings, paymentStrings, creditStrings, promoStrings, affiliateStrings} {
			maps.Copy(merged, src[lang])
		}
		out[lang] = merged
	}
	return out
}()

// hostedText is the text of the public pages in a language (English for one this API does not know). The map is shared: callers read it, never change it.
func hostedText(lang string) map[string]string {
	if t, ok := hostedTexts[lang]; ok {
		return t
	}
	return hostedTexts["en"]
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

const hostedCSS = `:root{color-scheme:light dark;--radius:1rem;--background:oklch(.965 .007 258);--foreground:oklch(.192 .017 269);--card:oklch(1 0 0);--primary:oklch(.519 .212 266);--primary-foreground:oklch(.985 0 0);--secondary:oklch(.945 .012 258);--muted:oklch(.955 .008 258);--muted-foreground:oklch(.5 .02 264);--accent:oklch(.95 .028 258);--accent-foreground:oklch(.437 .19 265);--destructive:oklch(.55 .21 27);--destructive-soft:oklch(.955 .03 25);--success:oklch(.5 .13 155);--success-soft:oklch(.95 .045 155);--warning:oklch(.52 .11 65);--warning-soft:oklch(.96 .055 85);--border:oklch(.915 .009 258);--input:oklch(.885 .011 258);--ring:oklch(.673 .164 261);--info:oklch(.437 .19 265);--info-soft:oklch(.95 .03 255);--shadow:0 1px 2px rgb(16 24 40/.04),0 10px 28px -12px rgb(16 24 40/.1)}
@media (prefers-color-scheme:dark){:root{--background:oklch(.16 .013 270);--foreground:oklch(.969 .007 261);--card:oklch(.192 .017 269);--primary:oklch(.762 .123 256);--primary-foreground:oklch(.192 .017 269);--secondary:oklch(.26 .02 268);--muted:oklch(.235 .018 268);--muted-foreground:oklch(.72 .02 262);--accent:oklch(.29 .045 262);--accent-foreground:oklch(.93 .03 255);--destructive:oklch(.7 .18 25);--destructive-soft:oklch(.31 .07 25);--success:oklch(.78 .14 155);--success-soft:oklch(.3 .06 155);--warning:oklch(.82 .12 80);--warning-soft:oklch(.32 .06 80);--border:oklch(1 0 0/9%);--input:oklch(1 0 0/14%);--info:oklch(.84 .081 254);--info-soft:oklch(.3 .06 262);--shadow:0 1px 2px rgb(0 0 0/.3)}}
*{box-sizing:border-box}
body{font:15px/1.55 "Inter",ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,"Helvetica Neue",Arial,sans-serif;margin:0;background:var(--background);color:var(--foreground);-webkit-font-smoothing:antialiased}
main{max-width:760px;margin:0 auto;padding:32px 16px 40px}
.card{background:var(--card);border:1px solid var(--border);border-radius:var(--radius);padding:28px;box-shadow:var(--shadow)}
h1{font-size:1.5rem;line-height:1.25;letter-spacing:-.01em;margin:0 0 4px}.muted{color:var(--muted-foreground);font-size:.9rem}
.meta{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:16px;margin:20px 0}
.meta b{display:block;font-size:.75rem;color:var(--muted-foreground);font-weight:600;text-transform:uppercase;letter-spacing:.05em;margin-bottom:2px}
table{width:100%;border-collapse:collapse}th,td{padding:12px 8px;border-bottom:1px solid var(--border);text-align:start;vertical-align:top}
th{font-size:.75rem;color:var(--muted-foreground);font-weight:600;text-transform:uppercase;letter-spacing:.05em}td.n,th.n{text-align:end;font-variant-numeric:tabular-nums;white-space:nowrap}
tfoot td{font-weight:600;border-bottom:0}.neg{color:var(--success)}
.badge{display:inline-flex;align-items:center;gap:6px;padding:2px 10px;border-radius:999px;font-size:.78rem;font-weight:500;background:var(--secondary);color:var(--foreground)}.badge:before{content:"";width:6px;height:6px;border-radius:50%;background:currentColor}.badge.paid{background:var(--success-soft);color:var(--success)}.badge.void,.badge.uncollectible{background:var(--destructive-soft);color:var(--destructive)}
.pay{margin-top:20px;padding:16px;border:1px solid var(--border);border-radius:calc(var(--radius) - 4px)}.pay b{display:block;margin-bottom:8px}.pay dl{display:grid;grid-template-columns:max-content 1fr;gap:4px 16px;margin:0}.pay dt{color:var(--muted-foreground);font-size:.9rem}.pay dd{margin:0}.mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
.was{text-decoration:line-through;color:var(--muted-foreground);font-variant-numeric:tabular-nums}.err{color:var(--destructive);margin:8px 0}
.promo{margin:20px 0 8px}.promo label{display:block;font-size:.75rem;color:var(--muted-foreground);font-weight:600;text-transform:uppercase;letter-spacing:.05em;margin-bottom:6px}
.promo-row{display:flex;gap:8px}
.promo-row input,input.code{flex:1;min-width:0;height:44px;padding:0 12px;border:1px solid var(--input);border-radius:calc(var(--radius) - 6px);background:var(--card);color:inherit;font:inherit;text-transform:uppercase;letter-spacing:.04em}
input.code::placeholder,.promo-row input::placeholder{text-transform:none;letter-spacing:0;color:var(--muted-foreground)}
.promo-row input:focus-visible,input.code:focus-visible,.btn:focus-visible,a:focus-visible{outline:2px solid var(--ring);outline-offset:2px}
.promo-row .btn{margin-top:0}
a.btn{display:inline-block;margin-top:20px;padding:10px 16px;border-radius:calc(var(--radius) - 6px);background:var(--primary);color:var(--primary-foreground);text-decoration:none}`

var hostedGone = template.Must(template.New("gone").Parse(`<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow">
<title>404</title><style>` + hostedCSS + `</style></head><body><main><div class="card"><h1>{{.L.gone}}</h1><p class="muted">{{.L.goneHint}}</p></div></main></body></html>`))
