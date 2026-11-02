//go:build integration

package itest

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/blocklist"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// A blocklist keeps a person the seller (or the operator) turned away from coming back under another customer id: an e-mail address, a tax id or an external id on the app's
// own list or on the platform-wide one refuses a new customer and a new address or tax id of an existing one. Customers that exist are not touched.
func testBlocklist(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Blocklist")[plansv1.Environment_ENVIRONMENT_LIVE]
	other := e.account(t, "Blocklist other")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx, octx := as(live.GetId()), as(other.GetId())
	newCustomer := func(c context.Context, id, email string) error {
		_, err := e.plans.CreateCustomer(c, &plansv1.CreateCustomerRequest{ExternalId: id, Name: id, Email: email})
		return err
	}
	add := func(c context.Context, kind, value, note string) *plansv1.BlocklistEntry {
		t.Helper()
		r, err := e.plans.AddBlocklistEntry(c, &plansv1.AddBlocklistEntryRequest{Kind: kind, Value: value, Note: note, Actor: "ada@example.com"})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetEntry()
	}

	t.Run("the entries are normalised, kept once and listed", func(t *testing.T) {
		a := add(ctx, "email", "  Fraud@Example.COM ", "stole a card")
		if a.GetValue() != "fraud@example.com" || a.GetNote() != "stole a card" || a.GetCreatedBy() != "ada@example.com" || a.GetCreateTime() == nil {
			t.Fatalf("entry = %v", a)
		}
		if again := add(ctx, "email", "fraud@example.com", "another note"); again.GetId() != a.GetId() || again.GetNote() != "stole a card" {
			t.Errorf("adding what is there returns it as it is: %v", again)
		}
		add(ctx, "tax_id", "tr 1234-567890", "")
		add(ctx, "external_id", "cus_gone", "")
		l, err := e.plans.ListBlocklist(ctx, &plansv1.ListBlocklistRequest{})
		if err != nil || len(l.GetEntries()) != 3 {
			t.Fatalf("list = %v, %v", l, err)
		}
		if o, _ := e.plans.ListBlocklist(octx, &plansv1.ListBlocklistRequest{}); len(o.GetEntries()) != 0 {
			t.Errorf("another app sees %v", o.GetEntries())
		}
		_, err = e.plans.AddBlocklistEntry(ctx, &plansv1.AddBlocklistEntryRequest{Kind: "card", Value: "4242"})
		wantCode(t, "a kind that does not exist", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = e.plans.AddBlocklistEntry(ctx, &plansv1.AddBlocklistEntryRequest{Kind: "email", Value: "nobody"})
		wantCode(t, "an e-mail address without an @", err, codes.InvalidArgument, "plans.request.invalid")
	})

	t.Run("a new customer that matches is refused, one that does not is made", func(t *testing.T) {
		wantCode(t, "a listed e-mail address in other capitals", newCustomer(ctx, "cus_new1", "FRAUD@example.com"), codes.FailedPrecondition, "plans.customer.blocklisted")
		wantCode(t, "a listed external id", newCustomer(ctx, "cus_gone", "fine@example.com"), codes.FailedPrecondition, "plans.customer.blocklisted")
		if _, err := e.plans.GetCustomer(ctx, &plansv1.GetCustomerRequest{ExternalId: "cus_new1"}); code(t, err) != codes.NotFound {
			t.Errorf("a refused customer must not exist: %v", err)
		}
		if err := newCustomer(ctx, "cus_ok", "ok@example.com"); err != nil {
			t.Fatalf("a customer that is not listed: %v", err)
		}
		if err := newCustomer(octx, "cus_new1", "fraud@example.com"); err != nil {
			t.Fatalf("another app's list does not hold here: %v", err)
		}
	})

	t.Run("a change to a listed e-mail address or tax id is refused; other changes are not", func(t *testing.T) {
		_, err := e.plans.SetCustomerContact(ctx, &plansv1.SetCustomerContactRequest{ExternalId: "cus_ok", Name: "Ok", Email: "fraud@example.com"})
		wantCode(t, "a new address that is listed", err, codes.FailedPrecondition, "plans.customer.blocklisted")
		_, err = e.plans.SetCustomerTax(ctx, &plansv1.SetCustomerTaxRequest{ExternalId: "cus_ok", TaxId: "TR1234567890"})
		wantCode(t, "a new tax id that is listed", err, codes.FailedPrecondition, "plans.customer.blocklisted")
		// A customer who is on the list already (the entry came after them) can still change their name: only a new value is checked.
		add(ctx, "email", "ok@example.com", "")
		if _, err := e.plans.SetCustomerContact(ctx, &plansv1.SetCustomerContactRequest{ExternalId: "cus_ok", Name: "Okay", Email: "OK@example.com"}); err != nil {
			t.Errorf("a name change of a customer whose address was listed after they came: %v", err)
		}
		if _, err := e.plans.SetCustomerTax(ctx, &plansv1.SetCustomerTaxRequest{ExternalId: "cus_ok", TaxId: "1111111111"}); err != nil {
			t.Errorf("a tax id that is not listed: %v", err)
		}
	})

	t.Run("an entry that is removed stops holding, and one that is not there is not found", func(t *testing.T) {
		l, _ := e.plans.ListBlocklist(ctx, &plansv1.ListBlocklistRequest{})
		var id string
		for _, en := range l.GetEntries() {
			if en.GetValue() == "fraud@example.com" {
				id = en.GetId()
			}
		}
		if _, err := e.plans.RemoveBlocklistEntry(ctx, &plansv1.RemoveBlocklistEntryRequest{Id: id}); err != nil {
			t.Fatal(err)
		}
		if err := newCustomer(ctx, "cus_new1", "fraud@example.com"); err != nil {
			t.Fatalf("after the entry was removed: %v", err)
		}
		_, err := e.plans.RemoveBlocklistEntry(ctx, &plansv1.RemoveBlocklistEntryRequest{Id: id})
		wantCode(t, "removing it twice", err, codes.NotFound, "")
		_, err = e.plans.RemoveBlocklistEntry(octx, &plansv1.RemoveBlocklistEntryRequest{Id: l.GetEntries()[0].GetId()})
		wantCode(t, "removing another app's entry", err, codes.NotFound, "")
	})

	t.Run("the operator's list holds for every app, and does not say it is the operator's", func(t *testing.T) {
		// An app's own call (not the operator) is refused by plans-svc itself, whatever the gateway lets through.
		_, err := e.admin.ListPlatformBlocklist(ctx, &plansv1.ListPlatformBlocklistRequest{})
		wantCode(t, "an app reading the operator's list", err, codes.PermissionDenied, "")
		_, err = e.admin.AddPlatformBlocklistEntry(ctx, &plansv1.AddPlatformBlocklistEntryRequest{Kind: "email", Value: "x@example.com"})
		wantCode(t, "an app adding to the operator's list", err, codes.PermissionDenied, "")
		added, err := e.admin.AddPlatformBlocklistEntry(operator(), &plansv1.AddPlatformBlocklistEntryRequest{Kind: "email", Value: "Banned@Example.com", Note: "fraud ring", Actor: "root@example.com"})
		if err != nil || added.GetEntry().GetValue() != "banned@example.com" || added.GetEntry().GetCreatedBy() != "root@example.com" {
			t.Fatalf("add = %v, %v", added, err)
		}
		for _, c := range []struct {
			name string
			ctx  context.Context
		}{{"first app", ctx}, {"second app", octx}} {
			wantCode(t, c.name, newCustomer(c.ctx, "cus_p_"+c.name[:1], "banned@example.com"), codes.FailedPrecondition, "plans.customer.blocklisted")
		}
		// The app's own list does not show it.
		if l, _ := e.plans.ListBlocklist(ctx, &plansv1.ListBlocklistRequest{}); len(l.GetEntries()) != 3 {
			t.Errorf("the app's list = %v", l.GetEntries())
		}
		// The platform's own tenant makes its SaaS customers with WithoutPlatform: a person the operator banned must not stop the billing of their organisation.
		lists := blocklist.New(store.New(e.appDB), e.system)
		tctx := tenant.NewContext(context.Background(), tenant.Info{ID: uuid.MustParse(live.GetId())})
		check := func(c context.Context) error {
			return store.New(e.appDB).Tx(c, func(q *sqlc.Queries) error { return lists.Check(c, q, blocklist.Subject{Email: "banned@example.com"}) })
		}
		wantCode(t, "checked as a seller's customer", check(tctx), codes.FailedPrecondition, "plans.customer.blocklisted")
		if err := check(blocklist.WithoutPlatform(tctx)); err != nil {
			t.Errorf("the platform's own customers skip the operator's list: %v", err)
		}
		pl, err := e.admin.ListPlatformBlocklist(operator(), &plansv1.ListPlatformBlocklistRequest{})
		if err != nil || len(pl.GetEntries()) < 1 {
			t.Fatalf("platform list = %v, %v", pl, err)
		}
		if _, err := e.admin.RemovePlatformBlocklistEntry(operator(), &plansv1.RemovePlatformBlocklistEntryRequest{Id: added.GetEntry().GetId()}); err != nil {
			t.Fatal(err)
		}
		if err := newCustomer(ctx, "cus_p_after", "banned@example.com"); err != nil {
			t.Fatalf("after the operator took it off: %v", err)
		}
	})
}
