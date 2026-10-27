package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// Payrudder as a SaaS (phase 55, ADR 0024). With PAYRUDDER_SAAS=on the gateway is the edge of the plans: it shows an organisation its plan and quotas, hands out
// the invoices and payment links of the platform tenant, answers the operator's view, and at the ingest edge it refuses events of an organisation over its
// hard events quota and meters the events it accepts into the platform tenant (the organisation's customer row), where limits-svc counts them. With the mode off
// d.Saas is nil and none of it exists: no route answers anything but "not enabled", ingest is untouched.

func (s *Server) saasRoutes() []Route {
	return []Route{
		{"GET", "/app/v1/account/plan", AccessSession, ratelimit.ClassRead, s.withAccount(s.planGet)},
		{"PUT", "/app/v1/account/plan", AccessSession, ratelimit.ClassWrite, s.withAccount(s.planChange)},
		{"GET", "/app/v1/account/plan/invoices", AccessSession, ratelimit.ClassRead, s.withAccount(s.planInvoices)},
		{"POST", "/app/v1/account/plan/invoices/{id}/pay-link", AccessSession, ratelimit.ClassWrite, s.withAccount(s.planPayLink)},
		{"POST", "/app/v1/account/plan/portal-link", AccessSession, ratelimit.ClassWrite, s.withAccount(s.planPortalLink)},
		{"GET", "/admin/v1/saas", AccessOperator, "", s.adminSaas},
		{"GET", "/admin/v1/organizations", AccessOperator, "", s.adminOrganizations},
		{"PUT", "/admin/v1/organizations/{account_id}/plan", AccessOperator, "", s.adminSetOrganizationPlan},
		{"DELETE", "/admin/v1/organizations/{account_id}/overrides", AccessOperator, "", s.adminClearOrganizationOverrides},
	}
}

// The JSON of the SaaS API. Limits and counts are numbers (a limit never comes near 2^53), unlike the proto-JSON of the rest of the API, which writes an int64
// as a string; the console reads them as numbers.

type planLimitJSON struct {
	Resource string `json:"resource"`
	Limit    int64  `json:"limit"`
	Mode     string `json:"mode"`
}

type planOfferJSON struct {
	Code         string          `json:"code"`
	Name         string          `json:"name"`
	Version      int32           `json:"version"`
	BaseFeeMinor int64           `json:"base_fee_minor"`
	Currency     string          `json:"currency"`
	Interval     string          `json:"interval"`
	TrialDays    int32           `json:"trial_days"`
	Limits       []planLimitJSON `json:"limits"`
	Current      bool            `json:"current"`
}

type planQuotaJSON struct {
	Resource   string  `json:"resource"`
	Used       int64   `json:"used"`
	Limit      int64   `json:"limit"`
	Unlimited  bool    `json:"unlimited"`
	Mode       string  `json:"mode"`
	OverLimit  bool    `json:"over_limit"`
	Overridden bool    `json:"overridden"`
	ResetsTime *string `json:"resets_time,omitempty"`
}

type organizationPlanJSON struct {
	Enabled        bool            `json:"enabled"`
	AccountID      string          `json:"account_id,omitempty"`
	Plan           *planOfferJSON  `json:"plan,omitempty"`
	State          string          `json:"state,omitempty"`
	TrialEndTime   *string         `json:"trial_end_time,omitempty"`
	Overridden     bool            `json:"overridden"`
	Quotas         []planQuotaJSON `json:"quotas"`
	Offers         []planOfferJSON `json:"offers"`
	CanManage      bool            `json:"can_manage"`
	CustomerID     string          `json:"customer_id,omitempty"`
	SubscriptionID string          `json:"subscription_id,omitempty"`
}

func rfc3339(t *timestamppb.Timestamp) *string {
	if t == nil || t.AsTime().IsZero() || t.AsTime().Unix() == 0 {
		return nil
	}
	s := t.AsTime().UTC().Format(time.RFC3339)
	return &s
}

func offerJSON(o *plansv1.PlanOffer) *planOfferJSON {
	if o == nil {
		return nil
	}
	out := &planOfferJSON{Code: o.GetCode(), Name: o.GetName(), Version: o.GetVersion(), BaseFeeMinor: o.GetBaseFeeMinor(), Currency: o.GetCurrency(), Interval: o.GetInterval(),
		TrialDays: o.GetTrialDays(), Current: o.GetCurrent(), Limits: []planLimitJSON{}}
	for _, l := range o.GetLimits() {
		out.Limits = append(out.Limits, planLimitJSON{Resource: l.GetResource(), Limit: l.GetLimit(), Mode: l.GetMode()})
	}
	return out
}

