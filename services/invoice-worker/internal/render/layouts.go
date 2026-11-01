package render

// The words of the five layouts. UNVERIFIED: the wording of the legal lines (e_document_note, and "Fatura" as the title of a Turkish document that is not itself the
// e-Fatura or e-Arşiv invoice) has not been read by an accountant (docs/plans/05-customer-locale-tax-documents.md).

var english = layout{code: "en", decimal: ".", group: ",", minGroup: 1, moneyGap: " ", dateFmt: "%d %s %d", monthYearFmt: "%s %d",
	months: [12]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
	words: map[key]string{
		kInvoice: "INVOICE", kNumber: "Number", kPeriod: "Period", kIssued: "Issued", kFrom: "From", kBillTo: "Bill to",
		kDescription: "Description", kQuantity: "Quantity", kAmount: "Amount", kSubtotal: "Subtotal", kTotal: "Total",
		kPayByTransfer: "Pay by bank transfer", kBank: "Bank", kAccountHolder: "Account holder", kIBAN: "IBAN", kBIC: "BIC / SWIFT", kPaymentRef: "Payment reference",
		kTransferNote: "Please write the payment reference on your transfer so that we can match it to this invoice.",
		kTaxID:        "Tax ID", kTaxOffice: "Tax office", kVATID: "VAT ID", kVKN: "VKN (tax number)", kTCKN: "TCKN (national ID)",
		kEDocumentNote: "The legal e-document (e-Fatura or e-Arşiv invoice) for this invoice is issued separately by the seller's e-invoice provider and carries its own number.",
		kSandbox:       "SANDBOX - not a real invoice",
	}}

var turkish = layout{code: "tr", decimal: ",", group: ".", minGroup: 1, moneyGap: " ", dateFmt: "%d %s %d", monthYearFmt: "%s %d",
	months: [12]string{"Ocak", "Şubat", "Mart", "Nisan", "Mayıs", "Haziran", "Temmuz", "Ağustos", "Eylül", "Ekim", "Kasım", "Aralık"},
	words: map[key]string{
		kInvoice: "FATURA", kNumber: "Fatura no", kPeriod: "Dönem", kIssued: "Düzenleme tarihi", kFrom: "Satıcı", kBillTo: "Alıcı",
		kDescription: "Açıklama", kQuantity: "Miktar", kAmount: "Tutar", kSubtotal: "Ara toplam", kTotal: "Genel toplam",
		kPayByTransfer: "Banka havalesi ile ödeme", kBank: "Banka", kAccountHolder: "Hesap sahibi", kIBAN: "IBAN", kBIC: "BIC / SWIFT", kPaymentRef: "Ödeme referansı",
		kTransferNote: "Ödemenizi bu faturayla eşleştirebilmemiz için havale açıklamasına ödeme referansını yazın.",
		kTaxID:        "Vergi kimlik no", kTaxOffice: "Vergi dairesi", kVATID: "KDV kimlik no (VAT ID)", kVKN: "VKN", kTCKN: "TCKN",
		kEDocumentNote: "Bu faturanın yasal e-belgesi (e-Fatura veya e-Arşiv Fatura) satıcının e-fatura sağlayıcısı tarafından ayrıca düzenlenir ve kendi numarasını taşır.",
		kSandbox:       "SANDBOX - gerçek fatura değildir",
	}}

