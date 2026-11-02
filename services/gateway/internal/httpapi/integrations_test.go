package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// fakeMailIntegration is the plans-svc service for a tenant's own e-mail provider; it records who it was asked for.
type fakeMailIntegration struct {
	mu      sync.Mutex
	tenants []string
	put     *plansv1.PutMailIntegrationRequest
	deleted int
	// deletedAccount, tested and testedAccount are what the last delete and test named.
	deletedAccount string
	tested         string
	testedAccount  string
	defaulted      string
	rules          []*plansv1.MailRoutingRule
}

func (f *fakeMailIntegration) saw(ctx context.Context) {
	md, _ := metadata.FromOutgoingContext(ctx)
	f.mu.Lock()
	f.tenants = append(f.tenants, strings.Join(md.Get(tenant.MDTenantID), ","))
	f.mu.Unlock()
}

func (f *fakeMailIntegration) GetMailIntegration(ctx context.Context, _ *plansv1.GetMailIntegrationRequest, _ ...grpc.CallOption) (*plansv1.GetMailIntegrationResponse, error) {
	f.saw(ctx)
	return &plansv1.GetMailIntegrationResponse{Connected: true, Integration: &plansv1.MailIntegration{Provider: "nafru", KeyHint: "1234"}}, nil
}

func (f *fakeMailIntegration) PutMailIntegration(ctx context.Context, in *plansv1.PutMailIntegrationRequest, _ ...grpc.CallOption) (*plansv1.PutMailIntegrationResponse, error) {
	f.saw(ctx)
	f.mu.Lock()
	f.put = in
	f.mu.Unlock()
	if in.GetApiKey() == "refused" {
		return nil, errkey.New(codes.InvalidArgument, "plans.mail_integration.refused", "the provider did not accept this key")
	}
	return &plansv1.PutMailIntegrationResponse{Integration: &plansv1.MailIntegration{Provider: in.GetProvider(), KeyHint: "1234"}}, nil
}

func (f *fakeMailIntegration) DeleteMailIntegration(ctx context.Context, in *plansv1.DeleteMailIntegrationRequest, _ ...grpc.CallOption) (*plansv1.DeleteMailIntegrationResponse, error) {
	f.saw(ctx)
	f.mu.Lock()
	f.deleted++
	f.deletedAccount = in.GetAccount()
	f.mu.Unlock()
	return &plansv1.DeleteMailIntegrationResponse{}, nil
}

func (f *fakeMailIntegration) TestMailIntegration(ctx context.Context, in *plansv1.TestMailIntegrationRequest, _ ...grpc.CallOption) (*plansv1.TestMailIntegrationResponse, error) {
	f.saw(ctx)
	f.mu.Lock()
	f.tested, f.testedAccount = in.GetTo(), in.GetAccount()
	f.mu.Unlock()
	return &plansv1.TestMailIntegrationResponse{Ok: true, MessageId: "em_1"}, nil
}

func (f *fakeMailIntegration) SetDefaultMailIntegration(ctx context.Context, in *plansv1.SetDefaultMailIntegrationRequest, _ ...grpc.CallOption) (*plansv1.SetDefaultMailIntegrationResponse, error) {
	f.saw(ctx)
	f.mu.Lock()
	f.defaulted = in.GetAccount()
	f.mu.Unlock()
	return &plansv1.SetDefaultMailIntegrationResponse{}, nil
}

