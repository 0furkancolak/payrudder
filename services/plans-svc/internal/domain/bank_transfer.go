package domain

import (
	"strings"
	"unicode/utf8"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
)

// ValidateBankTransfer checks the bank details printed on a tenant's invoices: the texts are one line, the IBAN has valid check digits and is already normalised
// (payments-worker, which owns the account, normalises it), the reference template uses known placeholders. field names the request field for the violations.
func ValidateBankTransfer(field string, d *plansv1.BankTransferDetails) error {
	var v Violations
	// A slice, not a map: the violations come in the order of the fields, so the message that names the first one is always the same.
	for _, part := range []struct{ name, text string }{{"bank_name", d.GetBankName()}, {"account_holder", d.GetAccountHolder()}} {
		switch {
		case strings.TrimSpace(part.text) == "":
			v.Add(field+"."+part.name, "is required")
		case utf8.RuneCountInString(part.text) > 100 || strings.ContainsAny(part.text, "\n\r\t\x00"):
			v.Add(field+"."+part.name, "must be one line of at most 100 characters")
		}
	}
	if iban, err := banktransfer.ValidateIBAN(d.GetIban()); err != nil || iban != d.GetIban() {
		v.Add(field+".iban", "must be a valid IBAN in capitals without spaces")
	}
	if bic := d.GetBic(); bic != "" && (len(bic) != 8 && len(bic) != 11 || bic != strings.ToUpper(bic)) {
		v.Add(field+".bic", "must be 8 or 11 characters in capitals")
	}
	if t := d.GetReferenceTemplate(); t != "" {
		if err := banktransfer.ValidateTemplate(t); err != nil {
			v.Add(field+".reference_template", err.Error())
		}
	}
	for _, c := range d.GetCurrencies() {
		if len(c) != 3 || c != strings.ToUpper(c) {
			v.Add(field+".currencies", "must be ISO 4217 codes in capitals")
			break
		}
	}
	return v.Err()
}
