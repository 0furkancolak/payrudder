package httpapi

import (
	"context"
	"encoding/csv"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	auditv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/audit/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// fakeAudit is the AuditService of one backing service: it answers the events it was given that are older than the cursor, as a service would, and remembers what it was asked
// and who asked.
type fakeAudit struct {
	auditv1.UnimplementedAuditServiceServer
	mu     sync.Mutex
	events []*auditv1.AuditEvent
	down   bool
	reqs   []*auditv1.ListAuditEventsRequest
	actors []audit.Actor
	tenant string
	oper   bool
}

func (f *fakeAudit) ListAuditEvents(ctx context.Context, req *auditv1.ListAuditEventsRequest) (*auditv1.ListAuditEventsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs, f.actors = append(f.reqs, req), append(f.actors, audit.FromContext(ctx))
	if t, ok := tenant.FromContext(ctx); ok {
		f.tenant = t.ID.String()
	}
	f.oper = tenant.IsOperator(ctx)
	if f.down {
		return nil, errkey.New(codes.Unavailable, "audit.unavailable", "down")
	}
	out, err := page(f.events, req.GetPageToken(), int(req.GetPageSize()))
	return &auditv1.ListAuditEventsResponse{Events: out}, err
}

// page is what a service does with a cursor and a page size: the entries older than the cursor, at most n.
func page(events []*auditv1.AuditEvent, token string, n int) ([]*auditv1.AuditEvent, error) {
	after, err := audit.ParseToken(token)
	if err != nil {
		return nil, err
	}
	var out []*auditv1.AuditEvent
	for _, e := range events {
		if after.IsZero() || e.GetCreateTime().AsTime().Before(after.At) {
			out = append(out, e)
		}
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out, nil
}

func auditEvent(minute int, action, summary string) *auditv1.AuditEvent {
	at := time.Date(2026, 11, 2, 10, minute, 0, 0, time.UTC)
	return &auditv1.AuditEvent{Id: "00000000-0000-4000-8000-0000000000" + string(rune('0'+minute/10)) + string(rune('0'+minute%10)), Action: action, Summary: summary, ActorType: "user",
		ActorEmail: "ada@example.com", DetailJson: `{"a":1}`, CreateTime: timestamppb.New(at)}
}

type auditHarness struct {
	*product
	plans, billing *fakeAudit
	payments       *httptest.Server
	paymentsEvents []*auditv1.AuditEvent
	paymentsDown   bool
	paymentsPath   string
	paymentsQuery  url.Values
}

// newAuditHarness serves the audit logs of plans-svc and billing-svc over gRPC and payments-worker's over HTTP, behind a gateway whose other upstreams are the usual fakes.
func newAuditHarness(t *testing.T) *auditHarness {
	t.Helper()
	h := &auditHarness{plans: &fakeAudit{}, billing: &fakeAudit{}}
	serve := func(f *fakeAudit) auditv1.AuditServiceClient {
		gs, _ := grpcx.NewServer(grpcx.ServerOptions{Extra: []grpc.ServerOption{grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor(), audit.UnaryServerInterceptor())}})
		auditv1.RegisterAuditServiceServer(gs, f)
		lis := bufconn.Listen(1 << 20)
		go func() { _ = gs.Serve(lis) }()
		t.Cleanup(gs.Stop)
		cc, err := grpcx.NewTenantClient("audit", "passthrough:///bufnet", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cc.Close() })
		return auditv1.NewAuditServiceClient(cc)
	}
	pc, bc := serve(h.plans), serve(h.billing)
	h.payments = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.paymentsPath, h.paymentsQuery = r.URL.Path, r.URL.Query()
		if h.paymentsDown {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		n, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		evs, _ := page(h.paymentsEvents, r.URL.Query().Get("page_token"), n)
		b, _ := protojson.MarshalOptions{UseProtoNames: true}.Marshal(&auditv1.ListAuditEventsResponse{Events: evs})
		_, _ = w.Write(b)
	}))
	t.Cleanup(h.payments.Close)
	pu, _ := url.Parse(h.payments.URL)
	h.product = newProduct(t, nil, func(d *Deps) {
		d.AuditPlans, d.AuditBilling, d.PaymentsURL = pc, bc, pu
		d.OperatorEmails = NormalizeOperatorEmails([]string{"boss@example.com"})
	})
	return h
}

func (h *auditHarness) events(t *testing.T, path, token, tenantID string) (*http.Response, map[string]any) {
	t.Helper()
	return h.call(t, "GET", path, token, tenantID, "")
}

