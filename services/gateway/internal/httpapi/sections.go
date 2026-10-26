package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// The pieces the overview and the dashboard share. Both are composed from several services with the environment's tenant as the caller; a section whose
// service does not answer is left out and named in `unavailable`.

// sectionRunner runs the sections of a composed page in parallel. A section that fails is named in the page's `unavailable` list, and the page leaves its
// data out; the others are not affected. Each section writes only variables of its own, so the sections need no further locking.
type sectionRunner struct {
	wg   sync.WaitGroup
	mu   sync.Mutex
	down []string
}

// run starts the section name.
func (s *sectionRunner) run(name string, fn func() error) {
	s.wg.Go(func() {
		if err := fn(); err != nil {
			s.mu.Lock()
			s.down = append(s.down, name)
			s.mu.Unlock()
		}
	})
}

// wait blocks until every section is done and returns the names of those that failed, sorted.
func (s *sectionRunner) wait() sectionsDown {
	s.wg.Wait()
	slices.Sort(s.down)
	return s.down
}

// sectionsDown is the list of sections that did not answer.
type sectionsDown []string

// has reports whether the section answered.
func (d sectionsDown) has(name string) bool { return !slices.Contains(d, name) }

// appTenant resolves the environment (?environment=, live by default) of the app in the path for the signed-in person: the app's id, the environment's
// name and its tenant. It writes the error itself and reports false.
func (s *Server) appTenant(w http.ResponseWriter, r *http.Request) (appID, envName string, t *plansv1.Tenant, ok bool) {
	want, err := environmentParam(r)
	if err != nil {
		writeError(w, r, err)
		return "", "", nil, false
	}
	app, err := s.d.Identity.GetMyApp(r.Context(), &plansv1.GetMyAppRequest{UserId: sessionFrom(r.Context()).UserID, AppId: r.PathValue("id")})
	if err != nil {
		writeError(w, r, err)
		return "", "", nil, false
	}
	for _, c := range app.GetApp().GetTenants() {
		if c.GetEnvironment() == want.Proto() {
			t = c
		}
	}
	if t == nil {
		writeError(w, r, errkey.New(codes.NotFound, "identity.tenant.not_found", "this app has no such environment"))
		return "", "", nil, false
	}
	return app.GetApp().GetId(), want.String(), t, true
}

// tenantContext is ctx with the tenant t as the caller, for at most timeout.
func tenantContext(ctx context.Context, t *plansv1.Tenant, timeout time.Duration) (context.Context, context.CancelFunc) {
	p := principalOf(t)
	return context.WithTimeout(tenant.NewContext(ctx, tenant.Info{ID: p.Tenant.ID, Environment: p.Tenant.Environment, Timezone: p.Tenant.Timezone, Tier: p.Tier}), timeout)
}

// paymentsHealth reads what payments-worker knows of a tenant for a composed page: the newest ledger rows, and the provider accounts. A ledger that cannot be read
// is the error; the provider accounts are optional and come back empty when they cannot be.
func (s *Server) paymentsHealth(ctx context.Context, tenantID string) ([]ledgerEntry, []providerView, error) {
	rows, err := s.recentLedger(ctx, tenantID)
	if err != nil {
		return nil, nil, errkey.New(codes.Unavailable, "gateway.dashboard.payments_unavailable", "payments")
	}
	var accts struct {
		Accounts []providerView `json:"accounts"`
	}
	var providers []providerView
	if code, err := s.paymentsDo(ctx, http.MethodGet, "/admin/tenants/"+url.PathEscape(tenantID)+"/provider-accounts", nil, nil, &accts); err == nil && code/100 == 2 {
		providers = accts.Accounts
	}
	return rows, providers, nil
}

// webhookHealth counts the tenant's webhook endpoints and its failed deliveries (up to listCountCap; more says there are further ones).
func (s *Server) webhookHealth(ctx context.Context) (endpoints, failed int, more bool, err error) {
	eps, err := s.d.Webhooks.ListWebhookEndpoints(ctx, &plansv1.ListWebhookEndpointsRequest{})
	if err != nil {
		return 0, 0, false, err
	}
	ds, err := s.d.Webhooks.ListWebhookDeliveries(ctx, &plansv1.ListWebhookDeliveriesRequest{State: plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_FAILED, PageSize: listCountCap})
	return len(eps.GetEndpoints()), len(ds.GetDeliveries()), ds.GetNextPageToken() != "", err
}

// newHealth is the health block of a page: whether payments-worker answered, and its provider accounts (none when it did not say).
func newHealth(paymentsUp bool, providers []providerView) healthView {
	h := healthView{PaymentsReachable: paymentsUp, Providers: []providerView{}}
	if providers != nil {
		h.Providers = providers
	}
	return h
}

// environmentOf is the name of a tenant's environment, or the refusal for one this API does not know.
func environmentOf(t *plansv1.Tenant) (string, error) {
	tenantEnv, err := env.FromProto(t.GetEnvironment())
	if err != nil {
		return "", env.Refuse(err)
	}
	return tenantEnv.String(), nil
}
