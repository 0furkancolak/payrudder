package delivery

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/mailx/fakenafru"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
)

// memNotes is the dedup record in memory.
type memNotes struct {
	mu       sync.Mutex
	sent     map[string]string
	claimed  map[string]bool
	released []string
	tenants  map[string]uuid.UUID
}

func newNotes() *memNotes {
	return &memNotes{sent: map[string]string{}, claimed: map[string]bool{}, tenants: map[string]uuid.UUID{}}
}

func (n *memNotes) Claim(_ context.Context, key string, _ *uuid.UUID, tenantID uuid.UUID, _, _, _ string) (store.Claim, string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if id, ok := n.sent[key]; ok {
		return store.AlreadySent, id, nil
	}
	n.claimed[key] = true
	n.tenants[key] = tenantID
	return store.Claimed, "", nil
}
func (n *memNotes) MarkSent(_ context.Context, key, id string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent[key] = id
	return nil
}
func (n *memNotes) Release(_ context.Context, key string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.claimed, key)
	n.released = append(n.released, key)
	return nil
}

type setSuppressed map[string]bool

func (s setSuppressed) IsSuppressed(_ context.Context, _ uuid.UUID, a string) (bool, error) {
	return s[a], nil
}

type fakeQuota struct {
	allow   bool
	asked   int
	counted []string
}

func (q *fakeQuota) Allow(context.Context, uuid.UUID) bool { q.asked++; return q.allow }
func (q *fakeQuota) Count(_ context.Context, _ uuid.UUID, key string) {
	q.counted = append(q.counted, key)
}

type rig struct {
	notes *memNotes
	nafru *fakenafru.Server
	quota *fakeQuota
	m     *Mailer
	tid   uuid.UUID
}

func newRig(t *testing.T) *rig {
	t.Helper()
	f := fakenafru.New()
	t.Cleanup(f.Close)
	r := &rig{notes: newNotes(), nafru: f, quota: &fakeQuota{allow: true}, tid: uuid.New()}
	r.m = &Mailer{Default: mailx.Nafru{Resend: mailx.Resend{BaseURL: f.URL, APIKey: "k"}}, From: "Payrudder <b@payrudder.test>", Notes: r.notes, Quota: r.quota}
	return r
}

func (r *rig) deliver(key, to string, sandbox bool) (string, error) {
	return r.m.Deliver(context.Background(), Delivery{Key: key, Kind: "command", Template: "invoice_paid.v1", TenantID: r.tid, To: to, Data: map[string]string{"customer_name": "Ada"}, Sandbox: sandbox})
}

func TestSuppressedAddressIsDroppedWithoutSending(t *testing.T) {
	r := newRig(t)
	r.m.Suppress = setSuppressed{"gone@example.com": true}
	id, err := r.deliver("k1", "gone@example.com", false)
	if err != nil || id != Dropped {
		t.Fatalf("a suppressed address: %q %v", id, err)
	}
	if len(r.nafru.Sent()) != 0 || len(r.notes.released) != 1 || r.quota.asked != 0 || len(r.quota.counted) != 0 {
		t.Errorf("sent=%d released=%v quota asked=%d counted=%v: nothing may go out or be counted", len(r.nafru.Sent()), r.notes.released, r.quota.asked, r.quota.counted)
	}
	if id, err = r.deliver("k2", "fine@example.com", false); err != nil || id == Dropped || len(r.nafru.Sent()) != 1 {
		t.Errorf("another address still gets its mail: %q %v", id, err)
	}
}

func TestSuppressionListFailureRetries(t *testing.T) {
	r := newRig(t)
	r.m.Suppress = failingSuppress{}
	if id, err := r.deliver("k1", "a@example.com", false); err == nil || id != "" || len(r.nafru.Sent()) != 0 {
		t.Errorf("an unreadable suppression list must retry, not send: %q %v", id, err)
	}
}

type failingSuppress struct{}

func (failingSuppress) IsSuppressed(context.Context, uuid.UUID, string) (bool, error) {
	return false, errors.New("db down")
}

