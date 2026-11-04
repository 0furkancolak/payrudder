package query

import (
	"bytes"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/secretbox/reseal"
)

// The key of an account is sealed with AAD and the reseal job opens it with its own copy of the rule (pkg/secretbox/reseal/places.go): a drift between the two makes
// a key rotation fail on exactly the accounts that have a label. The primary account keeps the form every key was sealed with before labels.
func TestAADIsTheOneTheResealJobUses(t *testing.T) {
	var col *reseal.Column
	for i := range reseal.Plans {
		if reseal.Plans[i].Table == "tenant_mail_integrations" {
			col = &reseal.Plans[i]
		}
	}
	if col == nil {
		t.Fatal("the reseal job does not know the mail keys")
	}
	if len(col.Key) != 2 || col.Key[0].Name != "tenant_id" || col.Key[1].Name != "provider" {
		t.Fatalf("the job finds a row by %v, the table's key is (tenant_id, provider)", col.Key)
	}
	const tenant = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	for _, account := range []string{"nafru", "nafru~eu", "nafru~us-east"} {
		if got, want := col.AAD([]string{tenant, account}), AAD(tenant, account); !bytes.Equal(got, want) {
			t.Errorf("account %q: the job opens with %q, the service seals with %q", account, got, want)
		}
	}
	if got := string(AAD(tenant, "nafru")); got != "mail-integration:"+tenant {
		t.Errorf("the primary account's AAD = %q, the form of the keys sealed before labels", got)
	}
	if bytes.Equal(AAD(tenant, "nafru~eu"), AAD(tenant, "nafru~us")) {
		t.Error("two accounts of one tenant share an AAD: a key copied between them would open")
	}
}

// An organisation's key is sealed to the organisation and the account key and to no app, and the job opens it with its own copy of that rule.
func TestSharedAADNamesTheOrganisationAndNoApp(t *testing.T) {
	var col *reseal.Column
	for i := range reseal.Plans {
		if reseal.Plans[i].Table == "organisation_mail_accounts" {
			col = &reseal.Plans[i]
		}
	}
	if col == nil || col.Tenant {
		t.Fatal("the reseal job does not know the organisation's mail keys, or treats them as an app's")
	}
	if len(col.Key) != 2 || col.Key[0].Name != "account_id" || col.Key[1].Name != "provider" {
		t.Fatalf("the job finds a row by %v, the table's key is (account_id, provider)", col.Key)
	}
	const org = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	for _, account := range []string{"nafru", "nafru~eu"} {
		if got, want := col.AAD([]string{org, account}), SharedAAD(org, account); !bytes.Equal(got, want) {
			t.Errorf("account %q: the job opens with %q, the service seals with %q", account, got, want)
		}
	}
	if bytes.Equal(SharedAAD(org, "nafru"), SharedAAD(org, "nafru~eu")) || bytes.Equal(SharedAAD(org, "nafru"), AAD(org, "nafru")) {
		t.Error("two accounts, or an organisation and an app, share an AAD")
	}
}
