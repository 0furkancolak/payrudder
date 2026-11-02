package httpapi

// The words of the portal's subscription summary. The model wrote them and nobody who reads these languages has checked them (docs/plans/04-hosted-pages-and-portal.md).
// The sub_trial_ends, sub_next, sub_ends and sub_changes texts are format strings: %s is a date (sub_changes: the plan, then the date, or the other way round in a language that
// needs it, with %[1]s and %[2]s).
var subscriptionStrings = map[string]map[string]string{
	"en": {"sub_title": "Subscription", "sub_state_trialing": "Trial", "sub_state_active": "Active", "sub_state_past_due": "Payment overdue", "sub_state_paused": "Paused",
		"sub_month": "month", "sub_year": "year", "sub_quantity": "Quantity", "sub_trial_ends": "Trial ends on %s", "sub_next": "Next payment on %s", "sub_ends": "Ends on %s",
		"sub_changes": "Changes to %[1]s on %[2]s", "sub_provider": "Billed and renewed by the payment provider.", "sub_contact": "To change your plan or cancel, contact the seller."},
	"tr": {"sub_title": "Abonelik", "sub_state_trialing": "Deneme", "sub_state_active": "Aktif", "sub_state_past_due": "Ödeme gecikmiş", "sub_state_paused": "Duraklatıldı",
		"sub_month": "ay", "sub_year": "yıl", "sub_quantity": "Adet", "sub_trial_ends": "Deneme süresi %s tarihinde bitiyor", "sub_next": "Sonraki ödeme: %s", "sub_ends": "%s tarihinde sona erecek",
		"sub_changes": "%[2]s tarihinde %[1]s planına geçilecek", "sub_provider": "Faturalama ve yenileme ödeme sağlayıcısı tarafından yapılır.", "sub_contact": "Planınızı değiştirmek veya iptal etmek için satıcıyla iletişime geçin."},
	"de": {"sub_title": "Abonnement", "sub_state_trialing": "Testphase", "sub_state_active": "Aktiv", "sub_state_past_due": "Zahlung überfällig", "sub_state_paused": "Pausiert",
		"sub_month": "Monat", "sub_year": "Jahr", "sub_quantity": "Menge", "sub_trial_ends": "Testphase endet am %s", "sub_next": "Nächste Zahlung am %s", "sub_ends": "Endet am %s",
		"sub_changes": "Wechsel zu %[1]s am %[2]s", "sub_provider": "Abrechnung und Verlängerung erfolgen über den Zahlungsanbieter.", "sub_contact": "Um Ihren Tarif zu ändern oder zu kündigen, wenden Sie sich an den Verkäufer."},
	"fr": {"sub_title": "Abonnement", "sub_state_trialing": "Essai", "sub_state_active": "Actif", "sub_state_past_due": "Paiement en retard", "sub_state_paused": "En pause",
		"sub_month": "mois", "sub_year": "an", "sub_quantity": "Quantité", "sub_trial_ends": "L'essai se termine le %s", "sub_next": "Prochain paiement le %s", "sub_ends": "Se termine le %s",
		"sub_changes": "Passage à %[1]s le %[2]s", "sub_provider": "Facturé et renouvelé par le prestataire de paiement.", "sub_contact": "Pour modifier votre formule ou résilier, contactez le vendeur."},
	"es": {"sub_title": "Suscripción", "sub_state_trialing": "Prueba", "sub_state_active": "Activa", "sub_state_past_due": "Pago vencido", "sub_state_paused": "En pausa",
		"sub_month": "mes", "sub_year": "año", "sub_quantity": "Cantidad", "sub_trial_ends": "La prueba termina el %s", "sub_next": "Próximo pago el %s", "sub_ends": "Termina el %s",
		"sub_changes": "Cambio a %[1]s el %[2]s", "sub_provider": "Facturada y renovada por el proveedor de pagos.", "sub_contact": "Para cambiar de plan o cancelar, contacta con el vendedor."},
	"ar": {"sub_title": "الاشتراك", "sub_state_trialing": "فترة تجريبية", "sub_state_active": "نشط", "sub_state_past_due": "الدفع متأخر", "sub_state_paused": "متوقف مؤقتًا",
		"sub_month": "شهر", "sub_year": "سنة", "sub_quantity": "الكمية", "sub_trial_ends": "تنتهي الفترة التجريبية في %s", "sub_next": "الدفعة التالية في %s", "sub_ends": "ينتهي في %s",
		"sub_changes": "التغيير إلى %[1]s في %[2]s", "sub_provider": "تتم الفوترة والتجديد عبر مزوّد الدفع.", "sub_contact": "لتغيير خطتك أو إلغاء الاشتراك، تواصل مع البائع."},
}
