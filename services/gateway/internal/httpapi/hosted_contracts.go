package httpapi

import (
	"html/template"
	"net/http"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// The agreements on the hosted payment pages (docs/plans/21-contracts.md). When the seller requires agreements, the page of an invoice that can be paid shows each
// text that the customer has not accepted yet with an unticked box, and the button says that pressing it is an order with an obligation to pay. The page is a plain
// form (no script: the pages run under default-src 'none'), so the server decides: a pay request without every box, or for a text that was replaced while the
// customer read, starts nothing and shows the page again. Only the page of an invoice asks: it is the one with the pay button. A renewal or a dunning charge never
// comes here (nobody is present), and a link that only makes the invoice (the ad-hoc "continue") takes no money.

// contractView is what the invoice template draws.
type contractView struct {
	Items []contractItemView
	// Consents: the consumer is asked for the request to start at once and the acknowledgement of losing the right of withdrawal, as two boxes of their own.
	Consents bool
	Error    string
}

type contractItemView struct {
	Kind, Label, VersionID, CheckboxText string
	Number                               int32
	HTML                                 template.HTML
	Accepted                             bool
	AcceptedOn                           string
}

// contractCustomer says whom the requirements are about, from the invoice the page shows: the customer of the invoice, or a guest known by the invoice.
func contractCustomer(resp *billingv1.GetHostedInvoiceResponse) (customerID, ref string) {
	if id := resp.GetInvoice().GetCustomerId(); id != "" && id != "00000000-0000-0000-0000-000000000000" {
		return id, ""
	}
	return "", resp.GetInvoice().GetId()
}

func (s *Server) contractRequirementsFor(r *http.Request, resp *billingv1.GetHostedInvoiceResponse, lang string) (*plansv1.GetContractRequirementsResponse, error) {
	tid, err := uuid.Parse(resp.GetPayment().GetTenantId())
	if err != nil {
		return nil, err
	}
	customerID, ref := contractCustomer(resp)
	exp := int(resp.GetCurrencyExponent())
	return s.d.Contracts.GetContractRequirements(tenant.OutgoingFor(r.Context(), tid), &plansv1.GetContractRequirementsRequest{CustomerId: customerID, ContextRef: ref, Locale: lang, CustomerType: resp.GetCustomerType(),
		GuestName: resp.GetCustomerName(), Order: &plansv1.ContractOrder{Description: resp.GetPayment().GetDescription(), Total: money.FormatMinorGrouped(resp.GetInvoice().GetTotalMinor(), exp), Currency: resp.GetInvoice().GetCurrency()}})
}

// contractsView builds what the page asks for. It is nil when nothing is to be accepted; unavailable says the seller's agreements cannot be shown (the page then
// offers no way to pay: a payment is not started without them).
func (s *Server) contractsView(r *http.Request, resp *billingv1.GetHostedInvoiceResponse, lang string, l map[string]string) (v *contractView, unavailable bool) {
	if s.d.Contracts == nil {
		return nil, false
	}
	req, err := s.contractRequirementsFor(r, resp, lang)
	if err != nil {
		s.d.Log.WarnContext(r.Context(), "agreements unavailable", "error", err)
		return nil, true
	}
	if !req.GetEnforced() || req.GetSatisfied() {
		return nil, false
	}
	if len(req.GetMissingKinds()) > 0 {
		return nil, true
	}
	v = &contractView{Consents: req.GetImmediateStart()}
	for _, rq := range req.GetRequirements() {
		item := contractItemView{Kind: rq.GetKind(), Label: kindLabel(lang, rq.GetKind()), VersionID: rq.GetVersionId(), Number: rq.GetVersionNumber(), HTML: template.HTML(rq.GetBodyHtml()), //nolint:gosec // rendered and escaped by plans-svc
			CheckboxText: l["accept_"+rq.GetKind()], Accepted: rq.GetAccepted()}
		if rq.GetAcceptTime() != nil {
			item.AcceptedOn = rq.GetAcceptTime().AsTime().UTC().Format(time.DateOnly)
		}
		v.Items = append(v.Items, item)
	}
	return v, false
}

// kindLabel names a kind of agreement in the page's language.
func kindLabel(lang, kind string) string {
	return hostedText(lang)["kind_"+kind]
}

// requireContracts is the server's gate in front of every payment a hosted page starts. It returns true when the payment may go on: the seller requires nothing, or
// everything required is accepted already, or the form carries every box and the acceptance was recorded. Otherwise it has answered (the page again with what is
// missing, or a refusal) and the payment is not started.
func (s *Server) requireContracts(w http.ResponseWriter, r *http.Request, sc scope, lang string) bool {
	if s.d.Contracts == nil {
		return true
	}
	l := hostedText(lang)
	back := sc.page() + langSuffix(r)
	refuse := func(code int, key string) { s.message(w, r, code, lang, l["pay_title"], l[key], back) }
	resp, err := s.d.Hosted.GetHostedInvoice(r.Context(), sc.get())
	switch {
	case goneError(err):
		hostedNotFound(w, lang)
		return false
	case err != nil:
		refuse(http.StatusServiceUnavailable, "pay_unavailable")
		return false
	case resp.GetInvoice() == nil:
		hostedNotFound(w, lang)
		return false
	}
	if st := resp.GetInvoice().GetStatus(); st == "paid" || st == "void" {
		return true // nothing can be paid on it, and billing says so; no agreement is asked for a payment that cannot start
	}
	lang = customerLang(r, resp.GetCustomerLocale())
	l = hostedText(lang)
	req, err := s.contractRequirementsFor(r, resp, lang)
	if err != nil {
		s.d.Log.WarnContext(r.Context(), "agreements unavailable, no payment started", "error", err)
		refuse(http.StatusServiceUnavailable, "contract_unavailable")
		return false
	}
	if !req.GetEnforced() || req.GetSatisfied() {
		return true
	}
	if len(req.GetMissingKinds()) > 0 {
		s.d.Log.ErrorContext(r.Context(), "a seller requires agreements it has not published, no payment started", "kinds", req.GetMissingKinds())
		refuse(http.StatusServiceUnavailable, "contract_unavailable")
		return false
	}
	form := r.PostForm
	var items []*plansv1.ContractAcceptanceItem
	problem := ""
	for _, rq := range req.GetRequirements() {
		if rq.GetAccepted() {
			continue
		}
		switch {
		case form.Get("v_"+rq.GetKind()) != rq.GetVersionId():
			problem = "contract_stale" // the text was replaced while the customer read, or the form is not from this page
		case form.Get("accept_"+rq.GetKind()) != "1":
			if problem == "" {
				problem = "contract_missing"
			}
		}
		items = append(items, &plansv1.ContractAcceptanceItem{VersionId: rq.GetVersionId(), CheckboxText: l["accept_"+rq.GetKind()]})
	}
	if req.GetImmediateStart() && (form.Get("consent_start") != "1" || form.Get("consent_waiver") != "1") && problem == "" {
		problem = "contract_missing"
	}
	if problem != "" {
		s.agreePage(w, r, sc, lang, resp, problem)
		return false
	}
	tid, _ := uuid.Parse(resp.GetPayment().GetTenantId())
	customerID, _ := contractCustomer(resp)
	rec := &plansv1.RecordContractAcceptanceRequest{CustomerId: customerID, Source: "hosted", ContextKind: "hosted_invoice", ContextRef: resp.GetInvoice().GetId(), Locale: lang, Items: items,
		Ip: s.clientAddr(r), UserAgent: r.UserAgent(), CustomerType: resp.GetCustomerType(), GuestName: resp.GetCustomerName(),
		Order: &plansv1.ContractOrder{Description: resp.GetPayment().GetDescription(), Total: money.FormatMinorGrouped(resp.GetInvoice().GetTotalMinor(), int(resp.GetCurrencyExponent())), Currency: resp.GetInvoice().GetCurrency()}}
	if req.GetImmediateStart() {
		rec.ImmediateStartConsent, rec.ImmediateStartText = true, l["consent_start"]
		rec.WithdrawalWaiverAcknowledged, rec.WithdrawalWaiverText = true, l["consent_waiver"]
	}
	if _, err := s.d.Contracts.RecordContractAcceptance(tenant.OutgoingFor(r.Context(), tid), rec); err != nil {
		switch {
		case errkey.Of(err) == "plans.contract.version_stale":
			s.agreePage(w, r, sc, lang, resp, "contract_stale")
		case status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.InvalidArgument:
			s.d.Log.WarnContext(r.Context(), "acceptance refused", "error", err)
			s.agreePage(w, r, sc, lang, resp, "contract_missing")
		default:
			s.d.Log.ErrorContext(r.Context(), "acceptance not recorded, no payment started", "error", err)
			refuse(http.StatusServiceUnavailable, "pay_unavailable")
		}
		return false
	}
	return true
}

// agreePage shows the invoice page again, with the reason the payment did not start above the boxes (422: the form was not complete).
func (s *Server) agreePage(w http.ResponseWriter, r *http.Request, sc scope, lang string, resp *billingv1.GetHostedInvoiceResponse, problem string) {
	v := s.invoiceView(r.Context(), r, sc, lang, resp)
	if v.Contracts != nil {
		v.Contracts.Error = v.L[problem]
	}
	s.render(w, r, http.StatusUnprocessableEntity, "invoice", v)
}

// contractStrings are the words of the agreement boxes in the six languages of the pages. The sentences next to the boxes are what the acceptance records as the words
// the customer saw; they are the product's wording, not the seller's contract text. UNVERIFIED: no lawyer has reviewed the translations of the sentences in German,
// French, Spanish and Arabic (docs/plans/21-contracts.md).
var contractStrings = map[string]map[string]string{
	"en": {"contract_title": "Agreements", "contract_pay": "Order with obligation to pay", "contract_pay_note": "By pressing the button you place an order that obliges you to pay.",
		"kind_distance_sales": "Distance sales contract", "kind_pre_info": "Pre-information", "kind_tos": "Terms of service", "kind_privacy": "Privacy notice",
		"accept_distance_sales": "I have read and accept the distance sales contract.", "accept_pre_info": "I confirm that I have read the pre-information and received it before the contract is concluded.",
		"accept_tos": "I have read and accept the terms of service.", "accept_privacy": "I have read the privacy notice.",
		"consent_start": "I expressly ask you to start the service now, before the 14-day withdrawal period ends.", "consent_waiver": "I acknowledge that once the service has been fully performed I lose my right of withdrawal.",
		"contract_accepted_on": "Accepted on", "contract_version": "version", "pay_method": "Payment method",
		"contract_missing": "Please read and tick every box below to continue.", "contract_stale": "The text was updated while you were reading. Please read it again and tick the boxes.",
		"contract_unavailable": "This payment cannot be made right now. Please contact the sender."},
	"tr": {"contract_title": "Sözleşmeler", "contract_pay": "Ödeme yükümlülüğü ile siparişi onayla", "contract_pay_note": "Düğmeye bastığınızda ödeme yükümlülüğü doğuran bir sipariş vermiş olursunuz.",
		"kind_distance_sales": "Mesafeli satış sözleşmesi", "kind_pre_info": "Ön bilgilendirme formu", "kind_tos": "Kullanım koşulları", "kind_privacy": "Gizlilik bildirimi",
		"accept_distance_sales": "Mesafeli satış sözleşmesini okudum ve kabul ediyorum.", "accept_pre_info": "Ön bilgilendirme formunu okuduğumu ve sözleşme kurulmadan önce edindiğimi teyit ediyorum.",
		"accept_tos": "Kullanım koşullarını okudum ve kabul ediyorum.", "accept_privacy": "Gizlilik bildirimini okudum.",
		"consent_start": "14 günlük cayma süresi dolmadan hizmetin hemen başlamasını açıkça talep ediyorum.", "consent_waiver": "Hizmet tamamen ifa edildiğinde cayma hakkımı kaybedeceğimi kabul ediyorum.",
		"contract_accepted_on": "Kabul tarihi", "contract_version": "sürüm", "pay_method": "Ödeme yöntemi",
		"contract_missing": "Devam etmek için lütfen aşağıdaki metinleri okuyup her kutuyu işaretleyin.", "contract_stale": "Siz okurken metin güncellendi. Lütfen yeniden okuyup kutuları işaretleyin.",
		"contract_unavailable": "Bu ödeme şu anda yapılamıyor. Lütfen gönderenle iletişime geçin."},
	"de": {"contract_title": "Vereinbarungen", "contract_pay": "Zahlungspflichtig bestellen", "contract_pay_note": "Mit dem Klick auf die Schaltfläche geben Sie eine Bestellung auf, die Sie zur Zahlung verpflichtet.",
		"kind_distance_sales": "Fernabsatzvertrag", "kind_pre_info": "Vorvertragliche Informationen", "kind_tos": "Nutzungsbedingungen", "kind_privacy": "Datenschutzerklärung",
		"accept_distance_sales": "Ich habe den Fernabsatzvertrag gelesen und akzeptiere ihn.", "accept_pre_info": "Ich bestätige, die vorvertraglichen Informationen gelesen und vor Vertragsschluss erhalten zu haben.",
		"accept_tos": "Ich habe die Nutzungsbedingungen gelesen und akzeptiere sie.", "accept_privacy": "Ich habe die Datenschutzerklärung gelesen.",
		"consent_start": "Ich verlange ausdrücklich, dass die Leistung sofort beginnt, noch vor Ablauf der 14-tägigen Widerrufsfrist.", "consent_waiver": "Ich nehme zur Kenntnis, dass ich mein Widerrufsrecht verliere, sobald die Leistung vollständig erbracht ist.",
		"contract_accepted_on": "Akzeptiert am", "contract_version": "Version", "pay_method": "Zahlungsart",
		"contract_missing": "Bitte lesen Sie die Texte unten und setzen Sie jedes Häkchen, um fortzufahren.", "contract_stale": "Der Text wurde aktualisiert, während Sie gelesen haben. Bitte lesen Sie ihn erneut und setzen Sie die Häkchen.",
		"contract_unavailable": "Diese Zahlung kann gerade nicht ausgeführt werden. Bitte wenden Sie sich an den Absender."},
	"fr": {"contract_title": "Accords", "contract_pay": "Commande avec obligation de paiement", "contract_pay_note": "En appuyant sur le bouton, vous passez une commande qui vous oblige à payer.",
		"kind_distance_sales": "Contrat de vente à distance", "kind_pre_info": "Informations précontractuelles", "kind_tos": "Conditions d'utilisation", "kind_privacy": "Politique de confidentialité",
		"accept_distance_sales": "J'ai lu et j'accepte le contrat de vente à distance.", "accept_pre_info": "Je confirme avoir lu les informations précontractuelles et les avoir reçues avant la conclusion du contrat.",
		"accept_tos": "J'ai lu et j'accepte les conditions d'utilisation.", "accept_privacy": "J'ai lu la politique de confidentialité.",
		"consent_start": "Je demande expressément que le service commence maintenant, avant la fin du délai de rétractation de 14 jours.", "consent_waiver": "Je reconnais que je perds mon droit de rétractation dès que le service est pleinement exécuté.",
		"contract_accepted_on": "Accepté le", "contract_version": "version", "pay_method": "Moyen de paiement",
		"contract_missing": "Veuillez lire les textes ci-dessous et cocher chaque case pour continuer.", "contract_stale": "Le texte a été mis à jour pendant votre lecture. Veuillez le relire et cocher les cases.",
		"contract_unavailable": "Ce paiement ne peut pas être effectué pour le moment. Veuillez contacter l'expéditeur."},
	"es": {"contract_title": "Acuerdos", "contract_pay": "Pedido con obligación de pago", "contract_pay_note": "Al pulsar el botón realiza un pedido que le obliga a pagar.",
		"kind_distance_sales": "Contrato de venta a distancia", "kind_pre_info": "Información precontractual", "kind_tos": "Condiciones de uso", "kind_privacy": "Aviso de privacidad",
		"accept_distance_sales": "He leído y acepto el contrato de venta a distancia.", "accept_pre_info": "Confirmo que he leído la información precontractual y que la recibí antes de celebrar el contrato.",
		"accept_tos": "He leído y acepto las condiciones de uso.", "accept_privacy": "He leído el aviso de privacidad.",
		"consent_start": "Solicito expresamente que el servicio comience ahora, antes de que termine el plazo de desistimiento de 14 días.", "consent_waiver": "Reconozco que pierdo mi derecho de desistimiento cuando el servicio se haya ejecutado por completo.",
		"contract_accepted_on": "Aceptado el", "contract_version": "versión", "pay_method": "Método de pago",
		"contract_missing": "Lea los textos de abajo y marque cada casilla para continuar.", "contract_stale": "El texto se actualizó mientras leía. Léalo de nuevo y marque las casillas.",
		"contract_unavailable": "Este pago no se puede realizar ahora. Póngase en contacto con el remitente."},
	"ar": {"contract_title": "الاتفاقيات", "contract_pay": "تأكيد الطلب مع الالتزام بالدفع", "contract_pay_note": "بالضغط على الزر تقدّم طلبًا يلزمك بالدفع.",
		"kind_distance_sales": "عقد البيع عن بعد", "kind_pre_info": "المعلومات المسبقة", "kind_tos": "شروط الخدمة", "kind_privacy": "إشعار الخصوصية",
		"accept_distance_sales": "قرأت عقد البيع عن بعد وأوافق عليه.", "accept_pre_info": "أؤكد أنني قرأت المعلومات المسبقة وتسلمتها قبل إبرام العقد.",
		"accept_tos": "قرأت شروط الخدمة وأوافق عليها.", "accept_privacy": "قرأت إشعار الخصوصية.",
		"consent_start": "أطلب صراحةً أن تبدأ الخدمة الآن قبل انتهاء مهلة الانسحاب البالغة 14 يومًا.", "consent_waiver": "أقرّ بأنني أفقد حقي في الانسحاب بمجرد تنفيذ الخدمة بالكامل.",
		"contract_accepted_on": "تاريخ القبول", "contract_version": "الإصدار", "pay_method": "طريقة الدفع",
		"contract_missing": "يرجى قراءة النصوص أدناه ووضع علامة في كل مربع للمتابعة.", "contract_stale": "تم تحديث النص أثناء قراءتك. يرجى قراءته مرة أخرى ووضع العلامات.",
		"contract_unavailable": "لا يمكن إتمام هذا الدفع الآن. يرجى التواصل مع المرسل."},
}
