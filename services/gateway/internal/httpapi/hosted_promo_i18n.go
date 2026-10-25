package httpapi

import (
	"strings"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// The strings of the promotion code field of the payment pages (phase 46) and the refusals a code can meet. hostedText (hosted.go) merges them like the other maps,
// falling back to English per key. A refusal is "promo_err_" and the error key with dots turned into underscores.

var promoStrings = map[string]map[string]string{
	"en": {
		"promo_label": "Promotion code", "promo_apply": "Apply", "promo_discount": "Discount", "promo_pay": "To pay", "promo_bonus": "Bonus credits", "promo_before": "Price",
		"promo_err_generic":                         "This code cannot be used.",
		"promo_err_coupon_not_found":                "This code does not exist. Check it and try again.",
		"promo_err_coupon_inactive":                 "This code is no longer active.",
		"promo_err_coupon_expired":                  "This code has expired.",
		"promo_err_coupon_exhausted":                "This code has been used up.",
		"promo_err_coupon_not_applicable":           "This code does not work for this purchase.",
		"promo_err_coupon_currency_mismatch":        "This code does not work in the currency of this purchase.",
		"promo_err_coupon_already_used":             "You have already used this code.",
		"promo_err_coupon_first_purchase_only":      "This code is for a first purchase only.",
		"promo_err_coupon_below_minimum":            "The purchase is below the minimum amount for this code.",
		"promo_err_coupon_customer_required":        "This code needs a known customer, so it cannot be used here.",
		"promo_err_coupon_already_applied":          "This purchase already uses a coupon.",
		"promo_err_billing_payment_link_free_total": "This code takes the whole price, which leaves nothing to pay. Please ask the seller.",
	},
	"tr": {
		"promo_label": "Promosyon kodu", "promo_apply": "Uygula", "promo_discount": "İndirim", "promo_pay": "Ödenecek", "promo_bonus": "Bonus kredi", "promo_before": "Fiyat",
		"promo_err_generic":                         "Bu kod kullanılamıyor.",
		"promo_err_coupon_not_found":                "Bu kod mevcut değil. Kontrol edip tekrar deneyin.",
		"promo_err_coupon_inactive":                 "Bu kod artık etkin değil.",
		"promo_err_coupon_expired":                  "Bu kodun süresi dolmuş.",
		"promo_err_coupon_exhausted":                "Bu kodun kullanım hakkı tükendi.",
		"promo_err_coupon_not_applicable":           "Bu kod bu alışverişte geçerli değil.",
		"promo_err_coupon_currency_mismatch":        "Bu kod, bu alışverişin para biriminde geçerli değil.",
		"promo_err_coupon_already_used":             "Bu kodu zaten kullandınız.",
		"promo_err_coupon_first_purchase_only":      "Bu kod yalnızca ilk alışveriş için geçerlidir.",
		"promo_err_coupon_below_minimum":            "Alışveriş tutarı bu kodun asgari tutarının altında.",
		"promo_err_coupon_customer_required":        "Bu kod müşterinin bilinmesini gerektirir; burada kullanılamaz.",
		"promo_err_coupon_already_applied":          "Bu alışverişte zaten bir kupon kullanılıyor.",
		"promo_err_billing_payment_link_free_total": "Bu kod fiyatın tamamını düşüyor; ödenecek bir şey kalmıyor. Lütfen satıcıya danışın.",
	},
	"de": {
		"promo_label": "Aktionscode", "promo_apply": "Anwenden", "promo_discount": "Rabatt", "promo_pay": "Zu zahlen", "promo_bonus": "Bonus-Credits", "promo_before": "Preis",
		"promo_err_generic":                         "Dieser Code kann nicht verwendet werden.",
		"promo_err_coupon_not_found":                "Diesen Code gibt es nicht. Bitte prüfen Sie ihn und versuchen Sie es erneut.",
		"promo_err_coupon_inactive":                 "Dieser Code ist nicht mehr aktiv.",
		"promo_err_coupon_expired":                  "Dieser Code ist abgelaufen.",
		"promo_err_coupon_exhausted":                "Dieser Code wurde bereits vollständig eingelöst.",
		"promo_err_coupon_not_applicable":           "Dieser Code gilt für diesen Kauf nicht.",
		"promo_err_coupon_currency_mismatch":        "Dieser Code gilt nicht in der Währung dieses Kaufs.",
		"promo_err_coupon_already_used":             "Sie haben diesen Code bereits verwendet.",
		"promo_err_coupon_first_purchase_only":      "Dieser Code gilt nur für den ersten Kauf.",
		"promo_err_coupon_below_minimum":            "Der Kaufbetrag liegt unter dem Mindestbetrag für diesen Code.",
		"promo_err_coupon_customer_required":        "Dieser Code setzt einen bekannten Kunden voraus und kann hier nicht verwendet werden.",
		"promo_err_coupon_already_applied":          "Für diesen Kauf wird bereits ein Gutschein verwendet.",
		"promo_err_billing_payment_link_free_total": "Dieser Code deckt den ganzen Preis; es bliebe nichts zu zahlen. Bitte wenden Sie sich an den Verkäufer.",
	},
	"fr": {
		"promo_label": "Code promotionnel", "promo_apply": "Appliquer", "promo_discount": "Remise", "promo_pay": "À payer", "promo_bonus": "Crédits bonus", "promo_before": "Prix",
		"promo_err_generic":                         "Ce code ne peut pas être utilisé.",
		"promo_err_coupon_not_found":                "Ce code n'existe pas. Vérifiez-le et réessayez.",
		"promo_err_coupon_inactive":                 "Ce code n'est plus actif.",
		"promo_err_coupon_expired":                  "Ce code a expiré.",
		"promo_err_coupon_exhausted":                "Ce code a été entièrement utilisé.",
		"promo_err_coupon_not_applicable":           "Ce code ne s'applique pas à cet achat.",
		"promo_err_coupon_currency_mismatch":        "Ce code ne s'applique pas dans la devise de cet achat.",
		"promo_err_coupon_already_used":             "Vous avez déjà utilisé ce code.",
		"promo_err_coupon_first_purchase_only":      "Ce code est réservé à un premier achat.",
		"promo_err_coupon_below_minimum":            "Le montant de l'achat est inférieur au minimum requis pour ce code.",
		"promo_err_coupon_customer_required":        "Ce code nécessite un client identifié ; il ne peut pas servir ici.",
		"promo_err_coupon_already_applied":          "Cet achat utilise déjà un coupon.",
		"promo_err_billing_payment_link_free_total": "Ce code couvre tout le prix et il ne resterait rien à payer. Veuillez contacter le vendeur.",
	},
	"es": {
		"promo_label": "Código de promoción", "promo_apply": "Aplicar", "promo_discount": "Descuento", "promo_pay": "A pagar", "promo_bonus": "Créditos extra", "promo_before": "Precio",
		"promo_err_generic":                         "Este código no se puede usar.",
		"promo_err_coupon_not_found":                "Este código no existe. Compruébelo e inténtelo de nuevo.",
		"promo_err_coupon_inactive":                 "Este código ya no está activo.",
		"promo_err_coupon_expired":                  "Este código ha caducado.",
		"promo_err_coupon_exhausted":                "Este código se ha agotado.",
		"promo_err_coupon_not_applicable":           "Este código no es válido para esta compra.",
		"promo_err_coupon_currency_mismatch":        "Este código no es válido en la moneda de esta compra.",
		"promo_err_coupon_already_used":             "Ya ha utilizado este código.",
		"promo_err_coupon_first_purchase_only":      "Este código es solo para una primera compra.",
		"promo_err_coupon_below_minimum":            "El importe de la compra es inferior al mínimo de este código.",
		"promo_err_coupon_customer_required":        "Este código necesita un cliente conocido, por lo que no se puede usar aquí.",
		"promo_err_coupon_already_applied":          "Esta compra ya usa un cupón.",
		"promo_err_billing_payment_link_free_total": "Este código cubre todo el precio y no quedaría nada por pagar. Consulte al vendedor.",
	},
	"ar": {
		"promo_label": "رمز ترويجي", "promo_apply": "تطبيق", "promo_discount": "الخصم", "promo_pay": "المبلغ المستحق", "promo_bonus": "رصيد إضافي", "promo_before": "السعر",
		"promo_err_generic":                         "لا يمكن استخدام هذا الرمز.",
		"promo_err_coupon_not_found":                "هذا الرمز غير موجود. تحقق منه وحاول مرة أخرى.",
		"promo_err_coupon_inactive":                 "هذا الرمز لم يعد فعّالًا.",
		"promo_err_coupon_expired":                  "انتهت صلاحية هذا الرمز.",
		"promo_err_coupon_exhausted":                "استُنفد هذا الرمز بالكامل.",
		"promo_err_coupon_not_applicable":           "هذا الرمز لا ينطبق على هذه العملية.",
		"promo_err_coupon_currency_mismatch":        "هذا الرمز لا ينطبق على عملة هذه العملية.",
		"promo_err_coupon_already_used":             "لقد استخدمت هذا الرمز من قبل.",
		"promo_err_coupon_first_purchase_only":      "هذا الرمز مخصص لأول عملية شراء فقط.",
		"promo_err_coupon_below_minimum":            "مبلغ العملية أقل من الحد الأدنى لهذا الرمز.",
		"promo_err_coupon_customer_required":        "يتطلب هذا الرمز عميلًا معروفًا، لذا لا يمكن استخدامه هنا.",
		"promo_err_coupon_already_applied":          "تستخدم هذه العملية قسيمة بالفعل.",
		"promo_err_billing_payment_link_free_total": "يغطي هذا الرمز السعر كاملًا فلا يبقى شيء للدفع. يرجى مراجعة البائع.",
	},
}

// promoErrorKeys are the refusals of a promotion code the pages explain, by error key.
func isPromoError(key string) bool {
	return strings.HasPrefix(key, "coupon.") || key == errkey.Key("billing.payment_link.free_total")
}

// promoErrorText is the sentence for a refused code in the page's language.
func promoErrorText(l map[string]string, key string) string {
	if t := l["promo_err_"+strings.ReplaceAll(key, ".", "_")]; t != "" {
		return t
	}
	return l["promo_err_generic"]
}
