package inputs

import (
	"encoding/json"
	"testing"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	sagamodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
)

func tenantIn(country string) *plansv1.Tenant {
	return &plansv1.Tenant{Name: "Acme", Address: &plansv1.Address{Line1: "1 Main St", Country: country},
		Legal: &plansv1.LegalInfo{LegalName: "Acme A.S.", TaxId: "1234567890", TaxOffice: "Kadikoy"}}
}

// What the printed invoice needs is frozen with the draft, whether or not the tenant e-invoices (EInvoiceOn is "off" here and Seller and Buyer stay empty).
func TestSnapshotFreezesTheDocumentFacts(t *testing.T) {
	cu := &plansv1.Customer{Name: "Ada", Email: "ada@example.com", Locale: "de", CustomerType: "business", TaxId: "DE136695976", TaxOffice: "Berlin",
		Address: &plansv1.Address{City: "Berlin", Country: "DE"}}
	snap := SnapshotOf{Tenant: tenantIn("TR"), Customer: cu, Currency: "TRY", CurrencyExponent: 2}.Snapshot()
	got := [...]string{snap.CustomerLocale, snap.CustomerType, snap.DefaultLocale, snap.SellerTaxID, snap.SellerTaxOffice, snap.SellerCountry, snap.CustomerTaxID, snap.CustomerTaxOffice, snap.CustomerCountry}
	want := [...]string{"de", "business", "tr", "1234567890", "Kadikoy", "TR", "DE136695976", "Berlin", "DE"}
	if got != want {
		t.Fatalf("snapshot facts = %q, want %q", got, want)
	}
	if snap.Seller != nil || snap.Buyer != nil {
		t.Error("a tenant that does not e-invoice keeps the small snapshot: no parties")
	}
	// The facts survive the saga row (the snapshot is stored as JSON).
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var back sagamodel.Snapshot
	if err := json.Unmarshal(b, &back); err != nil || back.DefaultLocale != "tr" || back.CustomerTaxID != "DE136695976" || back.SellerCountry != "TR" {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
}

func TestDefaultLocaleIsTheLanguageOfTheSellersCountry(t *testing.T) {
	for country, want := range map[string]string{"TR": "tr", "DE": "de", "AT": "de", "FR": "fr", "ES": "es", "GB": "", "US": "", "SA": "", "": ""} {
		var s sagamodel.Snapshot
		DocumentSnapshot(&s, tenantIn(country), nil)
		if s.DefaultLocale != want {
			t.Errorf("seller in %q: default locale %q, want %q", country, s.DefaultLocale, want)
		}
	}
}

// A customer without an address country falls back to the country of the record; a guest has none of the customer's facts.
func TestDocumentSnapshotCustomerCountryAndGuest(t *testing.T) {
	var s sagamodel.Snapshot
	DocumentSnapshot(&s, tenantIn("TR"), &plansv1.Customer{Country: "FR", Locale: "fr"})
	if s.CustomerCountry != "FR" || s.CustomerLocale != "fr" {
		t.Errorf("snapshot = %+v", s)
	}
	var g sagamodel.Snapshot
	DocumentSnapshot(&g, tenantIn("TR"), nil)
	if g.CustomerCountry != "" || g.CustomerLocale != "" || g.CustomerTaxID != "" || g.SellerTaxID != "1234567890" {
		t.Errorf("guest snapshot = %+v", g)
	}
}

// The tenant's time zone is frozen with the draft, for a customer's invoice and for a guest's alike: it dates the invoice (the year of its number, the dates printed on it).
func TestSnapshotFreezesTheTenantsTimeZone(t *testing.T) {
	tn := tenantIn("TR")
	tn.Timezone = "Europe/Istanbul"
	if got := (SnapshotOf{Tenant: tn, Customer: &plansv1.Customer{}}.Snapshot()).TimeZone; got != "Europe/Istanbul" {
		t.Errorf("customer snapshot zone = %q", got)
	}
	var guest sagamodel.Snapshot
	DocumentSnapshot(&guest, tn, nil)
	if guest.TimeZone != "Europe/Istanbul" {
		t.Errorf("guest snapshot zone = %q", guest.TimeZone)
	}
}
