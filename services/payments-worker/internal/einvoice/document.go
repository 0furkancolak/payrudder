// Package einvoice executes IssueEInvoice: it turns the invoice or credit note snapshot of a command into a legal document (pkg/einvoice), has the tenant's
// integrator issue it and keeps the UBL and PDF in object storage.
package einvoice

import (
	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/money"
)

// Placeholders a sandbox document may use for data the sandbox tenant never entered, so the mock can accept it. Live documents never get them, except the
// legal placeholder consumer id that e-arşiv allows for a buyer without a tax id.
const (
	sandboxSellerTaxID = "1234567890"
	consumerTaxID      = "11111111111" // GİB's id for an unidentified consumer
)

func party(p *commandsv1.PartySnapshot) einvoice.Party {
	return einvoice.Party{
		Name: p.GetName(), TaxID: p.GetTaxId(), TaxOffice: p.GetTaxOffice(), RegistrationNo: p.GetRegistrationNo(), TradeRegistryNo: p.GetTradeRegistryNo(),
		Street: p.GetLine1(), AdditionalLine: p.GetLine2(), City: p.GetCity(), Region: p.GetRegion(), PostalCode: p.GetPostalCode(), Country: p.GetCountry(),
		Email: p.GetEmail(), EInvoiceRegistered: p.GetEInvoiceRegistered(),
	}
}

func lineKind(kind string, amount int64) (einvoice.LineKind, int64) {
	switch kind {
	case "TAX":
		return einvoice.LineTax, amount
	case "DISCOUNT":
		return einvoice.LineDiscount, money.Abs(amount)
	case "CREDIT":
		return einvoice.LineCredit, money.Abs(amount)
	}
	if amount < 0 { // a correction that takes something back lowers the base like a discount
		return einvoice.LineDiscount, -amount
	}
	return einvoice.LineItem, amount
}

// Document maps a command to the provider-neutral document. The type is decided later (it depends on the seller's country and the buyer's registration).
func Document(cmd *commandsv1.IssueEInvoice, sandbox bool) einvoice.Document {
	snap := cmd.GetDocument()
	d := einvoice.Document{
		Kind: einvoice.Kind(cmd.GetKind()), UUID: cmd.GetDocumentId(), Number: snap.GetNumber(), IssueDate: snap.GetIssuedAt().AsTime().UTC(),
		Currency: snap.GetCurrency(), Exponent: int(snap.GetCurrencyExponent()), Seller: party(snap.GetSeller()), Buyer: party(snap.GetBuyer()),
		Reason: cmd.GetReason(), Sandbox: sandbox,
	}
	if d.Seller.Name == "" {
		d.Seller.Name = snap.GetTenantName()
	}
	if d.Buyer.Name == "" {
		d.Buyer.Name = snap.GetCustomerName()
	}
	if d.Buyer.Email == "" {
		d.Buyer.Email = snap.GetCustomerEmail()
	}
	d.Buyer.CustomerType = snap.GetCustomerType()
	for i, l := range snap.GetLines() {
		k, amount := lineKind(l.GetKind(), l.GetAmountMinor())
		d.Lines = append(d.Lines, einvoice.Line{No: i + 1, Kind: k, Description: l.GetDescription(), Quantity: l.GetQuantity(), UnitLabel: l.GetUnitLabel(), AmountMinor: amount})
	}
	if d.Kind == einvoice.KindCreditNote {
		d.Reference = &einvoice.Reference{Number: cmd.GetOriginalNumber(), IssueDate: cmd.GetOriginalIssuedAt().AsTime().UTC()}
	}
	return d
}

// ApplySandboxDefaults fills what a sandbox tenant never entered. Live documents are never touched.
func ApplySandboxDefaults(d *einvoice.Document) {
	if d.Seller.TaxID == "" {
		d.Seller.TaxID = sandboxSellerTaxID
	}
	if d.Seller.Country == "" {
		d.Seller.Country = "TR"
	}
	if d.Buyer.Country == "" {
		d.Buyer.Country = d.Seller.Country
	}
	if d.Buyer.TaxID == "" && d.Buyer.Country == "TR" && d.Buyer.CustomerType != customerBusiness {
		d.Buyer.TaxID = consumerTaxID // a business is never given it: IdentifyBuyer refuses it
	}
	if d.Seller.Name == "" {
		d.Seller.Name = "Sandbox seller"
	}
	if d.Buyer.Name == "" {
		d.Buyer.Name = "Sandbox buyer"
	}
}

// customerBusiness is the customer type of a taxpayer; "individual" and unknown are the other cases (docs/plans/05-customer-locale-tax-documents.md).
const customerBusiness = "business"

// IdentifyBuyer says how the buyer of an e-arşiv document is identified. A buyer with a tax id is identified by it, whatever its type. Without one, an individual (or a
// customer whose type nobody entered, as before the type existed) is the placeholder consumer id that e-arşiv allows for a buyer without a tax id. A business is a
// taxpayer: it is never given the consumer placeholder, so the document is refused with a message that names the missing field and the seller can enter the customer's tax id
// and retry. UNVERIFIED: that the tax office must also be present for a business (it is sent as entered, empty when there is none); whether GİB or the integrators reject a
// business under the placeholder is not read from a source here, the refusal is the cautious reading.
func IdentifyBuyer(d *einvoice.Document) (refusal string) {
	if d.Type != einvoice.TypeEArsiv || d.Buyer.TaxID != "" {
		return ""
	}
	if d.Buyer.CustomerType == customerBusiness {
		return "buyer.tax_id: a business customer is identified by its tax id and has none"
	}
	d.Buyer.TaxID = consumerTaxID
	return ""
}

// ChooseType decides the legal channel. Turkey: e-fatura for a registered buyer, e-arşiv otherwise; elsewhere Peppol.
func ChooseType(d einvoice.Document) einvoice.DocumentType {
	if d.Seller.Country != "TR" {
		return einvoice.TypePeppol
	}
	if d.Buyer.EInvoiceRegistered {
		return einvoice.TypeEFatura
	}
	return einvoice.TypeEArsiv
}
