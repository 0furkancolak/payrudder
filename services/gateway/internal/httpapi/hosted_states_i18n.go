package httpapi

// The words of the states a public invoice page used to leave unexplained (a void invoice, one that could not be collected) and of the page shown when the
// page cannot be built at all. hostedText (hosted.go) merges them like the other maps, falling back to English per key.
var stateStrings = map[string]map[string]string{
	"en": {
		"hint_void":          "This invoice was cancelled. Nothing is due.",
		"hint_uncollectible": "This invoice could not be collected and can no longer be paid here. Please contact the sender.",
		"trouble":            "This page cannot be shown right now.",
		"troubleHint":        "Please try again in a minute.",
		"pay_blocked":        "This payment cannot be made. Please contact the sender.",
	},
	"tr": {
		"hint_void":          "Bu fatura iptal edildi. Ödenecek bir tutar yok.",
		"hint_uncollectible": "Bu fatura tahsil edilemedi ve artık buradan ödenemez. Lütfen gönderenle iletişime geçin.",
		"trouble":            "Bu sayfa şu anda gösterilemiyor.",
		"troubleHint":        "Lütfen bir dakika sonra yeniden deneyin.",
		"pay_blocked":        "Bu ödeme yapılamıyor. Lütfen gönderenle iletişime geçin.",
	},
	"de": {
		"hint_void":          "Diese Rechnung wurde storniert. Es ist nichts zu zahlen.",
		"hint_uncollectible": "Diese Rechnung konnte nicht eingezogen werden und kann hier nicht mehr bezahlt werden. Bitte wenden Sie sich an den Absender.",
		"trouble":            "Diese Seite kann gerade nicht angezeigt werden.",
		"troubleHint":        "Bitte versuchen Sie es in einer Minute erneut.",
		"pay_blocked":        "Diese Zahlung kann nicht ausgeführt werden. Bitte wenden Sie sich an den Absender.",
	},
	"fr": {
		"hint_void":          "Cette facture a été annulée. Rien n'est dû.",
		"hint_uncollectible": "Cette facture n'a pas pu être recouvrée et ne peut plus être payée ici. Veuillez contacter l'expéditeur.",
		"trouble":            "Cette page ne peut pas être affichée pour le moment.",
		"troubleHint":        "Veuillez réessayer dans une minute.",
		"pay_blocked":        "Ce paiement ne peut pas être effectué. Veuillez contacter l'expéditeur.",
	},
	"es": {
		"hint_void":          "Esta factura fue anulada. No hay nada que pagar.",
		"hint_uncollectible": "Esta factura no pudo cobrarse y ya no se puede pagar aquí. Ponte en contacto con quien te la envió.",
		"trouble":            "Esta página no se puede mostrar ahora mismo.",
		"troubleHint":        "Inténtalo de nuevo en un minuto.",
		"pay_blocked":        "Este pago no se puede realizar. Ponte en contacto con quien te lo envió.",
	},
	"ar": {
		"hint_void":          "تم إلغاء هذه الفاتورة. لا يوجد مبلغ مستحق.",
		"hint_uncollectible": "تعذّر تحصيل هذه الفاتورة ولم يعد بالإمكان دفعها هنا. يُرجى التواصل مع المرسل.",
		"trouble":            "تعذّر عرض هذه الصفحة الآن.",
		"troubleHint":        "يُرجى المحاولة مرة أخرى بعد دقيقة.",
		"pay_blocked":        "تعذّر إتمام هذه الدفعة. يُرجى التواصل مع المرسل.",
	},
}
