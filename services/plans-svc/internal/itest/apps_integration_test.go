//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
)

func testApps(t *testing.T) {
	e := newEnv(t)
	created, err := e.admin.CreateAccount(operator(), &plansv1.CreateAccountRequest{
		Name: "Acme", Currency: "TRY", Timezone: "Europe/Istanbul", GracePeriod: durationpb.New(24 * time.Hour), Tier: plansv1.TenantTier_TENANT_TIER_STANDARD})
	if err != nil {
		t.Fatal(err)
	}
	acct := created.GetAccount()
	if len(acct.GetApps()) != 1 || acct.GetApps()[0].GetSlug() != "default" || len(acct.GetApps()[0].GetTenants()) != 2 || len(acct.GetTenants()) != 2 {
		t.Fatalf("a new account has one default app with a sandbox and a live tenant: %v", acct)
	}
	first := acct.GetApps()[0]
	for _, tn := range first.GetTenants() {
		if tn.GetAppId() != first.GetId() || tn.GetAccountId() != acct.GetId() {
			t.Errorf("tenant %v does not point at its app and account", tn)
		}
	}

	newApp := func(name, slug string) (*plansv1.CreateAppResponse, error) {
		return e.admin.CreateApp(operator(), &plansv1.CreateAppRequest{AccountId: acct.GetId(), Name: name, Slug: slug, Currency: "USD", Timezone: "UTC",
			GracePeriod: durationpb.New(0), Tier: plansv1.TenantTier_TENANT_TIER_FREE})
	}
	shop, err := newApp("Web shop", "")
	if err != nil {
		t.Fatal(err)
	}
	if shop.GetApp().GetSlug() != "web-shop" || len(shop.GetApp().GetTenants()) != 2 || shop.GetApp().GetTenants()[0].GetCurrency() != "USD" {
		t.Fatalf("app = %v", shop.GetApp())
	}
	if _, err := newApp("Web shop", "web-shop"); status.Code(err) != codes.AlreadyExists || errkey.Of(err) != "plans.app.slug_taken" {
		t.Errorf("a duplicate slug: %v (%q)", err, errkey.Of(err))
	}
	if _, err := newApp("Bad", "Not A Slug"); status.Code(err) != codes.InvalidArgument || errkey.Of(err) != "plans.app.bad_slug" {
		t.Errorf("a bad slug: %v", err)
	}
	if _, err := e.admin.CreateApp(operator(), &plansv1.CreateAppRequest{AccountId: "00000000-0000-4000-8000-000000000000", Name: "X", Currency: "USD", Timezone: "UTC", GracePeriod: durationpb.New(0)}); status.Code(err) != codes.NotFound {
		t.Errorf("an unknown account: %v", err)
	}
	if _, err := e.admin.CreateApp(nil2(), &plansv1.CreateAppRequest{AccountId: acct.GetId(), Name: "X"}); status.Code(err) != codes.PermissionDenied && status.Code(err) != codes.Unauthenticated {
		t.Errorf("without the operator: %v", err)
	}
	listed, err := e.admin.ListApps(operator(), &plansv1.ListAppsRequest{AccountId: acct.GetId()})
	if err != nil || len(listed.GetApps()) != 2 || listed.GetApps()[0].GetSlug() != "default" || len(listed.GetApps()[1].GetTenants()) != 2 {
		t.Fatalf("apps = %v, %v", listed, err)
	}

	// Two apps of one account are isolated: a customer of one is not found in the other.
	live := func(app *plansv1.App) *plansv1.Tenant {
		for _, tn := range app.GetTenants() {
			if tn.GetEnvironment() == plansv1.Environment_ENVIRONMENT_LIVE {
				return tn
			}
		}
		t.Fatal("no live tenant")
		return nil
	}
	a, b := live(first), live(shop.GetApp())
	if _, err := e.plans.CreateCustomer(as(a.GetId()), &plansv1.CreateCustomerRequest{ExternalId: "cus_1", Name: "Ada", Email: "ada@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.GetCustomer(as(b.GetId()), &plansv1.GetCustomerRequest{ExternalId: "cus_1"}); status.Code(err) != codes.NotFound {
		t.Errorf("a customer of one app is visible in another app of the same account: %v", err)
	}
	if _, err := e.plans.CreateCustomer(as(b.GetId()), &plansv1.CreateCustomerRequest{ExternalId: "cus_1", Name: "Bob", Email: "bob@example.com"}); err != nil {
		t.Errorf("the same external id in another app: %v", err)
	}
}

// nil2 is a context without the operator flag.
func nil2() context.Context { return context.Background() }

func testAddresses(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Addresses")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	home := &plansv1.Address{Line1: "Atatürk Cd. 1", City: "İstanbul", PostalCode: "34000", Country: "TR"}

	c, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_a", Name: "Ada", Email: "ada@example.com", Address: home})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.GetCustomer(); got.GetCountry() != "TR" || got.GetAddress().GetCity() != "İstanbul" || got.GetAddress().GetLine2() != "" {
		t.Fatalf("customer = %v; the address country is the customer's country", got)
	}
	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_b", Name: "Bob", Email: "bob@example.com", Country: "DE", Address: home}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("two different countries: %v", err)
	}
	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_c", Name: "Cy", Email: "cy@example.com", Address: &plansv1.Address{Line2: "a\nb"}}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("a multi-line part: %v", err)
	}

	moved, err := e.plans.SetCustomerAddress(ctx, &plansv1.SetCustomerAddressRequest{ExternalId: "cus_a", Address: &plansv1.Address{Line1: "Hauptstr. 5", City: "Berlin", PostalCode: "10115", Country: "DE"}})
	if err != nil || moved.GetCustomer().GetCountry() != "DE" || moved.GetCustomer().GetAddress().GetCity() != "Berlin" {
		t.Fatalf("moved = %v, %v", moved, err)
	}
	cleared, err := e.plans.SetCustomerAddress(ctx, &plansv1.SetCustomerAddressRequest{ExternalId: "cus_a", Address: &plansv1.Address{}})
	if err != nil || cleared.GetCustomer().GetAddress() != nil || cleared.GetCustomer().GetCountry() != "DE" {
		t.Fatalf("cleared = %v, %v; clearing the address keeps the country", cleared, err)
	}
	if _, err := e.plans.SetCustomerAddress(ctx, &plansv1.SetCustomerAddressRequest{ExternalId: "nobody", Address: home}); status.Code(err) != codes.NotFound {
		t.Errorf("unknown customer: %v", err)
	}

	seller := &plansv1.Address{Line1: "1 Market St", City: "London", Country: "GB"}
	tn, err := e.admin.UpdateTenantSettings(operator(), &plansv1.UpdateTenantSettingsRequest{TenantId: live.GetId(), Address: seller})
	if err != nil || tn.GetTenant().GetAddress().GetCity() != "London" {
		t.Fatalf("seller address = %v, %v", tn, err)
	}
	if again, err := e.admin.UpdateTenantSettings(operator(), &plansv1.UpdateTenantSettingsRequest{TenantId: live.GetId(), Tier: plansv1.TenantTier_TENANT_TIER_ENTERPRISE}); err != nil || again.GetTenant().GetAddress().GetCity() != "London" {
		t.Errorf("an update without an address changed it: %v, %v", again, err)
	}

	// E-invoice data: the buyer's tax data on the customer, the seller's legal data and the trigger on the tenant.
	taxed, err := e.plans.SetCustomerTax(ctx, &plansv1.SetCustomerTaxRequest{ExternalId: "cus_a", TaxId: "1234567890", TaxOffice: "Kadikoy", EInvoiceRegistered: true})
	if err != nil || taxed.GetCustomer().GetTaxId() != "1234567890" || taxed.GetCustomer().GetTaxOffice() != "Kadikoy" || !taxed.GetCustomer().GetEInvoiceRegistered() {
		t.Fatalf("tax = %v, %v", taxed, err)
	}
	if got, err := e.plans.GetCustomer(ctx, &plansv1.GetCustomerRequest{ExternalId: "cus_a"}); err != nil || got.GetCustomer().GetTaxId() != "1234567890" {
		t.Errorf("stored tax data = %v, %v", got, err)
	}
	if _, err := e.plans.SetCustomerTax(ctx, &plansv1.SetCustomerTaxRequest{ExternalId: "cus_a", TaxId: "12 3"}); errkey.Of(err) != "plans.customer.tax_invalid" {
		t.Errorf("a bad tax id: %v", err)
	}
	if _, err := e.plans.SetCustomerTax(ctx, &plansv1.SetCustomerTaxRequest{ExternalId: "nobody", TaxId: "1234567890"}); status.Code(err) != codes.NotFound {
		t.Errorf("unknown customer: %v", err)
	}
	if live.GetEInvoiceOn() != "off" {
		t.Errorf("a new tenant has e_invoice_on %q, want off", live.GetEInvoiceOn())
	}
	legal := &plansv1.LegalInfo{LegalName: "Acme Yazilim A.S.", TaxId: "9876543210", TaxOffice: "Besiktas", MersisNo: "0123456789012345"}
	on, err := e.admin.UpdateTenantSettings(operator(), &plansv1.UpdateTenantSettingsRequest{TenantId: live.GetId(), Legal: legal, EInvoiceOn: "paid"})
	if err != nil || on.GetTenant().GetEInvoiceOn() != "paid" || on.GetTenant().GetLegal().GetTaxOffice() != "Besiktas" || on.GetTenant().GetLegal().GetMersisNo() != "0123456789012345" {
		t.Fatalf("legal = %v, %v", on, err)
	}
	if _, err := e.admin.UpdateTenantSettings(operator(), &plansv1.UpdateTenantSettingsRequest{TenantId: live.GetId(), EInvoiceOn: "sometimes"}); errkey.Of(err) != "einvoice.setting.invalid" {
		t.Errorf("a bad setting: %v", err)
	}
	if kept, err := e.admin.UpdateTenantSettings(operator(), &plansv1.UpdateTenantSettingsRequest{TenantId: live.GetId(), Tier: plansv1.TenantTier_TENANT_TIER_STANDARD}); err != nil || kept.GetTenant().GetEInvoiceOn() != "paid" || kept.GetTenant().GetLegal().GetTaxId() != "9876543210" {
		t.Errorf("an update without them changed them: %v, %v", kept, err)
	}
}
