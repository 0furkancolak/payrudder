//go:build integration

package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	pkg "github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/mock"
	"github.com/0furkancolak/payrudder/pkg/einvoice/nilvera"
	einvoicewebhook "github.com/0furkancolak/payrudder/pkg/einvoice/webhook"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	einvoiceworker "github.com/0furkancolak/payrudder/services/payments-worker/internal/einvoice"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
)

// Two integrator accounts of one tenant: a default and one that takes the buyers of Germany. Documents go to the account routing names, the default moves without
// resending the settings, the default cannot be removed while others exist, a cancel goes through the account that issued, and every change is in the audit trail
// without a setting in it.
func TestEInvoiceAccountsRouteByCountry(t *testing.T) {
	pg := testkit.StartPostgres(t)
	cdb := openDB(t, pg)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, _ := secretbox.New(bytes.Repeat([]byte{9}, 32))
	registry := pkg.NewRegistry()
	mock.Register(registry)
	nilvera.RegisterWithOptions(registry, nilvera.Options{AllowPrivate: true})
	einvoicewebhook.RegisterWithOptions(registry, einvoicewebhook.Options{AllowPrivate: true})
	accounts := store.EInvoiceAccounts{DB: cdb.DB, Box: box}
	h := &einvoiceworker.Handler{Registry: registry, Accounts: accounts, Issuances: store.EInvoiceIssuances{DB: cdb.DB}, Blobs: &memBlobs{}, Log: log}

	api := httptest.NewServer((&webhook.Server{Registry: payments.NewRegistry(), Accounts: store.Accounts{DB: cdb.DB, Box: box}, EInvoiceAccounts: accounts, EInvoiceRegistry: registry, Log: log, Token: "internal"}).Handler())
	defer api.Close()
	do := func(method, path, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		req.Header.Set("X-Internal-Token", "internal")
		req.Header.Set("X-Payrudder-Actor", "user:ada")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	tenant := uuid.New()
	base := "/admin/tenants/" + tenant.String() + "/e-invoice-accounts"
	main, german := newIntegrator(t, "whsec_main"), newIntegrator(t, "whsec_german")
	settings := func(in *integrator, secret string) []byte {
		return []byte(`{"url":"` + in.srv.URL + `","secret":"` + secret + `","allow_private":true}`)
	}
	if err := accounts.Put(ctx, tenant, "live", "webhook", "production", settings(main, "whsec_main"), false, store.Audit{Actor: "user:ada"}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.Put(ctx, tenant, "live", "webhook~de", "production", settings(german, "whsec_german"), false, store.Audit{Actor: "user:ada"}); err != nil {
		t.Fatal(err)
	}
	// A labelled account through the API: the label is part of the key, the credentials are sealed and never come back.
	if code, body := do("PUT", base+"/live", `{"provider":"nilvera","label":"tr","mode":"test","credentials":{"api_key":"sk_secret_value"}}`); code != 200 || strings.Contains(body, "sk_secret_value") || !strings.Contains(body, `"account":"nilvera~tr"`) {
		t.Fatalf("put labelled = %d %s", code, body)
	}

	var list struct {
		Accounts []store.EInvoiceAccountInfo `json:"accounts"`
	}
	code, body := do("GET", base, "")
	if err := json.Unmarshal([]byte(body), &list); code != 200 || err != nil || len(list.Accounts) != 3 || strings.Contains(body, "sk_secret_value") {
		t.Fatalf("list = %d %s", code, body)
	}
	defaults := map[string]bool{}
	for _, a := range list.Accounts {
		defaults[a.Account] = a.IsDefault
		if a.Account == "nilvera~tr" && (a.Provider != "nilvera" || a.Label != "tr") {
			t.Errorf("labelled account = %+v", a)
		}
	}
	if !defaults["webhook"] || defaults["webhook~de"] || defaults["nilvera~tr"] {
		t.Fatalf("the first account is the default and the others are not: %v", defaults)
	}

	// Routing: Germany to the second account. A rule naming an account the tenant lacks is refused; a country twice is refused.
	if code, body := do("PUT", base+"/live/routing", `{"rules":[{"country":"DE","account":"webhook~fr"}]}`); code != 400 || !strings.Contains(body, "einvoice.routing.unknown_account") {
		t.Fatalf("unknown account = %d %s", code, body)
	}
	if code, body := do("PUT", base+"/live/routing", `{"rules":[{"country":"de","account":"webhook~de"}]}`); code != 200 || !strings.Contains(body, `{"country":"DE","account":"webhook~de"}`) {
		t.Fatalf("routing = %d %s", code, body)
	}

	seller := &commandsv1.PartySnapshot{Name: "Acme Yazilim A.S.", TaxId: "1234567890", TaxOffice: "Kadikoy", Line1: "Bagdat Cad. 1", City: "Istanbul", Country: "TR"}
	trBuyer := &commandsv1.PartySnapshot{Name: "Customer A.S.", TaxId: "9876543210", TaxOffice: "Besiktas", Line1: "Barbaros 2", City: "Istanbul", Country: "TR"}
	deBuyer := &commandsv1.PartySnapshot{Name: "Kunde GmbH", TaxId: "DE123456789", Line1: "Hauptstr. 1", City: "Berlin", PostalCode: "10115", Country: "DE"}
	lines := []*commandsv1.InvoiceLineSnapshot{{LineNo: 1, Kind: "BASE_FEE", Description: "Base fee", AmountMinor: 100000}, {LineNo: 2, Kind: "TAX", Description: "KDV 20%", AmountMinor: 20000}}
	issue := func(number string, buyer *commandsv1.PartySnapshot) (uuid.UUID, *commandsv1.EInvoiceResult) {
		t.Helper()
		doc := uuid.New()
		r, err := h.Handle(ctx, issueCmd(tenant, plansv1.Environment_ENVIRONMENT_LIVE, doc, "invoice", snapshot(number, seller, buyer, lines...)))
		if err != nil {
			t.Fatal(err)
		}
		return doc, r.GetEInvoiceResult()
	}
	trDoc, tr := issue("INV-2026-000001", trBuyer)
	deDoc, de := issue("INV-2026-000002", deBuyer)
	if tr.GetState() != "issued" || tr.GetProvider() != "webhook" || de.GetState() != "issued" || de.GetProvider() != "webhook~de" {
		t.Fatalf("results: TR %v, DE %v", tr, de)
	}
	if main.count("issue") != 1 || german.count("issue") != 1 || main.sigBad != 0 || german.sigBad != 0 {
		t.Fatalf("main saw %d issues, german %d (bad signatures %d, %d); want 1 and 1", main.count("issue"), german.count("issue"), main.sigBad, german.sigBad)
	}
	var recorded string
	if err := cdb.At(tenant).QueryRow(ctx, `SELECT provider FROM einvoice_issuances WHERE document_id = $1`, deDoc).Scan(&recorded); err != nil || recorded != "webhook~de" {
		t.Fatalf("the issuance records %q, %v", recorded, err)
	}

	// The default moves without the settings being sent again; the old default is no longer one.
	if code, body := do("POST", base+"/live/default?account=webhook~fr", ""); code != 404 || !strings.Contains(body, "einvoice.account.missing") {
		t.Fatalf("default of an unknown account = %d %s", code, body)
	}
	if code, _ := do("POST", base+"/live/default?account=webhook~de", ""); code != 204 {
		t.Fatalf("default = %d", code)
	}
	code, body = do("GET", base, "")
	_ = json.Unmarshal([]byte(body), &list)
	for _, a := range list.Accounts {
		if a.IsDefault != (a.Account == "webhook~de") {
			t.Fatalf("after the move: %+v", a)
		}
	}
	// A buyer of another country now goes to the new default.
	frBuyer := &commandsv1.PartySnapshot{Name: "Client SARL", TaxId: "FR12345678901", Line1: "1 rue X", City: "Paris", PostalCode: "75001", Country: "FR"}
	if _, fr := issue("INV-2026-000003", frBuyer); fr.GetProvider() != "webhook~de" {
		t.Fatalf("FR went through %q", fr.GetProvider())
	}

	// Removing: the default stays while others exist; with no name and several accounts the request is ambiguous.
	if code, body := do("DELETE", base+"/live?account=webhook~de", ""); code != 409 || !strings.Contains(body, "einvoice.account.default_in_use") {
		t.Fatalf("delete of the default = %d %s", code, body)
	}
	if code, _ := do("DELETE", base+"/live", ""); code != 400 {
		t.Fatalf("delete without a name among several = %d", code)
	}
	if code, _ := do("DELETE", base+"/live?account=nilvera~tr", ""); code != 204 {
		t.Fatalf("delete labelled = %d", code)
	}
	// The primary account goes, and its document can no longer be cancelled through another account.
	if code, _ := do("DELETE", base+"/live?account=webhook", ""); code != 204 {
		t.Fatalf("delete primary = %d", code)
	}
	cancel := &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), SagaId: trDoc.String(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
		Command: &commandsv1.CommandEnvelope_IssueEInvoice{IssueEInvoice: &commandsv1.IssueEInvoice{Action: "cancel", DocumentId: trDoc.String(), Reason: "x"}}}
	if r, err := h.Handle(ctx, cancel); err != nil || r.GetEInvoiceResult().GetErrorKey() != "einvoice.account.invalid" {
		t.Fatalf("cancel through a removed account = %v, %v", r, err)
	}
	if german.count("cancel") != 0 {
		t.Fatal("the cancel went through another integrator")
	}
	// The rule that named a removed account goes with it.
	var rules int
	if err := cdb.At(tenant).QueryRow(ctx, `SELECT count(*) FROM einvoice_routing WHERE tenant_id = $1`, tenant).Scan(&rules); err != nil || rules != 1 {
		t.Fatalf("rules left: %d, %v", rules, err)
	}

	// The audit trail: who did what, for e-invoice accounts only when asked for them, newest first, in pages, and never a setting.
	var page struct {
		Events []store.AccountEvent `json:"events"`
		Next   int64                `json:"next_before"`
	}
	code, body = do("GET", "/admin/tenants/"+tenant.String()+"/provider-account-events?kind=einvoice&limit=3", "")
	if err := json.Unmarshal([]byte(body), &page); code != 200 || err != nil || len(page.Events) != 3 || page.Next == 0 {
		t.Fatalf("events = %d %s", code, body)
	}
	if first := page.Events[0]; first.Action != "removed" || first.Account != "webhook" || first.Actor != "user:ada" || first.Kind != "einvoice" {
		t.Errorf("newest = %+v", first)
	}
	all := map[string]int{}
	for before := int64(0); ; {
		_, body := do("GET", "/admin/tenants/"+tenant.String()+"/provider-account-events?kind=einvoice&limit=2&before="+strconv.FormatInt(before, 10), "")
		var p struct {
			Events []store.AccountEvent `json:"events"`
			Next   int64                `json:"next_before"`
		}
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatal(err)
		}
		for _, e := range p.Events {
			all[e.Action]++
		}
		if p.Next == 0 {
			break
		}
		before = p.Next
	}
	if all["connected"] != 3 || all["routing_changed"] != 1 || all["default_set"] != 1 || all["removed"] != 2 {
		t.Errorf("events by action: %v", all)
	}
	// The same changes are in the platform's audit log (pkg/audit), in their own transaction, and no setting is.
	rows, err := cdb.At(tenant).Query(ctx, `SELECT action || '/' || count(*) FROM audit_events WHERE tenant_id = $1 AND action LIKE 'einvoice_account.%' GROUP BY action ORDER BY action`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	var logged []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		logged = append(logged, s)
	}
	rows.Close()
	if want := "einvoice_account.connected/3 einvoice_account.default_changed/1 einvoice_account.removed/2 einvoice_account.routing_changed/1"; strings.Join(logged, " ") != want {
		t.Errorf("audit_events = %v, want %s", logged, want)
	}
	if n, err := cdb.CountAll(ctx, `SELECT count(*) FROM audit_events WHERE summary LIKE '%whsec_%' OR detail::text LIKE '%whsec_%' OR detail::text LIKE '%sk_secret_value%'`); err != nil || n != 0 {
		t.Errorf("a setting is in the audit log: %d, %v", n, err)
	}
	if code, body := do("GET", "/admin/tenants/"+tenant.String()+"/provider-account-events?kind=payment", ""); code != 200 || strings.Contains(body, "webhook") {
		t.Errorf("payment events = %d %s", code, body)
	}
	if code, _ := do("GET", "/admin/tenants/"+tenant.String()+"/provider-account-events?kind=other", ""); code != 400 {
		t.Errorf("a bad kind = %d", code)
	}
	if _, body := do("GET", "/admin/tenants/"+tenant.String()+"/provider-account-events", ""); strings.Contains(body, "whsec_") || strings.Contains(body, "sk_secret_value") || strings.Contains(body, "allow_private") {
		t.Errorf("a setting is in the audit trail: %s", body)
	}
}

