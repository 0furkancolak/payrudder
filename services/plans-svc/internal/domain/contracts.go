package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// The kinds of agreement a seller presents to its customers (docs/plans/21-contracts.md).
const (
	ContractDistanceSales = "distance_sales"
	ContractPreInfo       = "pre_info"
	ContractTOS           = "tos"
	ContractPrivacy       = "privacy"

	AudienceConsumer = "consumer"
	AudienceBusiness = "business"
)

// ContractKinds are the kinds, in the order a page shows them.
var ContractKinds = []string{ContractDistanceSales, ContractPreInfo, ContractTOS, ContractPrivacy}

// ContractLocales are the languages a text can be written in (the customer languages).
var ContractLocales = []string{"en", "tr", "de", "fr", "es", "ar"}

// ContractVariables are the placeholders a text may use, {{name}}; nothing else is substituted, so a text cannot reach any other data.
var ContractVariables = []string{"seller_name", "seller_address", "seller_tax_id", "seller_tax_office", "seller_mersis", "customer_name", "customer_email", "customer_address",
	"order_description", "order_total", "order_currency", "date"}

// Limits on what a seller writes.
const (
	MaxContractBody  = 60000
	MaxCheckboxText  = 1000
	MaxContractNote  = 500
	MinRetentionMths = 12
	MaxRetentionMths = 120
	DefaultRetention = 36
)

// ValidContractKind reports whether k is a kind of agreement.
func ValidContractKind(k string) bool { return slices.Contains(ContractKinds, k) }

// AudienceOf is the audience a customer type gets: a business gets the business texts, an unknown type or an individual the consumer's (the safer default: the
// consumer's text gives rights a business does not have, never the other way round).
func AudienceOf(customerType string) string {
	if customerType == "business" {
		return AudienceBusiness
	}
	return AudienceConsumer
}

var variableRe = regexp.MustCompile(`\{\{\s*([a-z_]+)\s*\}\}`)

// ContractVariablesIn lists the variables a text uses, once each in the order they first appear, and the placeholders that are not a known variable (including a
// "{{" or "}}" that closes nothing).
func ContractVariablesIn(body string) (used, unknown []string) {
	rest := variableRe.ReplaceAllString(body, "")
	if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
		unknown = append(unknown, "{{ }}")
	}
	for _, m := range variableRe.FindAllStringSubmatch(body, -1) {
		name := m[1]
		switch {
		case !slices.Contains(ContractVariables, name):
			if !slices.Contains(unknown, name) {
				unknown = append(unknown, name)
			}
		case !slices.Contains(used, name):
			used = append(used, name)
		}
	}
	return used, unknown
}

