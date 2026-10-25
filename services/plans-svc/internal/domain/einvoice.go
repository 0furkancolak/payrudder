package domain

import (
	"strings"
	"unicode/utf8"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// EInvoiceOn values of a tenant.
const (
	EInvoiceOff       = "off"
	EInvoiceFinalized = "finalized"
	EInvoicePaid      = "paid"
)

// ValidateEInvoiceOn checks the tenant setting "when an invoice becomes an e-invoice".
func ValidateEInvoiceOn(v string) error {
	switch v {
	case EInvoiceOff, EInvoiceFinalized, EInvoicePaid:
		return nil
	}
	return errkey.New(codes.InvalidArgument, "einvoice.setting.invalid", "e_invoice_on must be off, finalized or paid")
}

func oneLine(s string, max int) bool {
	return utf8.RuneCountInString(s) <= max && !strings.ContainsAny(s, "\n\r\t\x00")
}

// ValidateLegal checks the seller's company data: each part one line of at most 200 characters, a tax id of letters and digits (a VAT id may start with the country).
func ValidateLegal(l *plansv1.LegalInfo) error {
	for _, p := range []string{l.GetLegalName(), l.GetTaxOffice(), l.GetMersisNo(), l.GetTradeRegistryNo()} {
		if !oneLine(p, 200) {
			return errkey.New(codes.InvalidArgument, "einvoice.setting.invalid", "legal data must be single-line text of at most 200 characters")
		}
	}
	return ValidateTaxID(l.GetTaxId())
}

// ValidateTaxID accepts an empty id or 5 to 20 letters and digits (VKN, TCKN, EU VAT ids).
func ValidateTaxID(id string) error {
	if id == "" {
		return nil
	}
	if n := len(id); n < 5 || n > 20 {
		return errkey.New(codes.InvalidArgument, "plans.customer.tax_invalid", "tax_id must be 5 to 20 letters and digits")
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return errkey.New(codes.InvalidArgument, "plans.customer.tax_invalid", "tax_id must be 5 to 20 letters and digits")
		}
	}
	return nil
}

// ValidateCustomerTax checks a customer's tax data.
func ValidateCustomerTax(req *plansv1.SetCustomerTaxRequest) error {
	if err := ValidateTaxID(req.GetTaxId()); err != nil {
		return err
	}
	if !oneLine(req.GetTaxOffice(), 120) {
		return errkey.New(codes.InvalidArgument, "plans.customer.tax_invalid", "tax_office must be single-line text of at most 120 characters")
	}
	return nil
}