func TestQuotaDropsAndCountsOnlyThePlatformSendersLiveMail(t *testing.T) {
	r := newRig(t)
	if _, err := r.deliver("k1", "a@example.com", false); err != nil {
		t.Fatal(err)
	}
	if len(r.quota.counted) != 1 || r.quota.counted[0] != "k1" {
		t.Errorf("a sent mail is counted once under its delivery key: %v", r.quota.counted)
	}
	r.quota.allow = false
	if id, err := r.deliver("k2", "a@example.com", false); err != nil || id != Dropped || len(r.nafru.Sent()) != 1 || len(r.quota.counted) != 1 {
		t.Errorf("an allowance used up drops the mail: %q %v sent=%d counted=%v", id, err, len(r.nafru.Sent()), r.quota.counted)
	}
	// Sandbox mail is not limited or counted, and neither is mail through the tenant's own provider.
	r.quota.asked, r.quota.counted = 0, nil
	r.m.Sandbox = mailx.Nafru{Resend: mailx.Resend{BaseURL: r.nafru.URL, APIKey: "k"}}
	if _, err := r.deliver("k3", "a@example.com", true); err != nil {
		t.Fatal(err)
	}
	own := fakenafru.New()
	defer own.Close()
	r.m.Providers = ownProvider{Provider{Sender: mailx.Nafru{Resend: mailx.Resend{BaseURL: own.URL, APIKey: "own"}}, From: "Acme <a@acme.test>"}}
	if _, err := r.deliver("k4", "a@example.com", false); err != nil {
		t.Fatal(err)
	}
	if r.quota.asked != 0 || len(r.quota.counted) != 0 || len(own.Sent()) != 1 {
		t.Errorf("sandbox and own-provider mail are free of the allowance: asked=%d counted=%v own sent=%d", r.quota.asked, r.quota.counted, len(own.Sent()))
	}
	// A mail that names no tenant (the platform's own) is neither.
	if _, err := r.m.Deliver(context.Background(), Delivery{Key: "k5", Kind: "limit", Template: "invoice_paid.v1", To: "a@example.com"}); err != nil || r.quota.asked != 0 {
		t.Errorf("a mail without a tenant: asked=%d err=%v", r.quota.asked, err)
	}
}

type ownProvider struct{ p Provider }

func (o ownProvider) For(context.Context, uuid.UUID) (*Provider, error) { return &o.p, nil }

func TestRateLimitedMailIsRetriedNotDropped(t *testing.T) {
	r := newRig(t)
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	r.m.Limit = &Limiter{PerMinute: 60, Burst: 2, Now: func() time.Time { return now }}
	for _, k := range []string{"k1", "k2"} {
		if _, err := r.deliver(k, "a@example.com", false); err != nil {
			t.Fatal(err)
		}
	}
	_, err := r.deliver("k3", "a@example.com", false)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("the third mail of a burst of two: %v", err)
	}
	if errors.Is(err, workerx.ErrPoison) {
		t.Fatal("a rate-limited mail must never be poison: it would go to a dead-letter queue")
	}
	if len(r.nafru.Sent()) != 2 || len(r.notes.released) != 1 || r.notes.released[0] != "k3" || len(r.quota.counted) != 2 {
		t.Errorf("sent=%d released=%v counted=%v: the refused mail leaves no claim and no count", len(r.nafru.Sent()), r.notes.released, r.quota.counted)
	}
	now = now.Add(time.Second) // a token is back: the retry sends it
	if id, err := r.deliver("k3", "a@example.com", false); err != nil || id == Dropped || len(r.nafru.Sent()) != 3 {
		t.Errorf("the retry: %q %v", id, err)
	}
}

func TestAlreadySentMailIsNotRefusedAgain(t *testing.T) {
	r := newRig(t)
	if _, err := r.deliver("k1", "a@example.com", false); err != nil {
		t.Fatal(err)
	}
	r.quota.allow = false
	r.m.Suppress = setSuppressed{"a@example.com": true}
	if id, err := r.deliver("k1", "a@example.com", false); err != nil || id != "fake-1" {
		t.Errorf("a redelivery of a mail that went out answers with its id, not 'dropped': %q %v", id, err)
	}
}

// --- the quota against fakes of plans-svc, limits-svc and ingest-svc

type fakePolicy struct {
	calls int
	err   error
	resp  *plansv1.ResolveIngestPolicyResponse
}

func (f *fakePolicy) ResolveIngestPolicy(_ context.Context, _ *plansv1.ResolveIngestPolicyRequest, _ ...grpc.CallOption) (*plansv1.ResolveIngestPolicyResponse, error) {
	f.calls++
	return f.resp, f.err
}

type fakeLimits struct {
	calls   int
	err     error
	allowed bool
	meter   string
	tenant  tenant.Info
}

func (f *fakeLimits) CheckEntitlement(ctx context.Context, in *limitsv1.CheckEntitlementRequest, _ ...grpc.CallOption) (*limitsv1.CheckEntitlementResponse, error) {
	f.calls++
	f.meter = in.GetMeterCode()
	f.tenant, _ = tenant.FromContext(ctx)
	if f.err != nil {
		return nil, f.err
	}
	return &limitsv1.CheckEntitlementResponse{Status: &limitsv1.LimitStatus{Allowed: f.allowed}}, nil
}

