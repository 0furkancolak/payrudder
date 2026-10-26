package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// appDashboard is GET /app/v1/apps/{id}/dashboard?environment=&period=&from=&to=&compare=.
func (s *Server) appDashboard(w http.ResponseWriter, r *http.Request) {
	appID, envName, t, ok := s.appTenant(w, r)
	if !ok {
		return
	}
	s.serveDashboard(w, r, appID, envName, t)
}

// adminTenantDashboard is GET /admin/v1/tenants/{tenant_id}/dashboard: the same dashboard for the operator, for any tenant.
func (s *Server) adminTenantDashboard(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Admin.GetTenant(r.Context(), &plansv1.GetTenantRequest{TenantId: r.PathValue("tenant_id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	t := resp.GetTenant()
	envName, err := environmentOf(t)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.serveDashboard(w, r, t.GetAppId(), envName, t)
}

func (s *Server) serveDashboard(w http.ResponseWriter, r *http.Request, appID, envName string, t *plansv1.Tenant) {
	loc := tenantLocation(t.GetTimezone())
	now := s.now()
	per, err := parseDashPeriod(r.URL.Query(), now, loc)
	if err != nil {
		writeError(w, r, err)
		return
	}
	noStoreJSON(w)
	key := t.GetId() + "|" + appID + "|" + per.Preset + "|" + dayStr(per.From) + "|" + dayStr(per.To) + "|" + strconv.FormatBool(per.Compare)
	if body, ok := s.dash.get(key, now); ok {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
		return
	}
	ctx, cancel, err := tenantContext(r.Context(), t, dashTimeout)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer cancel()
	out := s.buildDashboard(ctx, appID, envName, t, per, loc, now)
	body, err := json.Marshal(out)
	if err != nil {
		writeError(w, r, status.Error(codes.Internal, "internal error"))
		return
	}
	// A page that lost a section is not remembered: the next look should try again.
	if len(out.Unavailable) == 0 {
		s.dash.set(key, now, body)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// buildDashboard reads the sections in parallel, then puts what they answered together. ctx already carries the tenant.
func (s *Server) buildDashboard(ctx context.Context, appID, envName string, t *plansv1.Tenant, per dashPeriod, loc *time.Location, now time.Time) *dashboardResponse {
	f := &dashFetcher{s: s, ctx: ctx, t: t, per: per, loc: loc, src: dashSources{paymentsUp: true, usageByCustomer: map[string][]*limitsv1.LimitStatus{}}}
	f.curMonth = time.Date(per.To.Year(), per.To.Month(), 1, 0, 0, 0, 0, loc)
	f.prevMonth = f.curMonth.AddDate(0, -1, 0)
	var run sectionRunner
	run.run("revenue", f.revenue)
	run.run("invoices", f.invoices)
	run.run("subscriptions", f.subscriptions)
	run.run("customers", f.customers)
	run.run("payments", f.payments)
	run.run("renewals", f.renewals)
	run.run("usage", f.usage)
	run.run("webhooks", f.webhooks)
	run.run("activity", f.activity)
	return assembleDashboard(dashInput{appID: appID, envName: envName, tenant: t, per: per, loc: loc, now: now, curMonth: f.curMonth, prevMonth: f.prevMonth, src: &f.src, down: run.wait()})
}

// dashSources is what the services answered for one dashboard. Each section fills only its own fields.
type dashSources struct {
	cur, prev         *billingv1.GetAccountingSummaryResponse // revenue: the period and the one before it
	stats             *billingv1.GetDashboardStatsResponse    // invoices
	subsNow, subsPrev *plansv1.GetSubscriptionReportResponse  // subscriptions: the month the period ends in and the month before
	customers         *plansv1.GetCustomerStatsResponse
	ledger            []ledgerEntry  // payments
	providers         []providerView // payments
	paymentsUp        bool           // payments-worker answered
	renewals          *plansv1.ListSubscriptionsResponse
	usageByCustomer   map[string][]*limitsv1.LimitStatus // usage
	usageSampled      int
	usagePeriod       string
	endpoints, failed int // webhooks
	failedMore        bool
	recentSubs        *plansv1.ListSubscriptionsResponse // activity
}

// dashFetcher reads the sections of one dashboard from the services that own the facts.
type dashFetcher struct {
	s                   *Server
	ctx                 context.Context
	t                   *plansv1.Tenant
	per                 dashPeriod
	loc                 *time.Location
	curMonth, prevMonth time.Time
	src                 dashSources
}

// revenue is billing's accounting summary for the period and, when asked, the one before it.
func (f *dashFetcher) revenue() error {
	var err error
	if f.src.cur, err = f.s.d.Billing.GetAccountingSummary(f.ctx, &billingv1.GetAccountingSummaryRequest{FromDate: dayStr(f.per.From), ToDate: dayStr(f.per.To), Granularity: f.per.Granularity}); err != nil {
		return err
	}
	if f.per.Compare {
		f.src.prev, err = f.s.d.Billing.GetAccountingSummary(f.ctx, &billingv1.GetAccountingSummaryRequest{FromDate: dayStr(f.per.PrevFrom), ToDate: dayStr(f.per.PrevTo), Granularity: f.per.Granularity})
	}
	return err
}

// invoices is billing's top customers, ageing and recent invoices.
func (f *dashFetcher) invoices() error {
	var err error
	f.src.stats, err = f.s.d.Billing.GetDashboardStats(f.ctx, &billingv1.GetDashboardStatsRequest{FromDate: dayStr(f.per.From), ToDate: dayStr(f.per.To), Top: dashTop})
	return err
}

// subscriptions is plans' subscription report for the month the period ends in and, when asked, the month before it.
func (f *dashFetcher) subscriptions() error {
	var err error
	if f.src.subsNow, err = f.s.d.Plans.GetSubscriptionReport(f.ctx, &plansv1.GetSubscriptionReportRequest{Month: f.curMonth.Format(monthLayout), GroupBy: []string{"plan"}}); err != nil {
		return err
	}
	if f.per.Compare {
		f.src.subsPrev, err = f.s.d.Plans.GetSubscriptionReport(f.ctx, &plansv1.GetSubscriptionReportRequest{Month: f.prevMonth.Format(monthLayout), GroupBy: []string{"plan"}})
	}
	return err
}

// customers is plans' customer counts over the period and the one before it.
func (f *dashFetcher) customers() error {
	req := &plansv1.GetCustomerStatsRequest{FromTime: timestamppb.New(f.per.From), ToTime: timestamppb.New(f.per.To.AddDate(0, 0, 1))}
	if f.per.Compare {
		req.PrevFromTime, req.PrevToTime = timestamppb.New(f.per.PrevFrom), timestamppb.New(f.per.PrevTo.AddDate(0, 0, 1))
	}
	var err error
	f.src.customers, err = f.s.d.Plans.GetCustomerStats(f.ctx, req)
	return err
}

// payments is the newest ledger rows and the provider accounts, from payments-worker.
func (f *dashFetcher) payments() error {
	rows, providers, err := f.s.paymentsHealth(f.ctx, f.t.GetId())
	f.src.paymentsUp = err == nil
	f.src.ledger, f.src.providers = rows, providers
	return err
}

// renewals is the subscriptions the upcoming charges are worked out from.
func (f *dashFetcher) renewals() error {
	var err error
	f.src.renewals, err = f.s.d.Plans.ListSubscriptions(f.ctx, &plansv1.ListSubscriptionsRequest{PageSize: dashRenewalSubscriptions})
	return err
}

// usage reads the limits of a sample of the customers with a live subscription.
func (f *dashFetcher) usage() error {
	if f.s.d.Limits == nil {
		return errkey.New(codes.Unavailable, "gateway.dashboard.limits_unavailable", "limits")
	}
	// Which customers to look at comes from the subscriptions; the limits are read for at most ten of them.
	list, err := f.s.d.Plans.ListSubscriptions(f.ctx, &plansv1.ListSubscriptionsRequest{PageSize: dashUsageCandidates})
	if err != nil {
		return err
	}
	picks := usageSample(list.GetSubscriptions(), dashUsageSample)
	var mu sync.Mutex
	var eg errgroup.Group
	eg.SetLimit(dashUsageParallel)
	failed := 0
	for _, c := range picks {
		eg.Go(func() error {
			resp, err := f.s.d.Limits.GetLimitStatus(f.ctx, &limitsv1.GetLimitStatusRequest{CustomerId: c.id})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed++
				return nil
			}
			f.src.usageByCustomer[c.ref] = resp.GetStatuses()
			if len(resp.GetStatuses()) > 0 && f.src.usagePeriod == "" {
				f.src.usagePeriod = resp.GetStatuses()[0].GetPeriodId()
			}
			return nil
		})
	}
	_ = eg.Wait() // every goroutine returns nil: a failed customer is counted, not an error
	f.src.usageSampled = len(picks)
	if len(picks) > 0 && failed == len(picks) {
		return errkey.New(codes.Unavailable, "gateway.dashboard.limits_unavailable", "limits")
	}
	return nil
}

// webhooks counts the endpoints and the failed deliveries.
func (f *dashFetcher) webhooks() error {
	var err error
	f.src.endpoints, f.src.failed, f.src.failedMore, err = f.s.webhookHealth(f.ctx)
	return err
}

// activity is the newest subscriptions, for the activity feed.
func (f *dashFetcher) activity() error {
	var err error
	f.src.recentSubs, err = f.s.d.Plans.ListSubscriptions(f.ctx, &plansv1.ListSubscriptionsRequest{PageSize: dashActivitySubscriptions})
	return err
}