func planJSON(p *plansv1.OrganizationPlan) organizationPlanJSON {
	out := organizationPlanJSON{Enabled: p.GetEnabled(), AccountID: p.GetAccountId(), Plan: offerJSON(p.GetPlan()), State: p.GetState(), TrialEndTime: rfc3339(p.GetTrialEndTime()),
		Overridden: p.GetOverridden(), CanManage: p.GetCanManage(), CustomerID: p.GetCustomerId(), SubscriptionID: p.GetSubscriptionId(), Quotas: []planQuotaJSON{}, Offers: []planOfferJSON{}}
	for _, q := range p.GetQuotas() {
		out.Quotas = append(out.Quotas, planQuotaJSON{Resource: q.GetResource(), Used: q.GetUsed(), Limit: q.GetLimit(), Unlimited: q.GetUnlimited(), Mode: q.GetMode(),
			OverLimit: q.GetOverLimit(), Overridden: q.GetOverridden(), ResetsTime: rfc3339(q.GetResetsTime())})
	}
	for _, o := range p.GetOffers() {
		out.Offers = append(out.Offers, *offerJSON(o))
	}
	return out
}

// saasTimeout bounds what a SaaS page asks of limits-svc and billing-svc besides plans-svc.
const saasTimeout = 4 * time.Second

// platformContext is ctx acting as the platform tenant, for at most timeout.
func platformContext(ctx context.Context, p *plansv1.PlatformTenant, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	id, err := uuid.Parse(p.GetId())
	if err != nil {
		return nil, nil, errkey.New(codes.Unavailable, "gateway.saas.unavailable", "the platform tenant is not available")
	}
	ctx, cancel := context.WithTimeout(tenant.NewContext(ctx, tenant.Info{ID: id, Environment: p.GetEnvironment(), Timezone: p.GetTimezone()}), timeout)
	return ctx, cancel, nil
}

// eventsMeter is the platform-tenant meter the events of an organisation are counted on.
const eventsMeter = "events"

// fillEvents sets what only limits-svc knows on the events quota of a plan: how many events the organisation has had this month. It is best effort: a plan page
// without the count is better than none.
func (s *Server) fillEvents(ctx context.Context, p *plansv1.OrganizationPlan) {
	if p.GetCustomerId() == "" || p.GetPlatform() == nil {
		return
	}
	pctx, cancel, err := platformContext(ctx, p.GetPlatform(), saasTimeout)
	if err != nil {
		return
	}
	defer cancel()
	resp, err := s.d.Limits.CheckEntitlement(pctx, &limitsv1.CheckEntitlementRequest{CustomerId: p.GetCustomerId(), MeterCode: eventsMeter})
	if err != nil {
		s.d.Log.WarnContext(ctx, "saas: events usage unavailable", "account_id", p.GetAccountId(), "error", err)
		return
	}
	st := resp.GetStatus()
	for _, q := range p.GetQuotas() {
		if q.GetResource() == eventsMeter {
			q.Used = st.GetUsedUnits()
			q.OverLimit = !q.GetUnlimited() && q.GetUsed() >= q.GetLimit()
			q.ResetsTime = st.GetResetsAt()
		}
	}
}

func (s *Server) planGet(w http.ResponseWriter, r *http.Request, sess session, id string) {
	if s.d.Saas == nil {
		writeJSONValue(w, http.StatusOK, organizationPlanJSON{Quotas: []planQuotaJSON{}, Offers: []planOfferJSON{}})
		return
	}
	resp, err := s.d.Saas.GetMyPlan(r.Context(), &plansv1.GetMyPlanRequest{UserId: sess.UserID, AccountId: id})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if resp.GetPlan().GetEnabled() {
		s.fillEvents(r.Context(), resp.GetPlan())
	}
	writeJSONValue(w, http.StatusOK, planJSON(resp.GetPlan()))
}

