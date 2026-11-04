//go:build integration

package itest

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/secretbox/reseal"
	saascmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/command"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
)

// The four places plans-svc keeps boxes (a webhook endpoint's secret and the previous one it still signs with, a tenant's mail key, a person's two-factor secret), each
// written by the service itself under key 1, are sealed again under key 2 by the job running as the system role (the grants of migration 00015): the tenant tables in
// the schema of the tenant's cell, user_totp in the pooled database. Opening a box the service sealed with the associated data of reseal.Plans is what keeps the two
// from drifting apart: a service that changes its data makes the box unreadable here.
func TestSecretsReseal(t *testing.T) {
	w := startWorld(t, true)
	e := newEnvIn(t, w, saascmd.Config{})
	live := e.account(t, "Reseal")[plansv1.Environment_ENVIRONMENT_LIVE].GetId()
	ctx := as(live)

	made, err := e.hooks.CreateWebhookEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: "https://example.com/hook"})
	if err != nil {
		t.Fatal(err)
	}
	hook := made.GetEndpoint()
	rotated, err := e.hooks.RotateWebhookSecret(ctx, &plansv1.RotateWebhookSecretRequest{Id: hook.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	nafru := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"data":[]}`)) }))
	defer nafru.Close()
	if _, err := e.mailprov.PutMailIntegration(ctx, &plansv1.PutMailIntegrationRequest{Provider: "nafru", BaseUrl: nafru.URL, ApiKey: "nk_good_key_1234", From: "Acme <billing@acme.example>"}); err != nil {
		t.Fatal(err)
	}
	ada := e.verified(t, "ada@reseal.example", "Reseal Co")
	e.enableTwoFactor(t, ada)
	// An organisation's mail account (ADR 0026) is a fifth place; the binding of an app to it holds no box (NULL), so it is no sixth.
	if _, err := e.orgmail.PutOrganisationMailAccount(t.Context(), &plansv1.PutOrganisationMailAccountRequest{UserId: ada.id, AccountId: ada.account, Provider: "nafru", BaseUrl: nafru.URL, ApiKey: "nk_org_key_5678", From: "Acme <billing@acme.example>"}); err != nil {
		t.Fatal(err)
	}

	k1, k2 := make([]byte, 32), bytes.Repeat([]byte{2}, 32)
	ring, _ := secretbox.NewRing(2, map[byte][]byte{1: k1, 2: k2})
	only2, _ := secretbox.NewRing(2, map[byte][]byte{2: k2})
	run := func(dry bool) map[string]reseal.Report {
		t.Helper()
		reports, err := reseal.Run(t.Context(), reseal.Input{Box: ring, Columns: reseal.Plans, Pooled: e.system, Cells: map[string]*pgxpool.Pool{"c1": w.cellSys}, DryRun: dry})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]reseal.Report{}
		for _, r := range reports {
			out[r.Column] = r
		}
		return out
	}

	for col, r := range run(true) {
		if r.Boxes != 1 || r.Current != 0 || r.Resealed != 1 || r.Unreadable != 0 {
			t.Errorf("dry run %s = %+v, want the one box the service wrote, not yet current", col, r)
		}
	}
	for col, r := range run(false) {
		if r.Boxes != 1 || r.Current != 1 || r.Resealed != 1 || r.Unreadable != 0 || r.Changed != 0 {
			t.Errorf("run %s = %+v, want the one box sealed again", col, r)
		}
	}
	for col, r := range run(false) {
		if r.Resealed != 0 || r.Current != 1 {
			t.Errorf("second run %s = %+v: a finished job found work", col, r)
		}
	}

	var secret, previous, mailKey, totp []byte
	if err := e.scan(t.Context(), live, `SELECT secret_enc, prev_secret_enc FROM webhook_endpoints WHERE id = $1`, []any{hook.GetId()}, &secret, &previous); err != nil {
		t.Fatal(err)
	}
	if err := e.scan(t.Context(), live, `SELECT api_key_enc FROM tenant_mail_integrations`, nil, &mailKey); err != nil {
		t.Fatal(err)
	}
	if err := e.system.QueryRow(t.Context(), `SELECT secret_enc FROM user_totp WHERE user_id = $1::uuid`, ada.id).Scan(&totp); err != nil {
		t.Fatal(err)
	}
	aad := webhooksmodel.SecretAAD(uuid.MustParse(hook.GetId()))
	if got, err := only2.Open(secret, aad); err != nil || string(got) != rotated.GetEndpoint().GetSecret() {
		t.Errorf("the endpoint's secret under key 2 = %q, %v", got, err)
	}
	if _, err := only2.Open(previous, aad); err != nil {
		t.Errorf("the previous secret under key 2: %v", err)
	}
	if got, err := only2.Open(mailKey, []byte("mail-integration:"+live)); err != nil || string(got) != "nk_good_key_1234" {
		t.Errorf("the mail key under key 2 = %q, %v", got, err)
	}
	var orgKey []byte
	if err := e.system.QueryRow(t.Context(), `SELECT api_key_enc FROM organisation_mail_accounts WHERE account_id = $1::uuid`, ada.account).Scan(&orgKey); err != nil {
		t.Fatal(err)
	}
	if got, err := only2.Open(orgKey, []byte("org-mail-account:"+ada.account+":nafru")); err != nil || string(got) != "nk_org_key_5678" {
		t.Errorf("the organisation's mail key under key 2 = %q, %v", got, err)
	}
	if len(totp) == 0 || totp[0] != 2 {
		t.Errorf("the two-factor secret starts with %v: it was not sealed again", totp[:1])
	}
}
