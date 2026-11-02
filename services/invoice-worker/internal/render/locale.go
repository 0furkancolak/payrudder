package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/0furkancolak/payrudder/pkg/quantity"
)

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

// layout is one language of the document: its words, and how it writes numbers, money and dates (the conventions of CLDR for that language).
type layout struct {
	code string
	// decimal and group separate the fraction and the thousands; minGroup is the fewest digits before the first group separator (Spanish writes 1234,56 but 12.345,67).
	decimal, group string
	minGroup       int
	// moneyGap separates a number from its currency code.
	moneyGap string
	// dateFmt takes the day, the month name and the year; monthYearFmt the month name and the year.
	dateFmt, monthYearFmt string
	months                [12]string
	words                 map[key]string
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

// number writes a plain decimal ("-1234567.50") with this language's separators.
func (l *layout) number(plain string) string {
	neg := strings.HasPrefix(plain, "-")
	whole, frac, hasFrac := strings.Cut(strings.TrimPrefix(plain, "-"), ".")
	if n := len(whole); n > 3 && n-3 >= l.minGroup {
		var b strings.Builder
		for i, c := range whole {
			if i > 0 && (n-i)%3 == 0 {
				b.WriteString(l.group)
			}
			b.WriteRune(c)
		}
		whole = b.String()
	}
	out := whole
	if hasFrac {
		out += l.decimal + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// money writes minor units in the currency's exponent followed by the currency code: 1116 USD → "11.16 USD" in English, "11,16 USD" in German.
func (l *layout) money(minor int64, exp int, currency string) string {
	return l.number(quantity.FormatUnits(minor, exp)) + l.moneyGap + currency
}

// date writes a day in words: "2 November 2026", "2. November 2026", "2 de noviembre de 2026".
func (l *layout) date(t time.Time) string {
	return fmt.Sprintf(l.dateFmt, t.Day(), l.months[t.Month()-1], t.Year())
}

// period writes a billing period id: a calendar month "2026-10" as "October 2026"; anything else as it is.
func (l *layout) period(id string) string {
	t, err := time.Parse("2006-01", id)
	if err != nil {
		return id
	}
	return fmt.Sprintf(l.monthYearFmt, l.months[t.Month()-1], t.Year())
}
