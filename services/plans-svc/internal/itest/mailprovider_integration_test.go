//go:build integration

package itest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
)

// testMailProvider covers the tenant's own e-mail provider: only a live tenant has one, the provider must accept the key before it is stored, the key is
// sealed and never read back, a test sends through it, and the notifier's lookup gets the key in plaintext for that tenant only.
func testMailProvider(t *testing.T) {
	e := newEnv(t)
	accounts := e.account(t, "Mail Provider")
	live, sandbox := accounts[plansv1.Environment_ENVIRONMENT_LIVE], accounts[plansv1.Environment_ENVIRONMENT_SANDBOX]
	other := e.account(t, "Mail Provider Other")[plansv1.Environment_ENVIRONMENT_LIVE]

	var mu sync.Mutex
	var sent []string
	nafru := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer nk_good_key_1234" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"invalid key"}`))
			return
		}
		switch r.URL.Path {
		case "/domains":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/emails":
			mu.Lock()
			sent = append(sent, r.Header.Get("Idempotency-Key")+"|"+r.URL.Path)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"id":"em_1"}`))
		}
	}))
	defer nafru.Close()
	put := func(key string) *plansv1.PutMailIntegrationRequest {
		return &plansv1.PutMailIntegrationRequest{Provider: "nafru", BaseUrl: nafru.URL, ApiKey: key, From: "Acme <billing@acme.example>"}
	}

	if got, err := e.mailprov.GetMailIntegration(as(live.GetId()), &plansv1.GetMailIntegrationRequest{}); err != nil || got.GetConnected() {
		t.Fatalf("a new tenant has no provider: %v %v", got, err)
	}
	_, err := e.mailprov.PutMailIntegration(as(sandbox.GetId()), put("nk_good_key_1234"))
	wantCode(t, "a sandbox tenant", err, codes.FailedPrecondition, "plans.mail_integration.live_only")
	_, err = e.mailprov.PutMailIntegration(as(live.GetId()), put("nk_wrong"))
	wantCode(t, "a key the provider refuses", err, codes.InvalidArgument, "plans.mail_integration.refused")
	if got, _ := e.mailprov.GetMailIntegration(as(live.GetId()), &plansv1.GetMailIntegrationRequest{}); got.GetConnected() {
		t.Error("a refused key was stored")
	}
	bad := put("nk_good_key_1234")
	bad.Provider = "sendgrid"
	_, err = e.mailprov.PutMailIntegration(as(live.GetId()), bad)
	wantCode(t, "an unknown provider", err, codes.InvalidArgument, "")
	bad = put("nk_good_key_1234")
	bad.From = "not an address"
	_, err = e.mailprov.PutMailIntegration(as(live.GetId()), bad)
	wantCode(t, "a bad sender", err, codes.InvalidArgument, "")
	bad = put("nk_good_key_1234")
	bad.BaseUrl = "ftp://x"
	_, err = e.mailprov.PutMailIntegration(as(live.GetId()), bad)
	wantCode(t, "a bad base URL", err, codes.InvalidArgument, "")

	saved, err := e.mailprov.PutMailIntegration(as(live.GetId()), put("nk_good_key_1234"))
	if err != nil || saved.GetIntegration().GetKeyHint() != "1234" || saved.GetIntegration().GetCheckedTime() == nil {
		t.Fatalf("put: %v %v", saved, err)
	}
	got, err := e.mailprov.GetMailIntegration(as(live.GetId()), &plansv1.GetMailIntegrationRequest{})
	if err != nil || !got.GetConnected() || strings.Contains(got.String(), "nk_good_key") {
		t.Fatalf("get: %v %v (the key must never be read back)", got, err)
	}
	var stored []byte
	liveID, _ := uuid.Parse(live.GetId())
	err = pgxx.InTenantTx(context.Background(), e.appDB, liveID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT api_key_enc FROM tenant_mail_integrations`).Scan(&stored)
	})
	if err != nil || strings.Contains(string(stored), "nk_good_key") || len(stored) == 0 {
		t.Fatalf("the key must be sealed at rest: %v", err)
	}

	r, err := e.mailprov.ResolveMailSender(as(live.GetId()), &plansv1.ResolveMailSenderRequest{})
	if err != nil || !r.GetConnected() || r.GetApiKey() != "nk_good_key_1234" || r.GetBaseUrl() != nafru.URL || r.GetFrom() != "Acme <billing@acme.example>" {
		t.Fatalf("resolve: %v %v", r, err)
	}
	if o, err := e.mailprov.ResolveMailSender(as(other.GetId()), &plansv1.ResolveMailSenderRequest{}); err != nil || o.GetConnected() || o.GetApiKey() != "" {
		t.Fatalf("another tenant must not see it: %v %v", o, err)
	}

	tested, err := e.mailprov.TestMailIntegration(as(live.GetId()), &plansv1.TestMailIntegrationRequest{To: "owner@example.com"})
	if err != nil || !tested.GetOk() || tested.GetMessageId() != "em_1" {
		t.Fatalf("test: %v %v", tested, err)
	}
	mu.Lock()
	if len(sent) != 1 {
		t.Errorf("the provider was asked to send %d mails, want 1", len(sent))
	}
	mu.Unlock()
	_, err = e.mailprov.TestMailIntegration(as(live.GetId()), &plansv1.TestMailIntegrationRequest{To: "nope"})
	wantCode(t, "a bad recipient", err, codes.InvalidArgument, "")

	// The provider revokes the key: the test says so, and the status keeps it.
	nafru.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	tested, err = e.mailprov.TestMailIntegration(as(live.GetId()), &plansv1.TestMailIntegrationRequest{To: "owner@example.com"})
	if err != nil || tested.GetOk() || tested.GetProblem() != "refused" {
		t.Fatalf("a revoked key: %v %v", tested, err)
	}
	if got, _ := e.mailprov.GetMailIntegration(as(live.GetId()), &plansv1.GetMailIntegrationRequest{}); got.GetIntegration().GetCheckProblem() != "refused" {
		t.Errorf("status = %v", got)
	}

	if _, err := e.mailprov.DeleteMailIntegration(as(live.GetId()), &plansv1.DeleteMailIntegrationRequest{}); err != nil {
		t.Fatal(err)
	}
	_, err = e.mailprov.DeleteMailIntegration(as(live.GetId()), &plansv1.DeleteMailIntegrationRequest{})
	wantCode(t, "delete twice", err, codes.NotFound, "plans.mail_integration.not_connected")
	if r, _ := e.mailprov.ResolveMailSender(as(live.GetId()), &plansv1.ResolveMailSenderRequest{}); r.GetConnected() {
		t.Error("a disconnected tenant still resolves")
	}
}
