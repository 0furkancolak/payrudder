//go:build integration

package itest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	mailqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/mailintegration/query"
)

// testOrganisationMailAccounts covers an e-mail account of an organisation that several of its apps use (plan 10, ADR 0026): owners and administrators write and viewers
// and strangers do not, an app binds to it and holds no credential, the notifier's lookup opens the organisation's key for any bound app (and for a language rule), the
// app cannot change or remove what it inherits, removal is refused with the apps named while any uses it, the box is bound to the organisation and not to an app, and
// the audit trail has the moves without the key or its hint.
func testOrganisationMailAccounts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	nafru := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer nk_org_") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/emails" {
			_, _ = w.Write([]byte(`{"id":"em_org"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer nafru.Close()

	owner := e.verified(t, "owner@orgmail.example", "Org Mail Co")
	admin := e.joinNew(t, owner, "admin@orgmail.example", "admin")
	viewer := e.joinNew(t, owner, "viewer@orgmail.example", "viewer")
	stranger := e.verified(t, "stranger@orgmail.example", "Stranger Mail Co")
	strangerApp, err := e.createApp(stranger, "Elsewhere")
	if err != nil {
		t.Fatal(err)
	}

	liveOf := func(app *plansv1.App) string {
		t.Helper()
		for _, tn := range app.GetTenants() {
			if tn.GetEnvironment() == plansv1.Environment_ENVIRONMENT_LIVE {
				return tn.GetId()
			}
		}
		t.Fatal("no live tenant")
		return ""
	}
	mkApp := func(name string) (id, live string) {
		t.Helper()
		r, err := e.createApp(owner, name)
		if err != nil {
			t.Fatal(err)
		}
		return r.GetApp().GetId(), liveOf(r.GetApp())
	}
	appA, liveA := mkApp("Shop A")
	appB, liveB := mkApp("Shop B")

	put := func(by person, label, key string) (*plansv1.PutOrganisationMailAccountResponse, error) {
		return e.orgmail.PutOrganisationMailAccount(ctx, &plansv1.PutOrganisationMailAccountRequest{UserId: by.id, AccountId: owner.account, Provider: "nafru", Label: label,
			BaseUrl: nafru.URL, ApiKey: "nk_org_" + key, From: "Acme <billing@acme.example>"})
	}
	bind := func(by person, account, app string, makeDefault bool) error {
		_, err := e.orgmail.BindOrganisationMailAccount(ctx, &plansv1.BindOrganisationMailAccountRequest{UserId: by.id, AccountId: owner.account, Account: account, AppId: app, MakeDefault: makeDefault})
		return err
	}
	unbind := func(by person, account, app string) error {
		_, err := e.orgmail.UnbindOrganisationMailAccount(ctx, &plansv1.UnbindOrganisationMailAccountRequest{UserId: by.id, AccountId: owner.account, Account: account, AppId: app})
		return err
	}
	list := func(by person) *plansv1.ListOrganisationMailAccountsResponse {
		t.Helper()
		r, err := e.orgmail.ListOrganisationMailAccounts(ctx, &plansv1.ListOrganisationMailAccountsRequest{UserId: by.id, AccountId: owner.account})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	resolve := func(tenantID, language string) *plansv1.ResolveMailSenderResponse {
		t.Helper()
		r, err := e.mailprov.ResolveMailSender(as(tenantID), &plansv1.ResolveMailSenderRequest{Language: language})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	// Who may write: owners and administrators; a viewer reads; a stranger finds no organisation.
	_, err = put(viewer, "eu", "SECRETKEY9")
	wantCode(t, "a viewer connects", err, codes.PermissionDenied, "identity.account.forbidden")
	_, err = put(stranger, "eu", "SECRETKEY9")
	wantCode(t, "a stranger connects", err, codes.NotFound, "identity.account.not_found")
	_, err = put(admin, "EU", "SECRETKEY9")
	wantCode(t, "a label in capitals", err, codes.InvalidArgument, "")
	made, err := put(admin, "eu", "SECRETKEY9")
	if err != nil || made.GetAccount().GetAccount() != "nafru~eu" || made.GetAccount().GetKeyHint() != "KEY9" {
		t.Fatalf("an administrator connects: %v, %v", made, err)
	}
	if got := list(viewer); len(got.GetAccounts()) != 1 || got.GetCanWrite() || !list(admin).GetCanWrite() || len(got.GetAccounts()[0].GetUsedBy()) != 0 {
		t.Errorf("a viewer reads: %v", got)
	}
	_, err = e.orgmail.ListOrganisationMailAccounts(ctx, &plansv1.ListOrganisationMailAccountsRequest{UserId: stranger.id, AccountId: owner.account})
	wantCode(t, "a stranger lists", err, codes.NotFound, "identity.account.not_found")
	wantCode(t, "a viewer binds", bind(viewer, "nafru~eu", appA, true), codes.PermissionDenied, "identity.account.forbidden")
	wantCode(t, "an app of another organisation", bind(owner, "nafru~eu", strangerApp.GetApp().GetId(), true), codes.NotFound, "identity.app.not_found")
	wantCode(t, "an account that is not connected", bind(owner, "nafru~nope", appA, true), codes.NotFound, "plans.mail_integration.not_connected")
	wantCode(t, "no account named", bind(owner, "", appA, true), codes.InvalidArgument, "")

	// Two apps use it. The app sees it as inherited, with no credential; the notifier's lookup opens the organisation's key for either.
	if err := bind(admin, "nafru~eu", appA, false); err != nil {
		t.Fatal(err)
	}
	if err := bind(admin, "nafru~eu", appB, true); err != nil {
		t.Fatal(err)
	}
	if err := bind(admin, "nafru~eu", appB, true); err != nil {
		t.Errorf("binding twice is not an error: %v", err)
	}
	got, err := e.mailprov.GetMailIntegration(as(liveA), &plansv1.GetMailIntegrationRequest{})
	if err != nil || len(got.GetAccounts()) != 1 {
		t.Fatalf("app A: %v, %v", got, err)
	}
	if a := got.GetAccounts()[0]; a.GetAccount() != "nafru~eu" || !a.GetInherited() || !a.GetIsDefault() || a.GetBaseUrl() != "" || a.GetFrom() != "" || a.GetKeyHint() != "" {
		t.Errorf("app A sees the shared account as %v (the first account is the default whatever was asked; nothing of the credential)", a)
	}
	for _, tn := range []string{liveA, liveB} {
		if r := resolve(tn, "de"); !r.GetConnected() || r.GetAccount() != "nafru~eu" || r.GetApiKey() != "nk_org_SECRETKEY9" || r.GetFrom() != "Acme <billing@acme.example>" {
			t.Errorf("tenant %s resolves %v", tn, r)
		}
	}
	if used := list(owner).GetAccounts()[0].GetUsedBy(); len(used) != 2 || used[0].GetName() != "Shop A" || used[0].GetIsDefault() != true || used[1].GetName() != "Shop B" {
		t.Errorf("used_by = %v", used)
	}

	// The credential is not in the app: its row holds no box, and the organisation's box opens for the organisation and the key only.
	for _, tn := range []string{liveA, liveB} {
		var nulls int
		if err := e.scan(ctx, tn, `SELECT count(*) FROM tenant_mail_integrations WHERE shared_account_id IS NOT NULL AND api_key_enc IS NULL AND base_url = '' AND key_hint = '' AND tenant_id = $1::uuid`, []any{tn}, &nulls); err != nil || nulls != 1 {
			t.Errorf("tenant %s: bindings with no credential = %d, %v", tn, nulls, err)
		}
	}
	var box []byte
	if err := e.system.QueryRow(ctx, `SELECT api_key_enc FROM organisation_mail_accounts WHERE account_id = $1::uuid AND provider = 'nafru~eu'`, owner.account).Scan(&box); err != nil {
		t.Fatal(err)
	}
	if b, err := e.box.Open(box, mailqry.SharedAAD(owner.account, "nafru~eu")); err != nil || string(b) != "nk_org_SECRETKEY9" {
		t.Errorf("the box under the organisation's data = %q, %v", b, err)
	}
	for _, wrong := range [][]byte{mailqry.AAD(liveA, "nafru~eu"), mailqry.SharedAAD(owner.account, "nafru"), mailqry.SharedAAD(stranger.account, "nafru~eu")} {
		if _, err := e.box.Open(box, wrong); err == nil {
			t.Errorf("the box opened under %q", wrong)
		}
	}

	// What it inherits, an app cannot change; an own account with the same key cannot be made, nor bound beside it.
	_, err = e.mailprov.DeleteMailIntegration(as(liveA), &plansv1.DeleteMailIntegrationRequest{Account: "nafru~eu"})
	wantCode(t, "an app removes the inherited account", err, codes.FailedPrecondition, "plans.mail_integration.inherited")
	_, err = e.mailprov.PutMailIntegration(as(liveA), &plansv1.PutMailIntegrationRequest{Provider: "nafru", Label: "eu", BaseUrl: nafru.URL, ApiKey: "nk_org_own", From: "A <a@acme.example>"})
	wantCode(t, "an app overwrites the inherited account", err, codes.FailedPrecondition, "plans.mail_integration.inherited")
	if _, err := e.mailprov.PutMailIntegration(as(liveA), &plansv1.PutMailIntegrationRequest{Provider: "nafru", Label: "own", BaseUrl: nafru.URL, ApiKey: "nk_org_own", From: "A <a@acme.example>"}); err != nil {
		t.Fatal(err)
	}
	if _, err := put(owner, "own", "ORGOWN"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "binding where the app has its own account with that key", bind(owner, "nafru~own", appA, false), codes.AlreadyExists, "plans.mail_integration.account_exists")

	// The app's own routing works on it: German mail goes through its own account, everything else through the inherited default.
	if _, err := e.mailprov.PutMailRouting(as(liveA), &plansv1.PutMailRoutingRequest{Rules: []*plansv1.MailRoutingRule{{Language: "de", Account: "nafru~own"}, {Language: "fr", Account: "nafru~eu"}}}); err != nil {
		t.Fatal(err)
	}
	if r := resolve(liveA, "de"); r.GetAccount() != "nafru~own" || r.GetApiKey() != "nk_org_own" {
		t.Errorf("de: %v", r)
	}
	if r := resolve(liveA, "fr"); r.GetAccount() != "nafru~eu" || r.GetApiKey() != "nk_org_SECRETKEY9" {
		t.Errorf("fr goes through the inherited account by a rule: %v", r)
	}
	if r := resolve(liveA, "tr"); r.GetAccount() != "nafru~eu" {
		t.Errorf("tr takes the default, the inherited account: %v", r)
	}
	// The inherited default cannot be unbound while the app has others; once it is not the default it can, and its rule goes with it.
	wantCode(t, "unbinding the default", unbind(owner, "nafru~eu", appA), codes.FailedPrecondition, "plans.mail_integration.default_in_use")

	// Removal is refused while apps use it, and names them.
	_, err = e.orgmail.DeleteOrganisationMailAccount(ctx, &plansv1.DeleteOrganisationMailAccountRequest{UserId: owner.id, AccountId: owner.account, Account: "nafru~eu"})
	wantCode(t, "removing a used account", err, codes.FailedPrecondition, "plans.mail_integration.shared_in_use")
	if err == nil || !strings.Contains(err.Error(), "Shop A") || !strings.Contains(err.Error(), "Shop B") {
		t.Errorf("the refusal does not name the apps: %v", err)
	}
	_, err = e.orgmail.DeleteOrganisationMailAccount(ctx, &plansv1.DeleteOrganisationMailAccountRequest{UserId: viewer.id, AccountId: owner.account, Account: "nafru~own"})
	wantCode(t, "a viewer removes", err, codes.PermissionDenied, "identity.account.forbidden")
	if _, err := e.mailprov.SetDefaultMailIntegration(as(liveA), &plansv1.SetDefaultMailIntegrationRequest{Account: "nafru~own"}); err != nil {
		t.Fatal(err)
	}
	if err := unbind(owner, "nafru~eu", appA); err != nil {
		t.Fatal(err)
	}
	if rules, _ := e.mailprov.GetMailRouting(as(liveA), &plansv1.GetMailRoutingRequest{}); len(rules.GetRules()) != 1 || rules.GetRules()[0].GetLanguage() != "de" {
		t.Errorf("the rule that named the unbound account is still there: %v", rules.GetRules())
	}
	wantCode(t, "unbinding what is not bound", unbind(owner, "nafru~eu", appA), codes.NotFound, "plans.mail_integration.not_connected")
	_, err = e.orgmail.DeleteOrganisationMailAccount(ctx, &plansv1.DeleteOrganisationMailAccountRequest{UserId: owner.id, AccountId: owner.account, Account: "nafru~eu"})
	wantCode(t, "removing while one app is left", err, codes.FailedPrecondition, "plans.mail_integration.shared_in_use")
	if err == nil || strings.Contains(err.Error(), "Shop A") || !strings.Contains(err.Error(), "Shop B") {
		t.Errorf("only the app that still uses it is named: %v", err)
	}

	// A test names its account and stores what it found.
	tested, err := e.orgmail.TestOrganisationMailAccount(ctx, &plansv1.TestOrganisationMailAccountRequest{UserId: admin.id, AccountId: owner.account, Account: "nafru~eu", To: "owner@orgmail.example"})
	if err != nil || !tested.GetOk() || tested.GetMessageId() != "em_org" {
		t.Fatalf("test: %v, %v", tested, err)
	}

	// With the last app gone the account is removed, and the mail of that app goes through the sender it has left.
	if err := unbind(owner, "nafru~eu", appB); err != nil {
		t.Fatalf("an app's only account can be unbound: %v", err)
	}
	if r := resolve(liveB, "de"); r.GetConnected() {
		t.Errorf("app B has no account left: %v", r)
	}
	if _, err := e.orgmail.DeleteOrganisationMailAccount(ctx, &plansv1.DeleteOrganisationMailAccountRequest{UserId: admin.id, AccountId: owner.account, Account: "nafru~eu"}); err != nil {
		t.Fatal(err)
	}
	if got := list(owner); len(got.GetAccounts()) != 1 || got.GetAccounts()[0].GetAccount() != "nafru~own" {
		t.Errorf("accounts after the removal = %v", got.GetAccounts())
	}

	// The audit trail of the organisation and of the app has the moves, and never the key or its hint.
	seen := map[string]int{}
	count := func(rows pgx.Rows) {
		defer rows.Close()
		for rows.Next() {
			var action, text string
			if err := rows.Scan(&action, &text); err != nil {
				t.Fatal(err)
			}
			seen[action]++
			if strings.Contains(text, "SECRETKEY9") || strings.Contains(text, "KEY9") {
				t.Errorf("the key or its hint is in the audit trail: %s", text)
			}
		}
	}
	// The organisation's own entries are in the pooled database; an app's are where the app's rows are.
	rows, err := e.system.Query(ctx, `SELECT action, summary || detail::text FROM audit_events WHERE account_id = $1::uuid AND tenant_id IS NULL AND action LIKE 'mail_integration.org_%'`, owner.account)
	if err != nil {
		t.Fatal(err)
	}
	count(rows)
	for _, tn := range []string{liveA, liveB} {
		if err := e.tx(ctx, tn, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT action, summary || detail::text FROM audit_events WHERE tenant_id = $1::uuid AND account_id = $2::uuid AND action LIKE 'mail_integration.org_%'`, tn, owner.account)
			if err == nil {
				count(rows)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	// saved: 2 (eu, own); bound: the two apps (the repeat binds nothing, the refused ones nothing); unbound: the two apps; removed: 1. The entries of an app carry the
	// organisation's id too, so the organisation's trail has them.
	if seen["mail_integration.org_saved"] != 2 || seen["mail_integration.org_bound"] != 2 || seen["mail_integration.org_unbound"] != 2 || seen["mail_integration.org_removed"] != 1 {
		t.Errorf("audit actions = %v", seen)
	}
}