// ValidateContractBodies checks the texts of a version to publish: at least one, only known languages, not empty, not too long, no unknown variable. It returns the
// variables the texts use.
func ValidateContractBodies(bodies map[string]string) ([]string, error) {
	if len(bodies) == 0 {
		return nil, errkey.New(codes.InvalidArgument, "plans.contract.body_required", "at least one text is required")
	}
	var used []string
	for _, loc := range sortedKeys(bodies) {
		body := bodies[loc]
		switch {
		case !slices.Contains(ContractLocales, loc):
			return nil, Invalid("bodies", fmt.Sprintf("%q is not a language of the texts (%s)", loc, strings.Join(ContractLocales, ", ")))
		case strings.TrimSpace(body) == "":
			return nil, errkey.Newf(codes.InvalidArgument, "plans.contract.body_required", "the %s text is empty", loc)
		case len(body) > MaxContractBody:
			return nil, Invalid("bodies", fmt.Sprintf("the %s text is longer than %d characters", loc, MaxContractBody))
		}
		u, unknown := ContractVariablesIn(body)
		if len(unknown) > 0 {
			return nil, errkey.Newf(codes.InvalidArgument, "plans.contract.unknown_variable", "the %s text uses %s; the variables are %s", loc, strings.Join(unknown, ", "), strings.Join(ContractVariables, ", "))
		}
		for _, v := range u {
			if !slices.Contains(used, v) {
				used = append(used, v)
			}
		}
	}
	return used, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ContractHash identifies a version: the sha256 over its kind, audience and every text in a fixed order, each length-prefixed so that no text can be read as another.
func ContractHash(kind, audience string, bodies map[string]string) string {
	h := sha256.New()
	fmt.Fprintf(h, "contract/v1\n%s\n%s\n", kind, audience)
	for _, loc := range sortedKeys(bodies) {
		fmt.Fprintf(h, "%s\n%d\n%s\n", loc, len(bodies[loc]), bodies[loc])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TextHash is the sha256 of one text, as the acceptance holds it.
func TextHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// PickContractBody chooses the text to serve: the requested language, else English, else the first language in a fixed order. It says which language it chose.
func PickContractBody(bodies map[string]string, locale string) (body, served string) {
	for _, loc := range append([]string{locale, "en"}, ContractLocales...) {
		if b, ok := bodies[loc]; ok && loc != "" {
			return b, loc
		}
	}
	return "", ""
}

// ContractContext is what the variables of a text are resolved from.
type ContractContext struct {
	SellerName, SellerAddress, SellerTaxID, SellerTaxOffice, SellerMersis string
	CustomerName, CustomerEmail, CustomerAddress                          string
	OrderDescription, OrderTotal, OrderCurrency                           string
	At                                                                    time.Time
}

// Values is the variable map of a context, with an empty value as a dash (a text never shows a bare hole).
func (c ContractContext) Values() map[string]string {
	v := map[string]string{
		"seller_name": c.SellerName, "seller_address": c.SellerAddress, "seller_tax_id": c.SellerTaxID, "seller_tax_office": c.SellerTaxOffice, "seller_mersis": c.SellerMersis,
		"customer_name": c.CustomerName, "customer_email": c.CustomerEmail, "customer_address": c.CustomerAddress,
		"order_description": c.OrderDescription, "order_total": c.OrderTotal, "order_currency": c.OrderCurrency, "date": c.At.UTC().Format(time.DateOnly),
	}
	for k, s := range v {
		if strings.TrimSpace(s) == "" {
			v[k] = "—"
		}
	}
	return v
}

// ContractSample are the values the console's preview shows.
var ContractSample = map[string]string{
	"seller_name": "Acme Yazılım A.Ş.", "seller_address": "Example St 1, 34000 Istanbul, TR", "seller_tax_id": "1234567890", "seller_tax_office": "Kadıköy", "seller_mersis": "0123456789012345",
	"customer_name": "Ada Lovelace", "customer_email": "ada@example.com", "customer_address": "Example Rd 7, London, GB",
	"order_description": "Pro plan, monthly", "order_total": "12.00", "order_currency": "EUR", "date": "2026-01-31",
}

var (
	boldRe     = regexp.MustCompile(`\*\*(.+?)\*\*`)
	orderedRe  = regexp.MustCompile(`^\d+[.)]\s+`)
	headingRe  = regexp.MustCompile(`^(#{1,3})\s+(.*)$`)
	holderRe   = regexp.MustCompile("\x00([a-z_]+)\x00")
	whitespace = regexp.MustCompile(`\s+`)
)

// RenderContractHTML turns a text into safe HTML. The text is restricted markdown: "#", "##" and "###" headings, "- " bullets, "1. " numbered items, **bold** and
// blank-line paragraphs; everything else is text, and all of it is escaped (raw HTML never reaches a page). A {{variable}} is replaced by its value, escaped; with
// mark each value is wrapped in <mark> so a preview shows what is variable.
func RenderContractHTML(body string, values map[string]string, mark bool) string {
	var out strings.Builder
	var para []string
	list := "" // "ul", "ol" or ""
	flush := func() {
		if len(para) > 0 {
			out.WriteString("<p>" + inline(strings.Join(para, " ")) + "</p>\n")
			para = nil
		}
	}
	closeList := func() {
		if list != "" {
			out.WriteString("</" + list + ">\n")
			list = ""
		}
	}
	for _, raw := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			flush()
			closeList()
		case headingRe.MatchString(line):
			flush()
			closeList()
			m := headingRe.FindStringSubmatch(line)
			level := len(m[1]) + 2 // the page owns h1 and h2
			fmt.Fprintf(&out, "<h%d>%s</h%d>\n", level, inline(m[2]), level)
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			flush()
			if list != "ul" {
				closeList()
				out.WriteString("<ul>\n")
				list = "ul"
			}
			out.WriteString("<li>" + inline(strings.TrimSpace(line[2:])) + "</li>\n")
		case orderedRe.MatchString(line):
			flush()
			if list != "ol" {
				closeList()
				out.WriteString("<ol>\n")
				list = "ol"
			}
			out.WriteString("<li>" + inline(orderedRe.ReplaceAllString(line, "")) + "</li>\n")
		default:
			closeList()
			para = append(para, line)
		}
	}
	flush()
	closeList()
	return holderRe.ReplaceAllStringFunc(out.String(), func(m string) string {
		name := holderRe.FindStringSubmatch(m)[1]
		v, ok := values[name]
		if !ok {
			return html.EscapeString("{{" + name + "}}")
		}
		if mark {
			return "<mark>" + html.EscapeString(v) + "</mark>"
		}
		return html.EscapeString(v)
	})
}

// inline escapes a line, marks its variables with placeholders (replaced after the bold rule has run, so a value can never be read as markdown) and applies **bold**.
func inline(s string) string {
	s = html.EscapeString(s)
	s = variableRe.ReplaceAllString(s, "\x00$1\x00")
	return boldRe.ReplaceAllString(s, "<strong>$1</strong>")
}

// Template notices: the first line of a starter text. A text that still has it is a template nobody reviewed; the console and the go-live list say so.
const (
	NoticeEN = "TEMPLATE - not legal advice. Have a lawyer review and adapt this text before you use it."
	NoticeTR = "ŞABLON - hukuki tavsiye değildir. Bu metni kullanmadan önce bir avukata inceletin ve uyarlayın."
)

// HasTemplateNotice reports whether a text still carries a starter notice.
func HasTemplateNotice(body string) bool {
	return strings.Contains(body, NoticeEN) || strings.Contains(body, NoticeTR)
}

// AnyTemplateNotice reports whether any of the texts of a version carries a starter notice.
func AnyTemplateNotice(bodies map[string]string) bool {
	for _, b := range bodies {
		if HasTemplateNotice(b) {
			return true
		}
	}
	return false
}

// ContractSeries is the newest version of a (kind, audience) series, with the number of its newest material version.
type ContractSeries struct {
	ID             string
	Kind, Audience string
	Number         int
	MaterialNumber int
	Bodies         map[string]string
	SHA256         string
}

// ChooseSeries picks the series a customer of an audience sees for a kind: a business customer's own when the seller published one, else the consumer's.
func ChooseSeries(current []ContractSeries, kind, audience string) (ContractSeries, bool) {
	if audience == AudienceBusiness {
		for _, s := range current {
			if s.Kind == kind && s.Audience == AudienceBusiness {
				return s, true
			}
		}
	}
	for _, s := range current {
		if s.Kind == kind && s.Audience == AudienceConsumer {
			return s, true
		}
	}
	return ContractSeries{}, false
}

// AcceptedNumbers is what a customer (or a guest's invoice) accepted of one series: the highest version number, and the highest accepted together with both the
// immediate-start request and the withdrawal-waiver acknowledgement.
type AcceptedNumbers struct {
	Number, ConsentNumber int
	At                    time.Time
}

// Covers reports whether the acceptance satisfies the series: the customer accepted the newest material version or a later one. When the seller asks consumers for
// the immediate-start consents, the distance sales contract must have been accepted with them.
func (a AcceptedNumbers) Covers(s ContractSeries, needConsent bool) bool {
	n := a.Number
	if needConsent && s.Kind == ContractDistanceSales && s.Audience == AudienceConsumer {
		n = a.ConsentNumber
	}
	return n >= s.MaterialNumber && n > 0
}

// KindLabels are the names of the kinds in the languages the snapshot is written in (the others read English).
var KindLabels = map[string]map[string]string{
	"en": {ContractDistanceSales: "Distance sales contract", ContractPreInfo: "Pre-information", ContractTOS: "Terms of service", ContractPrivacy: "Privacy notice"},
	"tr": {ContractDistanceSales: "Mesafeli satış sözleşmesi", ContractPreInfo: "Ön bilgilendirme formu", ContractTOS: "Kullanım koşulları", ContractPrivacy: "Gizlilik bildirimi"},
	"de": {ContractDistanceSales: "Fernabsatzvertrag", ContractPreInfo: "Vorvertragliche Informationen", ContractTOS: "Nutzungsbedingungen", ContractPrivacy: "Datenschutzerklärung"},
	"fr": {ContractDistanceSales: "Contrat de vente à distance", ContractPreInfo: "Informations précontractuelles", ContractTOS: "Conditions d'utilisation", ContractPrivacy: "Politique de confidentialité"},
	"es": {ContractDistanceSales: "Contrato de venta a distancia", ContractPreInfo: "Información precontractual", ContractTOS: "Condiciones de uso", ContractPrivacy: "Aviso de privacidad"},
	"ar": {ContractDistanceSales: "عقد البيع عن بعد", ContractPreInfo: "المعلومات المسبقة", ContractTOS: "شروط الخدمة", ContractPrivacy: "إشعار الخصوصية"},
}

// KindLabel names a kind in a language.
func KindLabel(locale, kind string) string {
	if l, ok := KindLabels[locale][kind]; ok {
		return l
	}
	return KindLabels["en"][kind]
}

// SnapshotItem is one accepted text in a snapshot.
type SnapshotItem struct {
	Kind          string
	Number        int
	LocaleServed  string
	Body          string
	BodySHA256    string
	VersionSHA256 string
	CheckboxText  string
}

// Snapshot is everything a snapshot page is made of, all of it from the stored acceptance (no IP address and no user agent, which are erased later: the page and its
// hash must not change then).
type Snapshot struct {
	Seller, Customer, Email string
	AcceptedAt              time.Time
	Locale                  string
	Values                  map[string]string
	Items                   []SnapshotItem
	ImmediateStart          string // the text of the request the customer made; empty: not asked
	WaiverAck               string // the text of the acknowledgement; empty: not asked
	Source                  string
	Observed                map[string]bool
}

// RenderSnapshot writes the page a customer accepted: who accepted, when, what exactly (each text as it was rendered, with its hashes) and the words next to each
// box. It is a self-contained HTML document with no script and no external resource, written in the language the texts were served in. The same input gives the same
// bytes, so the hash stored with the acceptance is the hash of the page mailed and of the page fetched later.
func RenderSnapshot(s Snapshot) string {
	lang := s.Locale
	if _, ok := KindLabels[lang]; !ok {
		lang = "en"
	}
	dir := "ltr"
	if lang == "ar" {
		dir = "rtl"
	}
	e := html.EscapeString
	var b strings.Builder
	fmt.Fprintf(&b, "<!doctype html>\n<html lang=\"%s\" dir=\"%s\"><head><meta charset=\"utf-8\"><title>%s</title>", lang, dir, e(snapshotText(lang, "title")))
	b.WriteString("<style>body{font:15px/1.55 system-ui,sans-serif;max-width:760px;margin:24px auto;padding:0 16px;color:#111}h1{font-size:1.4rem}h2{font-size:1.15rem;margin-top:32px}" +
		"dl{display:grid;grid-template-columns:max-content 1fr;gap:4px 16px}dt{color:#555}dd{margin:0;word-break:break-all}.box{border:1px solid #ccc;border-radius:8px;padding:12px 16px;margin:12px 0}" +
		".ack{background:#f3f4f6;padding:8px 12px;border-radius:6px}code{font-size:.85rem}</style></head><body>\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n<dl>", e(snapshotText(lang, "title")))
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "<dt>%s</dt><dd>%s</dd>", e(snapshotText(lang, k)), e(v))
		}
	}
	row("seller", s.Seller)
	row("customer", s.Customer)
	row("email", s.Email)
	row("accepted_at", s.AcceptedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	row("source", snapshotText(lang, "source_"+s.Source))
	b.WriteString("</dl>\n")
	for _, it := range s.Items {
		fmt.Fprintf(&b, "<h2>%s · %s %d</h2>\n<div class=\"box\">%s</div>\n", e(KindLabel(lang, it.Kind)), e(snapshotText(lang, "version")), it.Number, RenderContractHTML(it.Body, s.Values, false))
		fmt.Fprintf(&b, "<p class=\"ack\">&#9745; %s</p>\n<p><code>%s %s · %s %s · %s</code></p>\n", e(it.CheckboxText), e(snapshotText(lang, "text_hash")), it.BodySHA256, e(snapshotText(lang, "version_hash")), it.VersionSHA256, e(it.LocaleServed))
	}
	if s.ImmediateStart != "" || s.WaiverAck != "" {
		fmt.Fprintf(&b, "<h2>%s</h2>\n", e(snapshotText(lang, "consents")))
		if s.ImmediateStart != "" {
			fmt.Fprintf(&b, "<p class=\"ack\">&#9745; %s</p>\n", e(s.ImmediateStart))
		}
		if s.WaiverAck != "" {
			fmt.Fprintf(&b, "<p class=\"ack\">&#9745; %s</p>\n", e(s.WaiverAck))
		}
	}
	if len(s.Observed) > 0 {
		keys := make([]string, 0, len(s.Observed))
		for k := range s.Observed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			state := snapshotText(lang, "claimed")
			if s.Observed[k] {
				state = snapshotText(lang, "observed")
			}
			parts = append(parts, k+": "+state)
		}
		fmt.Fprintf(&b, "<p><small>%s</small></p>\n", e(whitespace.ReplaceAllString(strings.Join(parts, ", "), " ")))
	}
	b.WriteString("</body></html>\n")
	return b.String()
}

var snapshotTexts = map[string]map[string]string{
	"en": {"title": "Record of acceptance", "seller": "Seller", "customer": "Customer", "email": "E-mail", "accepted_at": "Accepted", "source": "Collected by", "source_hosted": "Payrudder payment page", "source_api": "the seller's own checkout",
		"version": "version", "text_hash": "text sha256", "version_hash": "version sha256", "consents": "Immediate start and withdrawal", "observed": "observed by Payrudder", "claimed": "reported by the seller"},
	"tr": {"title": "Kabul kaydı", "seller": "Satıcı", "customer": "Müşteri", "email": "E-posta", "accepted_at": "Kabul zamanı", "source": "Toplayan", "source_hosted": "Payrudder ödeme sayfası", "source_api": "satıcının kendi ödeme akışı",
		"version": "sürüm", "text_hash": "metin sha256", "version_hash": "sürüm sha256", "consents": "Hemen başlama ve cayma", "observed": "Payrudder gözlemledi", "claimed": "satıcı bildirdi"},
}

func snapshotText(lang, key string) string {
	if t, ok := snapshotTexts[lang][key]; ok {
		return t
	}
	return snapshotTexts["en"][key]
}
