package httpapi

// The words of the instalment choice that a saved card's pay form shows when the account enables instalments and the provider quotes them (docs/plans/07-iyzico-parity.md),
// in the six languages of the pages. {count}, {each} and {total} are filled in with the provider's own quote, formatted in the page's language. UNVERIFIED: the German,
// French, Spanish, Arabic and Turkish wording has been read by no native speaker.
var installmentStrings = map[string]map[string]string{
	"en": {"pay_installments": "Instalments", "pay_installment_one": "One payment of {total}", "pay_installment_n": "{count} instalments of {each} ({total} in all)",
		"pay_installments_hint": "Your bank may charge for instalments: the total shows what you pay in all. This applies to your saved card only."},
	"tr": {"pay_installments": "Taksit", "pay_installment_one": "Tek çekim: {total}", "pay_installment_n": "{count} taksit × {each} (toplam {total})",
		"pay_installments_hint": "Bankanız taksit için ek ücret alabilir: toplam, ödeyeceğiniz tutarı gösterir. Yalnızca kayıtlı kartınız için geçerlidir."},
	"de": {"pay_installments": "Ratenzahlung", "pay_installment_one": "Einmalzahlung: {total}", "pay_installment_n": "{count} Raten zu je {each} ({total} insgesamt)",
		"pay_installments_hint": "Ihre Bank kann für Raten ein Entgelt berechnen: die Gesamtsumme zeigt, was Sie insgesamt zahlen. Gilt nur für Ihre gespeicherte Karte."},
	"fr": {"pay_installments": "Paiement en plusieurs fois", "pay_installment_one": "En une fois : {total}", "pay_installment_n": "{count} fois {each} ({total} au total)",
		"pay_installments_hint": "Votre banque peut facturer le paiement en plusieurs fois : le total indique ce que vous payez en tout. Valable pour votre carte enregistrée seulement."},
	"es": {"pay_installments": "Pago en cuotas", "pay_installment_one": "Un solo pago: {total}", "pay_installment_n": "{count} cuotas de {each} ({total} en total)",
		"pay_installments_hint": "Tu banco puede cobrar un recargo por las cuotas: el total muestra lo que pagas en conjunto. Solo para tu tarjeta guardada."},
	"ar": {"pay_installments": "التقسيط", "pay_installment_one": "دفعة واحدة: {total}", "pay_installment_n": "{count} أقساط بقيمة {each} (الإجمالي {total})",
		"pay_installments_hint": "قد يفرض مصرفك رسومًا على التقسيط: الإجمالي يبيّن ما تدفعه في المجموع. ينطبق على بطاقتك المحفوظة فقط."},
}