func ids(m map[string]any) []string {
	var out []string
	for _, e := range m["events"].([]any) {
		out = append(out, e.(map[string]any)["action"].(string))
	}
	return out
}

func TestAuditEventsAreMergedNewestFirstFromEveryService(t *testing.T) {
	h := newAuditHarness(t)
	h.plans.events = []*auditv1.AuditEvent{auditEvent(50, "api_key.created", "created a key"), auditEvent(20, "plan.created", "created a plan")}
	h.billing.events = []*auditv1.AuditEvent{auditEvent(40, "credit_note.issued", "issued a note")}
	h.paymentsEvents = []*auditv1.AuditEvent{auditEvent(30, "provider_account.connected", "connected stripe")}

	resp, m := h.events(t, "/v1/audit-events?page_size=3", tokAda, adaLive)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %v", resp.StatusCode, m)
	}
	if got := strings.Join(ids(m), ","); got != "api_key.created,credit_note.issued,provider_account.connected" {
		t.Fatalf("merged = %s", got)
	}
	if m["incomplete"] != false || m["next_page_token"] == "" {
		t.Fatalf("a fourth entry exists, so there is a next page: %v", m)
	}
	// Every service was asked for one more than the page shows, for the tenant of the call, as the person.
	if n := h.plans.reqs[0].GetPageSize(); n != 4 {
		t.Errorf("page size sent to plans-svc = %d, want 4", n)
	}
	if h.plans.tenant != adaLive || h.billing.tenant != adaLive || !strings.HasPrefix(h.paymentsPath, "/admin/tenants/"+adaLive+"/audit-events") {
		t.Errorf("tenant: plans %q billing %q payments %q", h.plans.tenant, h.billing.tenant, h.paymentsPath)
	}
	if a := h.plans.actors[0]; a.Type != audit.ActorUser || a.ID != adaUser || a.Email != "ada@example.com" || a.IP == "" || a.RequestID == "" {
		t.Errorf("the actor plans-svc was told: %+v", a)
	}

	// The next page continues after the last entry and ends.
	resp, m = h.events(t, "/v1/audit-events?page_size=3&page_token="+url.QueryEscape(m["next_page_token"].(string)), tokAda, adaLive)
	if resp.StatusCode != 200 || strings.Join(ids(m), ",") != "plan.created" || m["next_page_token"] != "" {
		t.Fatalf("second page: %d %v", resp.StatusCode, m)
	}
	// The filter reaches every service, payments-worker by query string.
	_, _ = h.events(t, "/v1/audit-events?action=api_key.*,plan.created&actor=ada%40example.com&target_type=plan&from=2026-11-01T00:00:00Z", tokAda, adaLive)
	for name, req := range map[string]*auditv1.ListAuditEventsRequest{"plans": h.plans.reqs[len(h.plans.reqs)-1], "billing": h.billing.reqs[len(h.billing.reqs)-1]} {
		if len(req.GetActions()) != 2 || req.GetActor() != "ada@example.com" || req.GetTargetType() != "plan" || req.GetFromTime() == nil {
			t.Errorf("%s was asked %v", name, req)
		}
	}
	if q := h.paymentsQuery; q.Get("action") != "api_key.*,plan.created" || q.Get("actor") != "ada@example.com" || q.Get("from") == "" {
		t.Errorf("payments-worker was asked %v", q)
	}
}

