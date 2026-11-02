package delivery

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// Quota is the allowance of mails a plan includes (the `mails` meter of the platform tenant, ADR 0024). Both calls fail open: a quota that cannot be read or a
// count that cannot be written never stops or fails a mail (ADR 0006: the counters are eventually consistent, and a lost count only lets an organisation slip a
// little past its allowance).
type Quota interface {
	// Allow reports whether the organisation behind the tenant may send one more mail this month. False only when its plan's hard limit is reached.
	Allow(ctx context.Context, tenantID uuid.UUID) bool
	// Count meters one sent mail, idempotently under key.
	Count(ctx context.Context, tenantID uuid.UUID, key string)
}

// MailsMeter is the platform-tenant meter the mails are counted on (plans-svc creates it, see saas/model).
const MailsMeter = "mails"

// SaasPolicy, SaasLimits and SaasIngest are the parts of plans-svc, limits-svc and ingest-svc the quota uses.
type (
	SaasPolicy interface {
		ResolveIngestPolicy(ctx context.Context, in *plansv1.ResolveIngestPolicyRequest, opts ...grpc.CallOption) (*plansv1.ResolveIngestPolicyResponse, error)
	}
	SaasLimits interface {
		CheckEntitlement(ctx context.Context, in *limitsv1.CheckEntitlementRequest, opts ...grpc.CallOption) (*limitsv1.CheckEntitlementResponse, error)
	}
	SaasIngest interface {
		IngestEvents(ctx context.Context, in *ingestv1.IngestEventsRequest, opts ...grpc.CallOption) (*ingestv1.IngestEventsResponse, error)
	}
)

// Lifetimes of the cache entries, as in the gateway's ingest edge: who pays for a tenant rarely changes, whether the allowance is used up moves with every mail.
const (
	policyTTL      = 30 * time.Second
	policyErrorTTL = 5 * time.Second
	allowTTL       = 5 * time.Second
	quotaTimeout   = 4 * time.Second
)

type policyEntry struct {
	policy *plansv1.ResolveIngestPolicyResponse // nil: the tenant is not metered (or not known)
	until  time.Time
}

type allowEntry struct {
	allowed bool
	until   time.Time
}

// SaasQuota implements Quota the way the gateway meters events: the policy of a tenant (who is its organisation, as a customer of the platform tenant) comes from
// plans-svc, the allowance from limits-svc's CheckEntitlement, and every sent mail is one event of the `mails` meter ingested into the platform tenant. Thresholds
// and alerts are the quota engine's: nothing here stores a count. With SaaS off plans-svc answers "not metered" and the quota never refuses.
type SaasQuota struct {
	Plans  SaasPolicy
	Limits SaasLimits
	Ingest SaasIngest
	Log    *slog.Logger
	// Now is the clock; time.Now when nil.
	Now func() time.Time

	mu       sync.Mutex
	policies map[uuid.UUID]policyEntry
	allowed  map[string]allowEntry
}

func (q *SaasQuota) now() time.Time {
	if q.Now != nil {
		return q.Now()
	}
	return time.Now()
}

// policy returns the tenant's metering policy, or nil when its mail is not metered or the answer is not known.
func (q *SaasQuota) policy(ctx context.Context, tenantID uuid.UUID) *plansv1.ResolveIngestPolicyResponse {
	q.mu.Lock()
	if hit, ok := q.policies[tenantID]; ok && q.now().Before(hit.until) {
		q.mu.Unlock()
		return hit.policy
	}
	q.mu.Unlock()
	resp, err := q.Plans.ResolveIngestPolicy(ctx, &plansv1.ResolveIngestPolicyRequest{TenantId: tenantID.String()})
	ttl := policyTTL
	var pol *plansv1.ResolveIngestPolicyResponse
	if err != nil {
		q.Log.WarnContext(ctx, "mail quota: policy unavailable; the mail is not limited or counted", "tenant_id", tenantID, "error", err)
		ttl = policyErrorTTL
	} else if resp.GetMetered() {
		pol = resp
	}
	q.mu.Lock()
	if q.policies == nil || len(q.policies) >= maxCached {
		q.policies = make(map[uuid.UUID]policyEntry)
	}
	q.policies[tenantID] = policyEntry{policy: pol, until: q.now().Add(ttl)}
	q.mu.Unlock()
	return pol
}

// platformContext is ctx acting as the platform tenant, for at most quotaTimeout.
func platformContext(ctx context.Context, p *plansv1.PlatformTenant) (context.Context, context.CancelFunc, bool) {
	id, err := uuid.Parse(p.GetId())
	if err != nil {
		return ctx, func() {}, false
	}
	ctx, cancel := context.WithTimeout(tenant.NewContext(ctx, tenant.Info{ID: id, Environment: p.GetEnvironment(), Timezone: p.GetTimezone()}), quotaTimeout)
	return ctx, cancel, true
}

// Allow implements Quota.
func (q *SaasQuota) Allow(ctx context.Context, tenantID uuid.UUID) bool {
	pol := q.policy(ctx, tenantID)
	if pol == nil {
		return true
	}
	q.mu.Lock()
	if hit, ok := q.allowed[pol.GetCustomerId()]; ok && q.now().Before(hit.until) {
		q.mu.Unlock()
		return hit.allowed
	}
	q.mu.Unlock()
	pctx, cancel, ok := platformContext(ctx, pol.GetPlatform())
	defer cancel()
	if !ok {
		return true
	}
	allowed := true
	if resp, err := q.Limits.CheckEntitlement(pctx, &limitsv1.CheckEntitlementRequest{CustomerId: pol.GetCustomerId(), MeterCode: MailsMeter}); err != nil {
		q.Log.WarnContext(ctx, "mail quota: allowance unavailable; the mail is not limited", "account_id", pol.GetAccountId(), "error", err)
	} else {
		allowed = resp.GetStatus().GetAllowed()
	}
	q.mu.Lock()
	if q.allowed == nil || len(q.allowed) >= maxCached {
		q.allowed = make(map[string]allowEntry)
	}
	q.allowed[pol.GetCustomerId()] = allowEntry{allowed: allowed, until: q.now().Add(allowTTL)}
	q.mu.Unlock()
	return allowed
}

// Count implements Quota. The idempotency key is derived from the delivery's dedup key, so a mail that is sent again after a lost answer, or a metering that is
// retried, is counted once.
func (q *SaasQuota) Count(ctx context.Context, tenantID uuid.UUID, key string) {
	pol := q.policy(ctx, tenantID)
	if pol == nil {
		return
	}
	pctx, cancel, ok := platformContext(ctx, pol.GetPlatform())
	defer cancel()
	if !ok {
		return
	}
	ev := &ingestv1.IncomingEvent{
		IdempotencyKey: "saas-mails:" + key, CustomerExternalId: pol.GetCustomerExternalId(), MeterCode: MailsMeter, Quantity: "1",
		EventTime: timestamppb.New(q.now()), Properties: map[string]string{"tenant_id": tenantID.String()},
	}
	resp, err := q.Ingest.IngestEvents(pctx, &ingestv1.IngestEventsRequest{Events: []*ingestv1.IncomingEvent{ev}})
	if err != nil || len(resp.GetResults()) != 1 || resp.GetResults()[0].GetStatus() == ingestv1.EventResult_STATUS_REJECTED {
		q.Log.WarnContext(ctx, "mail quota: a mail was sent but not metered", "account_id", pol.GetAccountId(), "error", err, "result", resp.GetResults())
	}
}
