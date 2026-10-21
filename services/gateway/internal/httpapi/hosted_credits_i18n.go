package httpapi

import "strings"

// The strings of the credit parts of the payment pages (phase 36): the customer portal's balance and "Buy credits" list, and what a credit-pack link shows before the
// payer continues. hostedText (hosted.go) merges them like the other maps, falling back to English per key. "{n}" is replaced with a number.

var creditStrings = map[string]map[string]string{
	"en": {
		"cr_title": "Credits", "cr_balance": "Credit balance", "cr_none": "You have no credits.", "cr_left": "Remaining", "cr_expires": "Expires", "cr_never": "Never expires",
		"cr_buy_title": "Buy credits", "cr_buy": "Buy", "cr_qty": "Quantity", "cr_unit": "credits", "cr_valid": "Valid for {n} days", "cr_forever": "The credits do not expire",
		"cr_you_get": "You get", "cr_price": "Price", "cr_pack": "Credit pack", "cr_unavailable": "Credits cannot be bought right now. Please try again later.",
	},
	"tr": {
		"cr_title": "Krediler", "cr_balance": "Kredi bakiyesi", "cr_none": "Hiç krediniz yok.", "cr_left": "Kalan", "cr_expires": "Son kullanma", "cr_never": "Süresiz",
		"cr_buy_title": "Kredi satın al", "cr_buy": "Satın al", "cr_qty": "Adet", "cr_unit": "kredi", "cr_valid": "{n} gün geçerli", "cr_forever": "Kredilerin süresi dolmaz",
		"cr_you_get": "Alacağınız", "cr_price": "Fiyat", "cr_pack": "Kredi paketi", "cr_unavailable": "Şu anda kredi satın alınamıyor. Lütfen daha sonra tekrar deneyin.",
	},
	"de": {
		"cr_title": "Guthaben", "cr_balance": "Guthabenstand", "cr_none": "Sie haben kein Guthaben.", "cr_left": "Verbleibend", "cr_expires": "Läuft ab", "cr_never": "Läuft nie ab",
		"cr_buy_title": "Guthaben kaufen", "cr_buy": "Kaufen", "cr_qty": "Menge", "cr_unit": "Credits", "cr_valid": "{n} Tage gültig", "cr_forever": "Die Credits verfallen nicht",
		"cr_you_get": "Sie erhalten", "cr_price": "Preis", "cr_pack": "Credit-Paket", "cr_unavailable": "Guthaben kann gerade nicht gekauft werden. Bitte versuchen Sie es später erneut.",
	},
	"fr": {
		"cr_title": "Crédits", "cr_balance": "Solde de crédits", "cr_none": "Vous n'avez aucun crédit.", "cr_left": "Restant", "cr_expires": "Expire le", "cr_never": "N'expire jamais",
		"cr_buy_title": "Acheter des crédits", "cr_buy": "Acheter", "cr_qty": "Quantité", "cr_unit": "crédits", "cr_valid": "Valable {n} jours", "cr_forever": "Les crédits n'expirent pas",
		"cr_you_get": "Vous recevez", "cr_price": "Prix", "cr_pack": "Pack de crédits", "cr_unavailable": "Les crédits ne peuvent pas être achetés pour le moment. Veuillez réessayer plus tard.",
	},
	"es": {
		"cr_title": "Créditos", "cr_balance": "Saldo de créditos", "cr_none": "No tiene créditos.", "cr_left": "Restante", "cr_expires": "Caduca", "cr_never": "No caduca",
		"cr_buy_title": "Comprar créditos", "cr_buy": "Comprar", "cr_qty": "Cantidad", "cr_unit": "créditos", "cr_valid": "Válido durante {n} días", "cr_forever": "Los créditos no caducan",
		"cr_you_get": "Recibe", "cr_price": "Precio", "cr_pack": "Paquete de créditos", "cr_unavailable": "No se pueden comprar créditos ahora. Inténtelo de nuevo más tarde.",
	},
	"ar": {
		"cr_title": "الأرصدة", "cr_balance": "رصيد الأرصدة", "cr_none": "ليس لديك أي رصيد.", "cr_left": "المتبقي", "cr_expires": "ينتهي في", "cr_never": "لا ينتهي",
		"cr_buy_title": "شراء أرصدة", "cr_buy": "شراء", "cr_qty": "الكمية", "cr_unit": "رصيد", "cr_valid": "صالحة لمدة {n} يومًا", "cr_forever": "الأرصدة لا تنتهي صلاحيتها",
		"cr_you_get": "ستحصل على", "cr_price": "السعر", "cr_pack": "باقة أرصدة", "cr_unavailable": "لا يمكن شراء الأرصدة الآن. يرجى المحاولة لاحقًا.",
	},
}

// validText renders a validity in days ("Valid for 30 days"), or the "do not expire" text for 0.
func validText(l map[string]string, days int32) string {
	if days <= 0 {
		return l["cr_forever"]
	}
	return strings.ReplaceAll(l["cr_valid"], "{n}", itoa(int64(days)))
}
