package render

import "github.com/0furkancolak/payrudder/pkg/localefmt"

// key names a piece of text the invoice prints. Every layout has every key (TestEveryLocaleHasEveryKey).
type key string

const (
	kInvoice       key = "invoice"
	kNumber        key = "number"
	kPeriod        key = "period"
	kIssued        key = "issued"
	kFrom          key = "from"
	kBillTo        key = "bill_to"
	kDescription   key = "description"
	kQuantity      key = "quantity"
	kAmount        key = "amount"
	kSubtotal      key = "subtotal"
	kTotal         key = "total"
	kPayByTransfer key = "pay_by_transfer"
	kBank          key = "bank"
	kAccountHolder key = "account_holder"
	kIBAN          key = "iban"
	kBIC           key = "bic"
	kPaymentRef    key = "payment_reference"
	kTransferNote  key = "transfer_note"
	kTaxID         key = "tax_id"
	kTaxOffice     key = "tax_office"
	kVATID         key = "vat_id"
	kVKN           key = "vkn"
	kTCKN          key = "tckn"
	kEDocumentNote key = "e_document_note"
	kSandbox       key = "sandbox"
)

// allKeys lists the keys, for the test that every layout has every one.
var allKeys = []key{kInvoice, kNumber, kPeriod, kIssued, kFrom, kBillTo, kDescription, kQuantity, kAmount, kSubtotal, kTotal, kPayByTransfer, kBank, kAccountHolder, kIBAN,
	kBIC, kPaymentRef, kTransferNote, kTaxID, kTaxOffice, kVATID, kVKN, kTCKN, kEDocumentNote, kSandbox}

// layout is one language of the document: its words, and (from pkg/localefmt, which the hosted pages share) how it writes numbers, money and dates.
type layout struct {
	code string
	*localefmt.Format
	words map[key]string
}

// layouts are the languages the PDF can be printed in. Arabic is a language of the customer (customers.locale) that the PDF library cannot print: it needs shaping and a
// right-to-left layout that fpdf does not do, so an Arabic customer gets the English layout (see choose).
var layouts = map[string]*layout{"en": &english, "tr": &turkish, "de": &german, "fr": &french, "es": &spanish}

// choose is the layout for a document: the customer's language, else the language of the seller's country, else English. An Arabic customer gets English and not the
// seller's language: an unreadable fallback is worse than a neutral one. printed is the layout used, requested what was asked for (empty when nobody asked).
func choose(customer, def string) (l *layout, requested string) {
	requested = customer
	if requested == "" {
		requested = def
	}
	if customer == "ar" {
		return &english, requested
	}
	for _, c := range []string{customer, def} {
		if l, ok := layouts[c]; ok {
			return l, requested
		}
	}
	return &english, requested
}

func (l *layout) t(k key) string { return l.words[k] }