var german = layout{code: "de", decimal: ",", group: ".", minGroup: 1, moneyGap: " ", dateFmt: "%d. %s %d", monthYearFmt: "%s %d",
	months: [12]string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"},
	words: map[key]string{
		kInvoice: "RECHNUNG", kNumber: "Rechnungsnummer", kPeriod: "Zeitraum", kIssued: "Ausgestellt am", kFrom: "Von", kBillTo: "Rechnung an",
		kDescription: "Beschreibung", kQuantity: "Menge", kAmount: "Betrag", kSubtotal: "Zwischensumme", kTotal: "Gesamtbetrag",
		kPayByTransfer: "Zahlung per Banküberweisung", kBank: "Bank", kAccountHolder: "Kontoinhaber", kIBAN: "IBAN", kBIC: "BIC / SWIFT", kPaymentRef: "Verwendungszweck",
		kTransferNote: "Bitte geben Sie bei der Überweisung den Verwendungszweck an, damit wir die Zahlung dieser Rechnung zuordnen können.",
		kTaxID:        "Steuernummer", kTaxOffice: "Finanzamt", kVATID: "USt-IdNr.", kVKN: "VKN (türkische Steuernummer)", kTCKN: "TCKN (türkische Identitätsnummer)",
		kEDocumentNote: "Das rechtlich maßgebliche E-Dokument (e-Fatura oder e-Arşiv-Rechnung) zu dieser Rechnung stellt der E-Rechnungs-Anbieter des Verkäufers gesondert aus; es trägt eine eigene Nummer.",
		kSandbox:       "SANDBOX - keine echte Rechnung",
	}}

var french = layout{code: "fr", decimal: ",", group: " ", minGroup: 1, moneyGap: " ", dateFmt: "%d %s %d", monthYearFmt: "%s %d",
	months: [12]string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"},
	words: map[key]string{
		kInvoice: "FACTURE", kNumber: "Numéro", kPeriod: "Période", kIssued: "Date d'émission", kFrom: "Émetteur", kBillTo: "Facturé à",
		kDescription: "Description", kQuantity: "Quantité", kAmount: "Montant", kSubtotal: "Sous-total", kTotal: "Total",
		kPayByTransfer: "Paiement par virement bancaire", kBank: "Banque", kAccountHolder: "Titulaire du compte", kIBAN: "IBAN", kBIC: "BIC / SWIFT", kPaymentRef: "Référence de paiement",
		kTransferNote: "Veuillez indiquer la référence de paiement sur votre virement afin que nous puissions le rattacher à cette facture.",
		kTaxID:        "N° fiscal", kTaxOffice: "Centre des impôts", kVATID: "N° de TVA", kVKN: "VKN (numéro fiscal turc)", kTCKN: "TCKN (numéro d'identité turc)",
		kEDocumentNote: "Le document électronique légal (e-Fatura ou facture e-Arşiv) de cette facture est émis séparément par le prestataire de facturation électronique du vendeur et porte son propre numéro.",
		kSandbox:       "SANDBOX - ce n'est pas une vraie facture",
	}}

var spanish = layout{code: "es", decimal: ",", group: ".", minGroup: 2, moneyGap: " ", dateFmt: "%d de %s de %d", monthYearFmt: "%s de %d",
	months: [12]string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"},
	words: map[key]string{
		kInvoice: "FACTURA", kNumber: "Número", kPeriod: "Período", kIssued: "Fecha de emisión", kFrom: "Emisor", kBillTo: "Facturar a",
		kDescription: "Descripción", kQuantity: "Cantidad", kAmount: "Importe", kSubtotal: "Subtotal", kTotal: "Total",
		kPayByTransfer: "Pago por transferencia bancaria", kBank: "Banco", kAccountHolder: "Titular de la cuenta", kIBAN: "IBAN", kBIC: "BIC / SWIFT", kPaymentRef: "Referencia de pago",
		kTransferNote: "Indique la referencia de pago en su transferencia para que podamos asociarla a esta factura.",
		kTaxID:        "NIF / ID fiscal", kTaxOffice: "Oficina tributaria", kVATID: "N.º de IVA", kVKN: "VKN (número fiscal turco)", kTCKN: "TCKN (número de identidad turco)",
		kEDocumentNote: "El documento electrónico legal (e-Fatura o factura e-Arşiv) de esta factura lo emite por separado el proveedor de facturación electrónica del vendedor y lleva su propio número.",
		kSandbox:       "SANDBOX - no es una factura real",
	}}