func (f *fakeMailIntegration) GetMailRouting(ctx context.Context, _ *plansv1.GetMailRoutingRequest, _ ...grpc.CallOption) (*plansv1.GetMailRoutingResponse, error) {
	f.saw(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	return &plansv1.GetMailRoutingResponse{Rules: f.rules}, nil
}

func (f *fakeMailIntegration) PutMailRouting(ctx context.Context, in *plansv1.PutMailRoutingRequest, _ ...grpc.CallOption) (*plansv1.PutMailRoutingResponse, error) {
	f.saw(ctx)
	f.mu.Lock()
	f.rules = in.GetRules()
	f.mu.Unlock()
	return &plansv1.PutMailRoutingResponse{Rules: in.GetRules()}, nil
}

func (f *fakeMailIntegration) ResolveMailSender(context.Context, *plansv1.ResolveMailSenderRequest, ...grpc.CallOption) (*plansv1.ResolveMailSenderResponse, error) {
	return &plansv1.ResolveMailSenderResponse{ApiKey: "must never be reachable from a route"}, nil
}

// The e-mail provider of an app: the tenant is the app's live one whatever the request says, only the owner changes anything, the key is never in an answer.
func TestMailIntegrationRoutes(t *testing.T) {
	mi := &fakeMailIntegration{}
	p := newProduct(t, nil, func(d *Deps) { d.MailIntegration = mi })
	base := "/app/v1/apps/" + adaApp + "/integrations/mail"

	resp, m := p.call(t, "GET", base, tokAda, "", "")
	if resp.StatusCode != 200 || m["connected"] != true || resp.Header.Get("Cache-Control") == "" {
		t.Fatalf("get: %d %v", resp.StatusCode, m)
	}
	if got := mi.tenants[len(mi.tenants)-1]; got != adaLive {
		t.Errorf("asked for tenant %q, want the app's live tenant %q", got, adaLive)
	}
	if resp, _ := p.call(t, "GET", base, tokViewer, "", ""); resp.StatusCode != 200 {
		t.Errorf("a viewer may read: %d", resp.StatusCode)
	}

	body := `{"provider":"nafru","base_url":"https://api.nafru.example","api_key":"nk_1234","from":"Acme <a@acme.example>"}`
	resp, m = p.call(t, "PUT", base, tokAda, "", body)
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" || strings.Contains(toJSON(m), "nk_1234") {
		t.Fatalf("put: %d %v (no-store, and the key is never answered)", resp.StatusCode, m)
	}
	if mi.put.GetApiKey() != "nk_1234" || mi.put.GetBaseUrl() != "https://api.nafru.example" || mi.tenants[len(mi.tenants)-1] != adaLive {
		t.Errorf("put reached plans-svc as %v for %v", mi.put, mi.tenants)
	}
	if resp, m := p.call(t, "PUT", base, tokViewer, "", body); resp.StatusCode != 403 || errKeyOf(m) != "identity.go_live.owner_only" {
		t.Errorf("a viewer: %d %v", resp.StatusCode, m)
	}
	if resp, m := p.call(t, "PUT", "/app/v1/apps/"+bobApp+"/integrations/mail", tokAda, "", body); resp.StatusCode != 404 || errKeyOf(m) != "identity.app.not_found" {
		t.Errorf("another account's app: %d %v", resp.StatusCode, m)
	}
	if resp, m := p.call(t, "PUT", base, tokAda, "", `{"provider":"nafru","api_key":"refused"}`); resp.StatusCode != 400 || errKeyOf(m) != "plans.mail_integration.refused" {
		t.Errorf("a refused key: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "PUT", base, "", "", body); resp.StatusCode != 401 {
		t.Errorf("without a session: %d", resp.StatusCode)
	}

	if resp, m := p.call(t, "POST", base+"/test", tokAda, "", `{"to":"ada@example.com"}`); resp.StatusCode != 200 || m["ok"] != true || mi.tested != "ada@example.com" {
		t.Errorf("test: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "DELETE", base, tokAda, "", ""); resp.StatusCode != 204 || mi.deleted != 1 {
		t.Errorf("delete: %d", resp.StatusCode)
	}
	if resp, _ := p.call(t, "DELETE", base, tokViewer, "", ""); resp.StatusCode != 403 || mi.deleted != 1 {
		t.Errorf("delete by a viewer: %d", resp.StatusCode)
	}
}

// Several e-mail accounts: a label connects a further one, the default moves without a key, a delete and a test name their account, and the rules that send a language
// through an account are read by a member and replaced by the owner, always for the app's live tenant.
func TestMailAccountRoutes(t *testing.T) {
	mi := &fakeMailIntegration{}
	p := newProduct(t, nil, func(d *Deps) { d.MailIntegration = mi })
	base := "/app/v1/apps/" + adaApp + "/integrations/mail"

	body := `{"provider":"nafru","label":"eu","make_default":true,"base_url":"https://api.nafru.example","api_key":"nk_1234","from":"Acme <a@acme.example>"}`
	if resp, m := p.call(t, "PUT", base, tokAda, "", body); resp.StatusCode != 200 || mi.put.GetLabel() != "eu" || !mi.put.GetMakeDefault() {
		t.Fatalf("a labelled account: %d %v %v", resp.StatusCode, m, mi.put)
	}
	if resp, m := p.call(t, "POST", base+"/default", tokAda, "", `{"account":"nafru~eu"}`); resp.StatusCode != 200 || mi.defaulted != "nafru~eu" || mi.tenants[len(mi.tenants)-1] != adaLive {
		t.Errorf("default: %d %v (%q for %v)", resp.StatusCode, m, mi.defaulted, mi.tenants)
	}
	if resp, m := p.call(t, "POST", base+"/default", tokViewer, "", `{"account":"nafru"}`); resp.StatusCode != 403 || errKeyOf(m) != "identity.go_live.owner_only" || mi.defaulted != "nafru~eu" {
		t.Errorf("default by a viewer: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "POST", base+"/test", tokAda, "", `{"to":"ada@example.com","account":"nafru~eu"}`); resp.StatusCode != 200 || mi.testedAccount != "nafru~eu" {
		t.Errorf("test names its account: %d %q", resp.StatusCode, mi.testedAccount)
	}
	if resp, _ := p.call(t, "DELETE", base+"?account=nafru~eu", tokAda, "", ""); resp.StatusCode != 204 || mi.deletedAccount != "nafru~eu" {
		t.Errorf("delete names its account: %d %q", resp.StatusCode, mi.deletedAccount)
	}
	if resp, m := p.call(t, "DELETE", base+"?account=Nafru~EU", tokAda, "", ""); resp.StatusCode != 400 || mi.deleted != 1 {
		t.Errorf("a delete of a name that is not an account key reaches nothing: %d %v", resp.StatusCode, m)
	}

	rules := `{"rules":[{"language":"de","account":"nafru~eu"},{"language":"fr","account":"nafru~eu"}]}`
	if resp, _ := p.call(t, "PUT", base+"/routing", tokViewer, "", rules); resp.StatusCode != 403 || len(mi.rules) != 0 {
		t.Errorf("rules by a viewer: %d", resp.StatusCode)
	}
	if resp, m := p.call(t, "PUT", base+"/routing", tokAda, "", rules); resp.StatusCode != 200 || len(mi.rules) != 2 || mi.tenants[len(mi.tenants)-1] != adaLive {
		t.Fatalf("rules: %d %v %v", resp.StatusCode, m, mi.rules)
	}
	resp, m := p.call(t, "GET", base+"/routing", tokViewer, "", "")
	if got, _ := m["rules"].([]any); resp.StatusCode != 200 || len(got) != 2 {
		t.Errorf("a viewer reads the rules: %d %v", resp.StatusCode, m)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// The e-invoice integrator of an app: forwarded to payments-worker for the live tenant only, the mock is never a choice, only the owner changes anything.
func TestEInvoiceIntegrationRoutes(t *testing.T) {
	var mu sync.Mutex
	var seen []seenRequest
	pw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, seenRequest{method: r.Method, path: r.URL.Path, actor: r.Header.Get("X-Payrudder-Actor"), body: string(b)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"accounts":[{"environment":"sandbox","provider":"mock","mode":"test"},{"environment":"live","account":"nilvera","provider":"nilvera","label":"","mode":"production","is_default":true,"updated_at":"2026-10-01T00:00:00Z"}]}`)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/check"):
			_, _ = io.WriteString(w, `{"ok":true,"checked":"credentials"}`)
		default:
			_, _ = io.WriteString(w, `{"environment":"live","provider":"nilvera","mode":"production","has_credentials":true}`)
		}
	}))
	defer pw.Close()
	u, _ := url.Parse(pw.URL)
	p := newProduct(t, nil, func(d *Deps) { d.PaymentsURL = u; d.PaymentsToken = "internal-token" })
	base := "/app/v1/apps/" + adaApp + "/integrations/e-invoice"

	resp, m := p.call(t, "GET", base, tokAda, "", "")
	acc, _ := m["account"].(map[string]any)
	if resp.StatusCode != 200 || acc["provider"] != "nilvera" || acc["mode"] != "production" {
		t.Fatalf("get: %d %v: only the live account is shown", resp.StatusCode, m)
	}
	if strings.Contains(toJSON(m), "mock") {
		t.Errorf("the sandbox account leaked: %v", m)
	}
	if seen[0].path != "/admin/tenants/"+adaLive+"/e-invoice-accounts" {
		t.Errorf("listed %s", seen[0].path)
	}

	put := `{"provider":"nilvera","mode":"production","credentials":{"api_key":"NV-SECRET"}}`
	resp, m = p.call(t, "PUT", base, tokAda, "", put)
	if resp.StatusCode != 200 || m["provider"] != "nilvera" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("put: %d %v", resp.StatusCode, m)
	}
	w := seen[len(seen)-1]
	if w.method != "PUT" || w.path != "/admin/tenants/"+adaLive+"/e-invoice-accounts/live" || w.body != put || w.actor != "user:"+adaUser {
		t.Errorf("forwarded %+v: live tenant, live environment, the body as typed, the person as actor", w)
	}
	if resp, m := p.call(t, "PUT", base, tokAda, "", `{"provider":"mock"}`); resp.StatusCode != 400 {
		t.Errorf("the mock integrator: %d %v", resp.StatusCode, m)
	}
	before := len(seen)
	if resp, m := p.call(t, "PUT", base, tokViewer, "", put); resp.StatusCode != 403 || errKeyOf(m) != "identity.go_live.owner_only" {
		t.Errorf("a viewer: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "POST", base+"/test", tokViewer, "", `{}`); resp.StatusCode != 403 {
		t.Errorf("a viewer's test: %d", resp.StatusCode)
	}
	if len(seen) != before {
		t.Errorf("payments-worker was bothered %d times for people who may not change anything", len(seen)-before)
	}
	if resp, m := p.call(t, "POST", base+"/test", tokAda, "", `{"tax_id":"1234567890"}`); resp.StatusCode != 200 || m["ok"] != true || seen[len(seen)-1].path != "/admin/tenants/"+adaLive+"/e-invoice-accounts/live/check" {
		t.Errorf("test: %d %v %s", resp.StatusCode, m, seen[len(seen)-1].path)
	}
	if resp, _ := p.call(t, "DELETE", base, tokAda, "", ""); resp.StatusCode != 204 || seen[len(seen)-1].path != "/admin/tenants/"+adaLive+"/e-invoice-accounts/live" {
		t.Errorf("delete: %d", resp.StatusCode)
	}
}