type fakeIngest struct {
	calls  []*ingestv1.IncomingEvent
	tenant tenant.Info
	err    error
}

func (f *fakeIngest) IngestEvents(ctx context.Context, in *ingestv1.IngestEventsRequest, _ ...grpc.CallOption) (*ingestv1.IngestEventsResponse, error) {
	f.calls = append(f.calls, in.GetEvents()...)
	f.tenant, _ = tenant.FromContext(ctx)
	if f.err != nil {
		return nil, f.err
	}
	return &ingestv1.IngestEventsResponse{Results: []*ingestv1.EventResult{{Status: ingestv1.EventResult_STATUS_ACCEPTED}}}, nil
}

const platformID = "0193c000-0000-7000-8000-0000000000f1"

func meteredPolicy() *plansv1.ResolveIngestPolicyResponse {
	return &plansv1.ResolveIngestPolicyResponse{Metered: true, AccountId: "acct", CustomerId: "cust-1", CustomerExternalId: "acct",
		Platform: &plansv1.PlatformTenant{Id: platformID, Environment: "live", Timezone: "UTC"}}
}

func newQuota(p *fakePolicy, l *fakeLimits, i *fakeIngest, now *time.Time) *SaasQuota {
	return &SaasQuota{Plans: p, Limits: l, Ingest: i, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return *now }}
}

func TestSaasQuotaAsksLimitsAsThePlatformTenantAndCaches(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	p, l, i := &fakePolicy{resp: meteredPolicy()}, &fakeLimits{allowed: false}, &fakeIngest{}
	q := newQuota(p, l, i, &now)
	tid := uuid.New()
	if q.Allow(context.Background(), tid) {
		t.Error("a hard limit that is reached must refuse")
	}
	if l.meter != "mails" || l.tenant.ID.String() != platformID {
		t.Errorf("the allowance is the mails meter read as the platform tenant: %q %v", l.meter, l.tenant.ID)
	}
	q.Allow(context.Background(), tid)
	if p.calls != 1 || l.calls != 1 {
		t.Errorf("answers are remembered: %d policy reads, %d allowance reads", p.calls, l.calls)
	}
	now = now.Add(6 * time.Second)
	l.allowed = true
	if !q.Allow(context.Background(), tid) || l.calls != 2 || p.calls != 1 {
		t.Errorf("the allowance is read again after a few seconds, the policy is not: policy %d allowance %d", p.calls, l.calls)
	}
}

func TestSaasQuotaFailsOpen(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	ctx := context.Background()
	// plans-svc down: unknown, so not limited and not counted.
	i := &fakeIngest{}
	q := newQuota(&fakePolicy{err: errors.New("down")}, &fakeLimits{}, i, &now)
	if !q.Allow(ctx, uuid.New()) {
		t.Error("an unknown policy must not stop mail")
	}
	q.Count(ctx, uuid.New(), "k")
	if len(i.calls) != 0 {
		t.Error("an unknown policy must not meter")
	}
	// Not metered (SaaS off, sandbox, the platform's own tenant): the same.
	q = newQuota(&fakePolicy{resp: &plansv1.ResolveIngestPolicyResponse{}}, &fakeLimits{}, i, &now)
	tid := uuid.New()
	q.Count(ctx, tid, "k")
	if !q.Allow(ctx, tid) || len(i.calls) != 0 {
		t.Error("a tenant that is not metered is neither limited nor counted")
	}
	// limits-svc down: allowed. ingest-svc down: Count must not panic or block.
	q = newQuota(&fakePolicy{resp: meteredPolicy()}, &fakeLimits{err: errors.New("down")}, &fakeIngest{err: errors.New("down")}, &now)
	if !q.Allow(ctx, uuid.New()) {
		t.Error("an unreadable allowance must not stop mail")
	}
	q.Count(ctx, uuid.New(), "k")
}

func TestSaasQuotaCountsOneMailPerKeyInThePlatformTenant(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	p, i := &fakePolicy{resp: meteredPolicy()}, &fakeIngest{}
	q := newQuota(p, &fakeLimits{}, i, &now)
	tid := uuid.New()
	q.Count(context.Background(), tid, "cmd-1")
	if len(i.calls) != 1 || i.tenant.ID.String() != platformID {
		t.Fatalf("ingested %d events as %v", len(i.calls), i.tenant.ID)
	}
	e := i.calls[0]
	if e.GetIdempotencyKey() != "saas-mails:cmd-1" || e.GetMeterCode() != "mails" || e.GetQuantity() != "1" || e.GetCustomerExternalId() != "acct" || e.GetProperties()["tenant_id"] != tid.String() {
		t.Errorf("event = %v", e)
	}
}