// Moving the default of a payment account is in the audit trail too, with who moved it, and the trail is read newest first and by kind.
func TestPaymentAccountDefaultMoveIsAudited(t *testing.T) {
	pg := testkit.StartPostgres(t)
	cdb := openDB(t, pg)
	ctx := context.Background()
	box, _ := secretbox.New(bytes.Repeat([]byte{9}, 32))
	accounts := store.Accounts{DB: cdb.DB, Box: box}
	tenant := uuid.New()
	if err := accounts.PutAudited(ctx, tenant, "paddle", "live", []byte(`{}`), true, store.Audit{Actor: "user:ada", VerifiedRemote: true}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.PutAudited(ctx, tenant, "paddle~eu", "live", []byte(`{}`), false, store.Audit{Actor: "user:ada"}); err != nil {
		t.Fatal(err)
	}
	if found, err := accounts.SetDefault(ctx, tenant, "paddle~eu", "live", store.Audit{Actor: "user:grace"}); err != nil || !found {
		t.Fatalf("default: %v %v", found, err)
	}
	if found, err := accounts.SetDefault(ctx, tenant, "paddle~xx", "live", store.Audit{Actor: "user:grace"}); err != nil || found {
		t.Fatalf("default of an unknown account: %v %v", found, err)
	}
	events, err := accounts.Events(ctx, tenant, store.KindPayment, 0, 10)
	if err != nil || len(events) != 3 {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if e := events[0]; e.Action != "default_set" || e.Account != "paddle~eu" || e.Actor != "user:grace" || !e.IsDefault || e.Kind != store.KindPayment {
		t.Errorf("newest = %+v", e)
	}
	if e := events[2]; e.Action != "connected" || !e.VerifiedRemote {
		t.Errorf("oldest = %+v", e)
	}
	if other, err := accounts.Events(ctx, tenant, store.KindEInvoice, 0, 10); err != nil || len(other) != 0 {
		t.Errorf("e-invoice events of a tenant with none: %+v, %v", other, err)
	}
}