func TestAuditEventsSayWhenAServiceDidNotAnswer(t *testing.T) {
	h := newAuditHarness(t)
	h.plans.events = []*auditv1.AuditEvent{auditEvent(50, "api_key.created", "created a key")}
	h.billing.down = true
	h.paymentsDown = true
	resp, m := h.events(t, "/v1/audit-events", tokAda, adaLive)
	if resp.StatusCode != 200 || m["incomplete"] != true || strings.Join(toStrings(m["unavailable_sources"]), ",") != "billing,payments" || len(ids(m)) != 1 {
		t.Fatalf("partial answer: %d %v", resp.StatusCode, m)
	}
	h.plans.down = true
	resp, m = h.events(t, "/v1/audit-events", tokAda, adaLive)
	if resp.StatusCode != 503 || errKeyOf(m) != "audit.unavailable" {
		t.Fatalf("nothing answered: %d %v", resp.StatusCode, m)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, s := range v.([]any) {
		out = append(out, s.(string))
	}
	return out
}

func TestAuditEventsPermissionsAndRoles(t *testing.T) {
	h := newAuditHarness(t)
	for _, c := range []struct {
		name, token, tenant string
		want                int
	}{
		{"a key without audit:read", "read-key", "", 403},
		{"a custom key with it", "audit-key", "", 200},
		{"an admin key", "live-key", "", 200},
		{"an owner's session", tokAda, adaLive, 200},
		{"a viewer's session", tokViewer, adaLive, 403},
		{"no credentials", "", "", 401},
	} {
		resp, m := h.events(t, "/v1/audit-events", c.token, c.tenant)
		if resp.StatusCode != c.want {
			t.Errorf("%s: %d %v, want %d", c.name, resp.StatusCode, m, c.want)
		}
	}
	// The tenant routes never take a tenant from the query: a key's own tenant is the only one.
	h.plans.events = nil
	_, _ = h.events(t, "/v1/audit-events?tenant_id="+bobLive, "live-key", "")
	if h.plans.tenant != liveTenant.String() || h.plans.reqs[len(h.plans.reqs)-1].GetTenantId() != liveTenant.String() {
		t.Errorf("a tenant call read tenant %q (request says %q)", h.plans.tenant, h.plans.reqs[len(h.plans.reqs)-1].GetTenantId())
	}
}

func TestAuditEventsOperatorView(t *testing.T) {
	h := newAuditHarness(t)
	h.plans.events = []*auditv1.AuditEvent{auditEvent(10, "two_factor.enabled", "turned on")}
	if resp, _ := h.events(t, "/admin/v1/audit-events", tokAda, ""); resp.StatusCode != 403 {
		t.Fatalf("a person who is not an operator: %d", resp.StatusCode)
	}
	resp, m := h.events(t, "/admin/v1/audit-events", tokOperator, "")
	if resp.StatusCode != 200 || len(ids(m)) != 1 || !h.plans.oper || h.plans.tenant != "" || h.paymentsPath != "/admin/audit-events" {
		t.Fatalf("operator, all tenants: %d %v oper=%v tenant=%q path=%q", resp.StatusCode, m, h.plans.oper, h.plans.tenant, h.paymentsPath)
	}
	if a := h.plans.actors[len(h.plans.actors)-1]; a.Type != audit.ActorOperator || a.Email != "boss@example.com" {
		t.Errorf("operator actor = %+v", a)
	}
	_, _ = h.events(t, "/admin/v1/audit-events?tenant_id="+bobLive+"&account_id="+bobAcct, tokOperator, "")
	if req := h.plans.reqs[len(h.plans.reqs)-1]; req.GetTenantId() != bobLive || req.GetAccountId() != bobAcct || !strings.HasPrefix(h.paymentsPath, "/admin/tenants/"+bobLive+"/") {
		t.Errorf("operator narrowed to a tenant: %v path %s", req, h.paymentsPath)
	}
	if resp, m := h.events(t, "/admin/v1/audit-events?from=yesterday", tokOperator, ""); resp.StatusCode != 400 || errKeyOf(m) != "audit.filter.bad_time" {
		t.Errorf("bad time: %d %v", resp.StatusCode, m)
	}
}

func TestAuditExportIsCSVAndNeverLeavesRowsOut(t *testing.T) {
	h := newAuditHarness(t)
	h.plans.events = []*auditv1.AuditEvent{auditEvent(50, "member.invited", `=HYPERLINK("http://evil.example")`), auditEvent(20, "plan.created", "created a plan")}
	req, _ := http.NewRequest("GET", h.ts.URL+"/v1/audit-events/export", nil)
	req.Header.Set("Authorization", "Bearer "+tokAda)
	req.Header.Set("X-Payrudder-Tenant", adaLive)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") || !strings.Contains(resp.Header.Get("Content-Disposition"), "audit-log.csv") || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("export: %d %v", resp.StatusCode, resp.Header)
	}
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil || len(rows) != 3 || strings.Join(rows[0], ",") != strings.Join(audit.CSVHeader, ",") {
		t.Fatalf("csv = %q, %v", body, err)
	}
	if !strings.HasPrefix(rows[1][10], "'=") {
		t.Errorf("a summary that starts with = must not be a formula in a spreadsheet: %q", rows[1][10])
	}
	// A service that does not answer fails the export instead of leaving its rows out.
	h.billing.down = true
	if resp, m := h.events(t, "/v1/audit-events/export", tokAda, adaLive); resp.StatusCode != 503 || errKeyOf(m) != "audit.unavailable" {
		t.Errorf("incomplete export: %d %v", resp.StatusCode, m)
	}
}
