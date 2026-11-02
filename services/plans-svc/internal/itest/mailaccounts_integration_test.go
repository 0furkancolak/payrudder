//go:build integration

package itest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	auditv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/audit/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// testMailAccounts covers several e-mail accounts of one tenant (plan 10): the first is the default, a labelled one is added beside it, the default moves without a
// key, a rule sends the mail of a language through another account, the notifier's lookup follows the language, the default cannot be removed while others exist,
// a rule dies with its account, another tenant sees none of it, and the audit trail has the moves without a key.
func testMailAccounts(t *testing.T) {
	e := newEnv(t)
	accounts := e.account(t, "Mail Accounts")
	live := accounts[plansv1.Environment_ENVIRONMENT_LIVE]
	other := e.account(t, "Mail Accounts Other")[plansv1.Environment_ENVIRONMENT_LIVE]

	nafru := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer nk_acct_") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/emails" {
			_, _ = w.Write([]byte(`{"id":"em_` + r.Header.Get("Authorization")[len("Bearer nk_acct_"):] + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer nafru.Close()
	put := func(label, key, from string, makeDefault bool) *plansv1.PutMailIntegrationRequest {
		return &plansv1.PutMailIntegrationRequest{Provider: "nafru", Label: label, MakeDefault: makeDefault, BaseUrl: nafru.URL, ApiKey: "nk_acct_" + key, From: from}
	}
	list := func() []*plansv1.MailIntegration {
		t.Helper()
		got, err := e.mailprov.GetMailIntegration(as(live.GetId()), &plansv1.GetMailIntegrationRequest{})
		if err != nil {
			t.Fatal(err)
		}
		return got.GetAccounts()
	}
	defaultOf := func() string {
		t.Helper()
		for _, a := range list() {
			if a.GetIsDefault() {
				return a.GetAccount()
			}
		}
		return ""
	}
	resolve := func(tenantID, language string) *plansv1.ResolveMailSenderResponse {
		t.Helper()
		r, err := e.mailprov.ResolveMailSender(as(tenantID), &plansv1.ResolveMailSenderRequest{Language: language})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	// The first account is the default whatever the request says; a labelled one is added beside it and is not.
	if _, err := e.mailprov.PutMailIntegration(as(live.GetId()), put("", "main", "Acme <billing@acme.example>", false)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mailprov.PutMailIntegration(as(live.GetId()), put("eu", "eu", "Acme <rechnung@acme.eu>", false)); err != nil {
		t.Fatal(err)
	}
	if got := list(); len(got) != 2 || got[0].GetAccount() != "nafru" || !got[0].GetIsDefault() || got[1].GetAccount() != "nafru~eu" || got[1].GetIsDefault() || got[1].GetLabel() != "eu" || got[1].GetProvider() != "nafru" {
		t.Fatalf("accounts = %v", got)
	}
	_, err := e.mailprov.PutMailIntegration(as(live.GetId()), put("EU", "x", "Acme <a@acme.eu>", false))
	wantCode(t, "a label in capitals", err, codes.InvalidArgument, "")

	// With no rule every language takes the default; a rule moves one language.
	if r := resolve(live.GetId(), "de"); r.GetAccount() != "nafru" || r.GetApiKey() != "nk_acct_main" || r.GetFrom() != "Acme <billing@acme.example>" {
		t.Fatalf("no rule: %v", r)
	}
	_, err = e.mailprov.PutMailRouting(as(live.GetId()), &plansv1.PutMailRoutingRequest{Rules: []*plansv1.MailRoutingRule{{Language: "de", Account: "nafru~nope"}}})
	wantCode(t, "a rule naming an account that is not connected", err, codes.InvalidArgument, "plans.mail_integration.unknown_account")
	_, err = e.mailprov.PutMailRouting(as(live.GetId()), &plansv1.PutMailRoutingRequest{Rules: []*plansv1.MailRoutingRule{{Language: "xx", Account: "nafru~eu"}}})
	wantCode(t, "a language outside the six", err, codes.InvalidArgument, "")
	_, err = e.mailprov.PutMailRouting(as(live.GetId()), &plansv1.PutMailRoutingRequest{Rules: []*plansv1.MailRoutingRule{{Language: "de", Account: "nafru~eu"}, {Language: "de", Account: "nafru"}}})
	wantCode(t, "a language twice", err, codes.InvalidArgument, "")
	if _, err := e.mailprov.PutMailRouting(as(live.GetId()), &plansv1.PutMailRoutingRequest{Rules: []*plansv1.MailRoutingRule{{Language: "fr", Account: "nafru~eu"}, {Language: "de", Account: "nafru~eu"}}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.mailprov.GetMailRouting(as(live.GetId()), &plansv1.GetMailRoutingRequest{}); len(got.GetRules()) != 2 || got.GetRules()[0].GetLanguage() != "de" || got.GetRules()[1].GetLanguage() != "fr" {
		t.Errorf("rules = %v (in the order of the languages)", got.GetRules())
	}
	for lang, want := range map[string]string{"de": "nafru~eu", "fr": "nafru~eu", "en": "nafru", "": "nafru", "tr": "nafru", "ar": "nafru"} {
		r := resolve(live.GetId(), lang)
		if r.GetAccount() != want {
			t.Errorf("a mail in %q goes through %q, want %q", lang, r.GetAccount(), want)
		}
		if want == "nafru~eu" && (r.GetApiKey() != "nk_acct_eu" || r.GetFrom() != "Acme <rechnung@acme.eu>") {
			t.Errorf("the labelled account's own key and sender are used: %v", r)
		}
	}
	// Another tenant sees none of it.
	if r := resolve(other.GetId(), "de"); r.GetConnected() || r.GetApiKey() != "" {
		t.Errorf("another tenant: %v", r)
	}
	if got, _ := e.mailprov.GetMailRouting(as(other.GetId()), &plansv1.GetMailRoutingRequest{}); len(got.GetRules()) != 0 {
		t.Errorf("another tenant's rules: %v", got.GetRules())
	}

	// A test names its account and records what it found there only.
	tested, err := e.mailprov.TestMailIntegration(as(live.GetId()), &plansv1.TestMailIntegrationRequest{To: "owner@example.com", Account: "nafru~eu"})
	if err != nil || !tested.GetOk() || tested.GetMessageId() != "em_eu" {
		t.Fatalf("test of the labelled account: %v %v", tested, err)
	}
	_, err = e.mailprov.TestMailIntegration(as(live.GetId()), &plansv1.TestMailIntegrationRequest{To: "owner@example.com", Account: "nafru~gone"})
	wantCode(t, "a test of an account that is not connected", err, codes.NotFound, "plans.mail_integration.not_connected")

	// The default moves with no key, and a replace keeps the place of an account.
	_, err = e.mailprov.SetDefaultMailIntegration(as(live.GetId()), &plansv1.SetDefaultMailIntegrationRequest{Account: "nafru~gone"})
	wantCode(t, "a default that is not connected", err, codes.NotFound, "plans.mail_integration.not_connected")
	_, err = e.mailprov.SetDefaultMailIntegration(as(live.GetId()), &plansv1.SetDefaultMailIntegrationRequest{})
	wantCode(t, "a default with no account", err, codes.InvalidArgument, "")
	if _, err := e.mailprov.SetDefaultMailIntegration(as(live.GetId()), &plansv1.SetDefaultMailIntegrationRequest{Account: "nafru~eu"}); err != nil {
		t.Fatal(err)
	}
	if defaultOf() != "nafru~eu" || resolve(live.GetId(), "en").GetAccount() != "nafru~eu" {
		t.Errorf("the default is %q after it moved", defaultOf())
	}
	if _, err := e.mailprov.PutMailIntegration(as(live.GetId()), put("", "main2", "Acme <billing@acme.example>", false)); err != nil || defaultOf() != "nafru~eu" {
		t.Errorf("replacing the primary account moved the default to %q (%v)", defaultOf(), err)
	}
	if r := resolve(live.GetId(), "tr"); r.GetAccount() != "nafru~eu" {
		t.Errorf("a language with no rule takes the new default: %v", r)
	}
	if _, err := e.mailprov.PutMailIntegration(as(live.GetId()), put("", "main3", "Acme <billing@acme.example>", true)); err != nil || defaultOf() != "nafru" {
		t.Errorf("make_default on a replace: the default is %q (%v)", defaultOf(), err)
	}

	// The default is not removed while there are others; a removed account takes its rules with it.
	_, err = e.mailprov.DeleteMailIntegration(as(live.GetId()), &plansv1.DeleteMailIntegrationRequest{})
	wantCode(t, "removing the default while another exists", err, codes.FailedPrecondition, "plans.mail_integration.default_in_use")
	_, err = e.mailprov.DeleteMailIntegration(as(live.GetId()), &plansv1.DeleteMailIntegrationRequest{Account: "nafru"})
	wantCode(t, "naming the default", err, codes.FailedPrecondition, "plans.mail_integration.default_in_use")
	if _, err := e.mailprov.DeleteMailIntegration(as(live.GetId()), &plansv1.DeleteMailIntegrationRequest{Account: "nafru~eu"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.mailprov.GetMailRouting(as(live.GetId()), &plansv1.GetMailRoutingRequest{}); len(got.GetRules()) != 0 {
		t.Errorf("rules that named the removed account are still there: %v", got.GetRules())
	}
	if r := resolve(live.GetId(), "de"); r.GetAccount() != "nafru" {
		t.Errorf("after the removal a German mail goes through %q", r.GetAccount())
	}
	_, err = e.mailprov.DeleteMailIntegration(as(live.GetId()), &plansv1.DeleteMailIntegrationRequest{Account: "nafru~eu"})
	wantCode(t, "removing it twice", err, codes.NotFound, "plans.mail_integration.not_connected")
	if _, err := e.mailprov.DeleteMailIntegration(as(live.GetId()), &plansv1.DeleteMailIntegrationRequest{}); err != nil {
		t.Fatalf("the last account, named by nobody: %v", err)
	}
	if r := resolve(live.GetId(), "de"); r.GetConnected() {
		t.Errorf("with no account left the mail goes through the built-in sender: %v", r)
	}

	// The audit trail has the moves, and no key or rule detail beyond the languages.
	got := e.events(t, as(live.GetId()), &auditv1.ListAuditEventsRequest{Actions: []string{"mail_integration.*"}, PageSize: 100})
	seen := map[string]int{}
	for _, ev := range got {
		seen[ev.GetAction()]++
		if strings.Contains(ev.GetDetailJson(), "nk_acct_") {
			t.Errorf("a key is in the audit trail: %s", ev.GetDetailJson())
		}
	}
	if seen["mail_integration.saved"] != 4 || seen["mail_integration.default_set"] != 1 || seen["mail_integration.routing_changed"] != 1 || seen["mail_integration.removed"] != 2 {
		t.Errorf("audit actions = %v", seen)
	}
}