func (s *Server) planChange(w http.ResponseWriter, r *http.Request, sess session, id string) {
	if s.d.Saas == nil {
		writeError(w, r, saasOff())
		return
	}
	var body struct {
		PlanCode string `json:"plan_code"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Saas.ChangeMyPlan(r.Context(), &plansv1.ChangeMyPlanRequest{UserId: sess.UserID, AccountId: id, PlanCode: body.PlanCode})
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.fillEvents(r.Context(), resp.GetPlan())
	writeJSONValue(w, http.StatusOK, planJSON(resp.GetPlan()))
}

// ownPlan loads the plan page data of the account for the handlers that talk to billing-svc about it: the customer and the platform tenant. manage requires
// the caller to be an owner or administrator. It writes the refusal itself.
func (s *Server) ownPlan(w http.ResponseWriter, r *http.Request, sess session, id string, manage bool) (*plansv1.OrganizationPlan, bool) {
	if s.d.Saas == nil {
		writeError(w, r, saasOff())
		return nil, false
	}
	resp, err := s.d.Saas.GetMyPlan(r.Context(), &plansv1.GetMyPlanRequest{UserId: sess.UserID, AccountId: id})
	if err != nil {
		writeError(w, r, err)
		return nil, false
	}
	p := resp.GetPlan()
	if !p.GetEnabled() || p.GetCustomerId() == "" {
		writeError(w, r, errkey.New(codes.Unavailable, "gateway.saas.unavailable", "the plan's billing is not available yet"))
		return nil, false
	}
	if manage && !p.GetCanManage() {
		writeError(w, r, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "your role in this account does not allow this"))
		return nil, false
	}
	return p, true
}

type planInvoiceJSON struct {
	ID           string  `json:"id"`
	Number       string  `json:"number"`
	Status       string  `json:"status"`
	Currency     string  `json:"currency"`
	TotalMinor   int64   `json:"total_minor"`
	CreateTime   *string `json:"create_time,omitempty"`
	FinalizeTime *string `json:"finalize_time,omitempty"`
}

// planInvoices lists what the platform tenant invoiced the organisation. Drafts are not the organisation's yet, and an invoice of nothing (every month of the
// Free plan has one) says nothing, so both are left out.
func (s *Server) planInvoices(w http.ResponseWriter, r *http.Request, sess session, id string) {
	p, ok := s.ownPlan(w, r, sess, id, false)
	if !ok {
		return
	}
	pctx, cancel, err := platformContext(r.Context(), p.GetPlatform(), saasTimeout)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer cancel()
	list, err := s.d.Billing.ListInvoices(pctx, &billingv1.ListInvoicesRequest{CustomerId: p.GetCustomerId(), PageSize: 100})
	if err != nil {
		writeError(w, r, errkey.New(codes.Unavailable, "gateway.saas.unavailable", "the invoices are not available now"))
		return
	}
	out := []planInvoiceJSON{}
	for _, inv := range list.GetInvoices() {
		if inv.GetStatus() == "draft" || inv.GetTotalMinor() == 0 {
			continue
		}
		out = append(out, planInvoiceJSON{ID: inv.GetId(), Number: inv.GetNumber(), Status: inv.GetStatus(), Currency: inv.GetCurrency(), TotalMinor: inv.GetTotalMinor(),
			CreateTime: rfc3339(inv.GetCreateTime()), FinalizeTime: rfc3339(inv.GetFinalizeTime())})
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"invoices": out})
}

// planPayLink makes a payment link for an unpaid invoice of the organisation, through the payment-link machinery of the platform tenant. The invoice must be the
// organisation's own: another customer's invoice is "not found".
func (s *Server) planPayLink(w http.ResponseWriter, r *http.Request, sess session, id string) {
	p, ok := s.ownPlan(w, r, sess, id, true)
	if !ok {
		return
	}
	invoiceID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, r, invalid("id", "must be a UUID"))
		return
	}
	pctx, cancel, err := platformContext(r.Context(), p.GetPlatform(), saasTimeout)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer cancel()
	inv, err := s.d.Billing.GetInvoice(pctx, &billingv1.GetInvoiceRequest{InvoiceId: invoiceID.String()})
	if err != nil || inv.GetInvoice().GetCustomerId() != p.GetCustomerId() {
		writeError(w, r, errkey.New(codes.NotFound, "billing.invoice.not_found", "no such invoice"))
		return
	}
	if inv.GetInvoice().GetStatus() != "open" || inv.GetInvoice().GetTotalMinor() <= 0 {
		writeError(w, r, errkey.New(codes.FailedPrecondition, "gateway.saas.invoice_not_payable", "this invoice is not waiting for a payment"))
		return
	}
	// A new key each time: a link's token is shown once, so a repeated request has to make another link rather than replay the first without its token.
	link, err := s.d.Billing.CreatePaymentLink(pctx, &billingv1.CreatePaymentLinkRequest{Kind: "invoice", InvoiceId: invoiceID.String(), IdempotencyKey: "saas-pay:" + uuid.NewString()})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusCreated, map[string]any{"url": s.d.PublicURL + "/i/" + link.GetToken()})
}

// planPortalLink makes a link to the customer portal of the organisation's customer row: its invoices and its payment method.
func (s *Server) planPortalLink(w http.ResponseWriter, r *http.Request, sess session, id string) {
	p, ok := s.ownPlan(w, r, sess, id, true)
	if !ok {
		return
	}
	pctx, cancel, err := platformContext(r.Context(), p.GetPlatform(), saasTimeout)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer cancel()
	link, err := s.d.Billing.CreatePortalLink(pctx, &billingv1.CreatePortalLinkRequest{CustomerExternalId: p.GetAccountId()})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusCreated, map[string]any{"url": s.d.PublicURL + "/c/" + link.GetToken(), "expires_at": link.GetExpireTime().AsTime().UTC().Format(time.RFC3339)})
}

type platformInfoJSON struct {
	Enabled     bool   `json:"enabled"`
	TenantID    string `json:"tenant_id,omitempty"`
	Environment string `json:"environment,omitempty"`
	Currency    string `json:"currency,omitempty"`
}

func platformInfo(enabled bool, p *plansv1.PlatformTenant) platformInfoJSON {
	return platformInfoJSON{Enabled: enabled, TenantID: p.GetId(), Environment: p.GetEnvironment(), Currency: p.GetCurrency()}
}

func (s *Server) adminSaas(w http.ResponseWriter, r *http.Request) {
	if s.d.Saas == nil {
		writeJSONValue(w, http.StatusOK, platformInfoJSON{})
		return
	}
	resp, err := s.d.Saas.GetPlatform(r.Context(), &plansv1.GetPlatformRequest{})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusOK, platformInfo(resp.GetEnabled(), resp.GetTenant()))
}

// saasOff is the answer of a SaaS route while the mode is off.
func saasOff() error {
	return errkey.New(codes.FailedPrecondition, "plans.saas.disabled", "Payrudder is not running as a SaaS here: there are no plans")
}

type organizationRowJSON struct {
	AccountID   string  `json:"account_id"`
	Name        string  `json:"name"`
	OwnerEmail  string  `json:"owner_email"`
	PlanCode    string  `json:"plan_code"`
	PlanName    string  `json:"plan_name"`
	State       string  `json:"state"`
	Overridden  bool    `json:"overridden"`
	Apps        int64   `json:"apps"`
	Members     int64   `json:"members"`
	APIKeys     int64   `json:"api_keys"`
	EventsUsed  int64   `json:"events_used"`
	EventsLimit int64   `json:"events_limit"`
	CreateTime  *string `json:"create_time,omitempty"`
}

// adminOrganizations is the operator's list of organisations with plan and usage. The events used are read from limits-svc a few at a time; an organisation
// whose count cannot be read shows 0.
func (s *Server) adminOrganizations(w http.ResponseWriter, r *http.Request) {
	if s.d.Saas == nil {
		writeError(w, r, saasOff())
		return
	}
	q := r.URL.Query()
	n, err := intParam(q, "page_size")
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Saas.ListOrganizations(r.Context(), &plansv1.ListOrganizationsRequest{PageSize: n, PageToken: q.Get("page_token"), Query: q.Get("q")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	rows := make([]organizationRowJSON, len(resp.GetOrganizations()))
	var g errgroup.Group
	g.SetLimit(8)
	for i, o := range resp.GetOrganizations() {
		rows[i] = organizationRowJSON{AccountID: o.GetAccountId(), Name: o.GetName(), OwnerEmail: o.GetOwnerEmail(), PlanCode: o.GetPlanCode(), PlanName: o.GetPlanName(), State: o.GetState(),
			Overridden: o.GetOverridden(), Apps: o.GetApps(), Members: o.GetMembers(), APIKeys: o.GetApiKeys(), EventsLimit: o.GetEventsLimit(), CreateTime: rfc3339(o.GetCreateTime())}
		if o.GetCustomerId() == "" {
			continue
		}
		g.Go(func() error {
			pctx, cancel, err := platformContext(r.Context(), resp.GetPlatform(), saasTimeout)
			if err != nil {
				return nil
			}
			defer cancel()
			if st, err := s.d.Limits.CheckEntitlement(pctx, &limitsv1.CheckEntitlementRequest{CustomerId: o.GetCustomerId(), MeterCode: eventsMeter}); err == nil {
				rows[i].EventsUsed = st.GetStatus().GetUsedUnits()
			}
			return nil
		})
	}
	_ = g.Wait() // the goroutines never fail the batch
	writeJSONValue(w, http.StatusOK, map[string]any{"organizations": rows, "next_page_token": resp.GetNextPageToken(), "platform": platformInfo(true, resp.GetPlatform())})
}

func (s *Server) adminSetOrganizationPlan(w http.ResponseWriter, r *http.Request) {
	if s.d.Saas == nil {
		writeError(w, r, saasOff())
		return
	}
	var body struct {
		PlanCode string           `json:"plan_code"`
		Limits   map[string]int64 `json:"limits"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Saas.SetOrganizationPlan(r.Context(), &plansv1.SetOrganizationPlanRequest{AccountId: r.PathValue("account_id"), PlanCode: body.PlanCode, Limits: body.Limits})
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.fillEvents(r.Context(), resp.GetPlan())
	writeJSONValue(w, http.StatusOK, planJSON(resp.GetPlan()))
}

func (s *Server) adminClearOrganizationOverrides(w http.ResponseWriter, r *http.Request) {
	if s.d.Saas == nil {
		writeError(w, r, saasOff())
		return
	}
	if _, err := s.d.Saas.ClearOrganizationOverrides(r.Context(), &plansv1.ClearOrganizationOverridesRequest{AccountId: r.PathValue("account_id")}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// hiddenPlatformTenant is the id of the platform tenant while the mode is on, so the operator's tenant list can leave it out ("" when off or not known). The
// platform tenant never changes once it exists, so it is remembered; before it exists it is asked for again after a moment.
func (s *Server) hiddenPlatformTenant(ctx context.Context) string {
	if s.d.Saas == nil {
		return ""
	}
	return s.saasEdge.platformTenant(ctx, s.d.Saas)
}

// saasEdge is the gateway's memory for the ingest edge: the platform tenant, the ingest policy of each tenant and whether an organisation may still ingest.
// All of it is a cache of answers that change rarely, so a request seldom waits for plans-svc or limits-svc, and all of it fails open: SaaS limits must never
// take ingest down.
type saasEdge struct {
	mu       sync.Mutex
	platform string
	checked  time.Time
	policies map[string]policyEntry
	allowed  map[string]allowEntry
	now      func() time.Time
}

type policyEntry struct {
	policy *plansv1.ResolveIngestPolicyResponse
	until  time.Time
}

type allowEntry struct {
	allowed bool
	until   time.Time
}

// Lifetimes of the cache entries. The policy only changes when an account is made or a tenant is added; the allowance moves with the usage, so it is
// kept short (ADR 0006: the counters are eventually consistent anyway).
const (
	policyTTL      = 30 * time.Second
	policyErrorTTL = 5 * time.Second
	allowTTL       = 5 * time.Second
	platformTTL    = 10 * time.Second
	maxEdgeEntries = 10_000
)

func newSaasEdge() *saasEdge {
	return &saasEdge{policies: map[string]policyEntry{}, allowed: map[string]allowEntry{}, now: time.Now}
}

func (e *saasEdge) platformTenant(ctx context.Context, c plansv1.SaasServiceClient) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.platform != "" || e.now().Sub(e.checked) < platformTTL {
		return e.platform
	}
	e.checked = e.now()
	if resp, err := c.GetPlatform(ctx, &plansv1.GetPlatformRequest{}); err == nil && resp.GetEnabled() {
		e.platform = resp.GetTenant().GetId()
	}
	return e.platform
}

// saasPolicy returns the ingest policy of a tenant, or nil when its events are not metered (or the answer is not known: then they are not limited either).
func (s *Server) saasPolicy(ctx context.Context, tenantID string) *plansv1.ResolveIngestPolicyResponse {
	if s.d.Saas == nil {
		return nil
	}
	e := s.saasEdge
	e.mu.Lock()
	if hit, ok := e.policies[tenantID]; ok && e.now().Before(hit.until) {
		e.mu.Unlock()
		return hit.policy
	}
	e.mu.Unlock()
	resp, err := s.d.Saas.ResolveIngestPolicy(ctx, &plansv1.ResolveIngestPolicyRequest{TenantId: tenantID})
	ttl := policyTTL
	var policy *plansv1.ResolveIngestPolicyResponse
	if err != nil {
		s.d.Log.WarnContext(ctx, "saas: ingest policy unavailable; events are not limited", "tenant_id", tenantID, "error", err)
		ttl = policyErrorTTL
	} else if resp.GetMetered() {
		policy = resp
	}
	e.mu.Lock()
	if len(e.policies) >= maxEdgeEntries {
		clear(e.policies)
	}
	e.policies[tenantID] = policyEntry{policy: policy, until: e.now().Add(ttl)}
	e.mu.Unlock()
	return policy
}

// saasMayIngest says whether the organisation behind the policy is within its events quota. A hard limit that is reached refuses; a soft or notify limit, no
// limit and any doubt allow.
func (s *Server) saasMayIngest(ctx context.Context, pol *plansv1.ResolveIngestPolicyResponse) bool {
	e := s.saasEdge
	e.mu.Lock()
	if hit, ok := e.allowed[pol.GetCustomerId()]; ok && e.now().Before(hit.until) {
		e.mu.Unlock()
		return hit.allowed
	}
	e.mu.Unlock()
	pctx, cancel, err := platformContext(ctx, pol.GetPlatform(), saasTimeout)
	if err != nil {
		return true
	}
	defer cancel()
	allowed := true
	if resp, err := s.d.Limits.CheckEntitlement(pctx, &limitsv1.CheckEntitlementRequest{CustomerId: pol.GetCustomerId(), MeterCode: pol.GetMeterCode()}); err != nil {
		s.d.Log.WarnContext(ctx, "saas: events quota unavailable; events are not limited", "account_id", pol.GetAccountId(), "error", err)
	} else {
		allowed = resp.GetStatus().GetAllowed()
	}
	e.mu.Lock()
	if len(e.allowed) >= maxEdgeEntries {
		clear(e.allowed)
	}
	e.allowed[pol.GetCustomerId()] = allowEntry{allowed: allowed, until: e.now().Add(allowTTL)}
	e.mu.Unlock()
	return allowed
}

// errEventsQuota is the refusal of events over the plan's hard events quota.
func errEventsQuota() error {
	return errkey.New(codes.ResourceExhausted, "gateway.saas.events_quota", "the organisation's plan allows no more events this month: upgrade the plan to continue")
}

// saasMeter counts the events a request had accepted into the platform tenant, on the organisation's customer row. The key is derived from the first accepted
// event's id, which is unique and which a retry of the same batch does not repeat (the retried events are duplicates and are not counted), so a metering
// that is retried or replayed is counted once. A failure is logged and does not fail the request: the events are accepted and a missed count only
// lets an organisation slip a little past its limit.
func (s *Server) saasMeter(ctx context.Context, pol *plansv1.ResolveIngestPolicyResponse, tenantID string, results []*ingestv1.EventResult) {
	n, first := 0, ""
	for _, r := range results {
		if r.GetStatus() == ingestv1.EventResult_STATUS_ACCEPTED {
			if n == 0 {
				first = r.GetEventId()
			}
			n++
		}
	}
	if n == 0 {
		return
	}
	pctx, cancel, err := platformContext(ctx, pol.GetPlatform(), saasTimeout)
	if err != nil {
		return
	}
	defer cancel()
	ev := &ingestv1.IncomingEvent{
		IdempotencyKey: "saas-events:" + first, CustomerExternalId: pol.GetCustomerExternalId(), MeterCode: pol.GetMeterCode(), Quantity: strconv.Itoa(n),
		EventTime: timestamppb.New(s.now()), Properties: map[string]string{"tenant_id": tenantID},
	}
	resp, err := s.d.Ingest.IngestEvents(pctx, &ingestv1.IngestEventsRequest{Events: []*ingestv1.IncomingEvent{ev}})
	if err != nil || len(resp.GetResults()) != 1 || resp.GetResults()[0].GetStatus() == ingestv1.EventResult_STATUS_REJECTED {
		s.d.Log.WarnContext(ctx, "saas: events were accepted but not metered", "account_id", pol.GetAccountId(), "events", n, "error", err, "result", resp.GetResults())
	}
}
