package ubl

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/0furkancolak/quotify/pkg/einvoice"
)

// InvalidError says which field of a document cannot be turned into UBL.
type InvalidError struct{ Field, Msg string }

func (e *InvalidError) Error() string { return "ubl: invalid document: " + e.Field + ": " + e.Msg }

// ErrInvalid matches every *InvalidError with errors.Is.
var ErrInvalid = errors.New("ubl: invalid document")

// Is makes errors.Is(err, ErrInvalid) true.
func (e *InvalidError) Is(target error) bool { return target == ErrInvalid }

func invalid(field, format string, a ...any) error {
	return &InvalidError{Field: field, Msg: fmt.Sprintf(format, a...)}
}

// Sums are the monetary totals of a document, all in minor units of its currency.
type Sums struct {
	// LineExtension is the sum of the item lines.
	LineExtension int64
	// Allowance is the sum of the discounts (document level).
	Allowance int64
	// TaxExclusive is LineExtension - Allowance, the taxable base.
	TaxExclusive int64
	// TaxAmount is the sum of the TAX lines.
	TaxAmount int64
	// TaxInclusive is TaxExclusive + TaxAmount.
	TaxInclusive int64
	// Prepaid is the credit balance applied.
	Prepaid int64
	// Payable is TaxInclusive - Prepaid.
	Payable int64
	// HasTax is false when the document has no TAX line (it is then exempt).
	HasTax bool
	// TaxRateBps is the rate of the tax (2000 is 20 %), 0 without tax.
	TaxRateBps int
	// TaxName is the tax's label without the rate ("VAT"); empty without tax.
	TaxName string
}

var ratePattern = regexp.MustCompile(`(\d+(?:[.,]\d{1,2})?)\s*%`)

// parseRateBps reads "VAT 20%", "KDV 7.5%" or "8,25 %" as basis points.
func parseRateBps(desc string) (bps int, name string, ok bool) {
	m := ratePattern.FindStringSubmatchIndex(desc)
	if m == nil {
		return 0, "", false
	}
	num := strings.Replace(desc[m[2]:m[3]], ",", ".", 1)
	whole, frac, _ := strings.Cut(num, ".")
	for len(frac) < 2 {
		frac += "0"
	}
	w, err1 := strconv.Atoi(whole)
	f, err2 := strconv.Atoi(frac)
	if err1 != nil || err2 != nil {
		return 0, "", false
	}
	return w*100 + f, strings.TrimSpace(desc[:m[0]]), true
}

// Totals derives the totals of doc from its lines and validates their arithmetic (no negative amount, discounts at most the items, credit at most
// the total). The rate comes from the label of the first TAX line, else it is tax / base rounded to basis points.
func Totals(doc einvoice.Document) (Sums, error) {
	var t Sums
	items := 0
	for i, l := range doc.Lines {
		f := fmt.Sprintf("lines[%d]", i)
		if l.AmountMinor < 0 {
			return Sums{}, invalid(f+".amount", "must not be negative")
		}
		switch l.Kind {
		case einvoice.LineItem:
			items++
			t.LineExtension += l.AmountMinor
		case einvoice.LineDiscount:
			t.Allowance += l.AmountMinor
		case einvoice.LineTax:
			if !t.HasTax {
				t.TaxRateBps, t.TaxName, _ = parseRateBps(l.Description)
				if t.TaxRateBps == 0 {
					t.TaxRateBps = -1 // derive below
				}
			}
			t.HasTax = true
			t.TaxAmount += l.AmountMinor
		case einvoice.LineCredit:
			t.Prepaid += l.AmountMinor
		default:
			return Sums{}, invalid(f+".kind", "unknown kind %q", l.Kind)
		}
	}
	if items == 0 {
		return Sums{}, invalid("lines", "at least one item line is required")
	}
	if t.Allowance > t.LineExtension {
		return Sums{}, invalid("lines", "discounts exceed the items")
	}
	t.TaxExclusive = t.LineExtension - t.Allowance
	if t.HasTax && t.TaxAmount == 0 {
		t.HasTax, t.TaxRateBps, t.TaxName = false, 0, ""
	}
	if t.HasTax && t.TaxRateBps == -1 {
		if t.TaxExclusive == 0 {
			return Sums{}, invalid("lines", "tax on a zero base")
		}
		t.TaxRateBps = int((t.TaxAmount*20000/t.TaxExclusive + 1) / 2) // round half up
	}
	if t.TaxName == "" && t.HasTax {
		t.TaxName = "VAT"
	}
	t.TaxInclusive = t.TaxExclusive + t.TaxAmount
	if t.Prepaid > t.TaxInclusive {
		return Sums{}, invalid("lines", "credit applied exceeds the total")
	}
	t.Payable = t.TaxInclusive - t.Prepaid
	return t, nil
}
