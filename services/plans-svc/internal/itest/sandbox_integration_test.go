//go:build integration

package itest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/sandboxreset"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/sandbox"
)

// recordingRemotes stands in for ingest-svc, aggregator, analytics-svc, billing-svc and payments-worker (each has its own test against its own database): it records the
// tenants it was asked to empty, in order, and can be made to fail or to be "not configured".
type recordingRemotes struct {
	mu      sync.Mutex
	calls   []string // "<service>:<tenant>"
	failing map[string]error
	missing map[string]bool
}

var remoteNames = []string{"ingest", "aggregator", "analytics", "billing", "payments"}

func (r *recordingRemotes) steps() []sandbox.Step {
	var out []sandbox.Step
	for _, name := range remoteNames {
		out = append(out, sandbox.Step{Name: name, Run: func(_ context.Context, id uuid.UUID) (map[string]int64, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.missing[name] {
				return nil, errors.New("must not be called: not configured")
			}
			r.calls = append(r.calls, name+":"+id.String())
			if err := r.failing[name]; err != nil {
				return nil, err
			}
			return map[string]int64{name + "_rows": 1}, nil
		}})
	}
	return out
}

func (r *recordingRemotes) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls, r.failing = nil, nil
}

func (r *recordingRemotes) called() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *recordingRemotes) fail(name string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failing == nil {
		r.failing = map[string]error{}
	}
	r.failing[name] = err
}

// ageResets makes every reset of the app older than the cooldown.
func (e *env) ageResets(t *testing.T, appID string) {
	t.Helper()
	if _, err := e.system.Exec(context.Background(), `UPDATE sandbox_resets SET created_at = created_at - interval '3 hours', finished_at = finished_at - interval '3 hours' WHERE app_id = $1::uuid`, appID); err != nil {
		t.Fatal(err)
	}
}

func stepState(r *plansv1.SandboxReset, name string) *plansv1.SandboxResetStep {
	for _, s := range r.GetSteps() {
		if s.GetName() == name {
			return s
		}
	}
	return nil
}

// testSandboxReset: a reset empties a sandbox's customer side in every service, in order, keeps its configuration and leaves the live environment alone; one at a
// time per sandbox and not twice in a row; a failing service stops the job where it failed and a new request finishes it; only owners and admins ask, and only a sandbox
// can be reset.
func testSandboxReset(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.verified(t, "reset-owner@example.com", "Reset Co")
	created, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: owner.id, AccountId: owner.account, Name: "Resettable", Currency: "TRY", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	app := created.GetApp()
	live, sandboxT := envOf(app, plansv1.Environment_ENVIRONMENT_LIVE), envOf(app, plansv1.Environment_ENVIRONMENT_SANDBOX)
	e.activate(t, live.GetId()) // so that its own data includes a webhook endpoint
	seed(t, e, sandboxT.GetId(), "sbx")
	seed(t, e, live.GetId(), "liv")
	liveBefore := fingerprint(t, e, live.GetId())
	sbxBefore := fingerprint(t, e, sandboxT.GetId())
	status := func() *plansv1.SandboxReset {
		t.Helper()
		r, err := e.ident.GetMySandboxReset(ctx, &plansv1.GetMySandboxResetRequest{UserId: owner.id, AppId: app.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetSandboxReset()
	}
	ask := func(uid string) (*plansv1.SandboxReset, error) {
		r, err := e.ident.ResetMySandbox(ctx, &plansv1.ResetMySandboxRequest{UserId: uid, AppId: app.GetId()})
		return r.GetSandboxReset(), err
	}

	if status() != nil {
		t.Fatal("a sandbox that was never reset has a reset")
	}

	// Who may ask.
	viewer := e.signUp(t, "reset-viewer@example.com", "Viewer Co")
	if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'viewer')`, owner.account, viewer.GetUser().GetId()); err != nil {
		t.Fatal(err)
	}
	_, err = ask(viewer.GetUser().GetId())
	wantCode(t, "a viewer", err, codes.PermissionDenied, "identity.account.forbidden")
	stranger := e.verified(t, "reset-stranger@example.com", "Stranger Co")
	_, err = ask(stranger.id)
	wantCode(t, "a stranger", err, codes.NotFound, "identity.app.not_found")

	// Queued: every step is listed, pending, and nothing has happened.
	queued, err := ask(owner.id)
	if err != nil {
		t.Fatal(err)
	}
	if queued.GetState() != sandbox.StateQueued || queued.GetKind() != "manual" || len(queued.GetSteps()) != 6 || queued.GetSteps()[0].GetName() != "plans" || queued.GetSteps()[5].GetName() != "payments" {
		t.Fatalf("queued = %v", queued)
	}
	for _, s := range queued.GetSteps() {
		if s.GetState() != "pending" {
			t.Errorf("step %v is not pending", s)
		}
	}
	// (The job's own row is a row of the sandbox's tenant too; everything else must be as it was.)
	sbxNow := fingerprint(t, e, sandboxT.GetId())
	delete(sbxNow, "sandbox_resets")
	delete(sbxBefore, "sandbox_resets")
	if !equalFingerprints(sbxBefore, sbxNow) {
		t.Fatalf("asking for a reset changed the sandbox before the runner ran: %s", diffFingerprints(sbxBefore, sbxNow))
	}
	// Asking again while it waits is the same job.
	if again, err := ask(owner.id); err != nil || again.GetId() != queued.GetId() {
		t.Errorf("a second request while one is open = %v, %v; want the same job", again, err)
	}

	ran, err := e.runner.RunOnce(ctx)
	if err != nil || !ran {
		t.Fatalf("run = %v, %v", ran, err)
	}
	done := status()
	if done.GetId() != queued.GetId() || done.GetState() != sandbox.StateDone || done.GetFinishTime() == nil {
		t.Fatalf("after the run: %v", done)
	}
	for _, s := range done.GetSteps() {
		if s.GetState() != "done" {
			t.Errorf("step %v is not done", s)
		}
	}
	if stepState(done, "plans").GetDeleted()["customers"] != 1 || stepState(done, "plans").GetDeleted()["subscriptions"] != 1 {
		t.Errorf("plans step = %v", stepState(done, "plans"))
	}
	// Every other service was asked about the sandbox and about no other tenant, plans-svc's own part first.
	want := []string{"ingest", "aggregator", "analytics", "billing", "payments"}
	got := e.remotes.called()
	if len(got) != len(want) {
		t.Fatalf("remote calls = %v", got)
	}
	for i, name := range want {
		if got[i] != name+":"+sandboxT.GetId() {
			t.Errorf("call %d = %s, want %s:%s", i, got[i], name, sandboxT.GetId())
		}
	}

	// What went and what stayed.
	sctx := as(sandboxT.GetId())
	if r, _ := e.plans.ListCustomers(sctx, &plansv1.ListCustomersRequest{}); len(r.GetCustomers()) != 0 {
		t.Errorf("customers after the reset: %v", r.GetCustomers())
	}
	if r, _ := e.plans.ListSubscriptions(sctx, &plansv1.ListSubscriptionsRequest{}); len(r.GetSubscriptions()) != 0 {
		t.Errorf("subscriptions after the reset: %v", r.GetSubscriptions())
	}
	if m, err := e.plans.GetMeter(sctx, &plansv1.GetMeterRequest{Code: "gb_hours"}); err != nil || m.GetMeter().GetCode() == "" {
		t.Errorf("the meter went: %v", err)
	}
	if v, err := e.plans.GetPlanVersion(sctx, &plansv1.GetPlanVersionRequest{PlanCode: "pro"}); err != nil || v.GetPlanVersion().GetBaseFeeMinor() != 9900 {
		t.Errorf("the plan went: %v", err)
	}
	if r, _ := e.plans.GetTaxRates(sctx, &plansv1.GetTaxRatesRequest{}); len(r.GetRates()) != 1 {
		t.Errorf("tax rates after the reset: %v", r.GetRates())
	}
	if r, _ := e.plans.ListCoupons(sctx, &plansv1.ListCouponsRequest{}); len(r.GetCoupons()) != 1 || r.GetCoupons()[0].GetRedeemed() != 0 {
		t.Errorf("coupons after the reset: %v (the redemption count starts again with the ledger)", r.GetCoupons())
	}
	if r, _ := e.hooks.ListWebhookEndpoints(sctx, &plansv1.ListWebhookEndpointsRequest{}); len(r.GetEndpoints()) != 1 {
		t.Errorf("webhook endpoints after the reset: %v", r.GetEndpoints())
	}
	if !equalFingerprints(liveBefore, fingerprint(t, e, live.GetId())) {
		t.Errorf("the live environment changed: %s", diffFingerprints(liveBefore, fingerprint(t, e, live.GetId())))
	}

	// Not twice in a row.
	_, err = ask(owner.id)
	wantCode(t, "right after a reset", err, codes.ResourceExhausted, "identity.sandbox.reset_rate_limited")
	e.ageResets(t, app.GetId())

	// A service that fails stops the job there; the earlier steps are done, the later ones untouched; asking again after the cooldown repeats it all and finishes.
	e.remotes.reset()
	e.remotes.fail("billing", errors.New("billing-svc is down"))
	if _, err := ask(owner.id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	failed := status()
	if failed.GetState() != sandbox.StateFailed || failed.GetError() == "" || stepState(failed, "billing").GetState() != "failed" || stepState(failed, "billing").GetError() == "" {
		t.Fatalf("failed job = %v", failed)
	}
	if stepState(failed, "plans").GetState() != "done" || stepState(failed, "ingest").GetState() != "done" || stepState(failed, "payments").GetState() != "pending" {
		t.Errorf("steps = %v", failed.GetSteps())
	}
	for _, c := range e.remotes.called() {
		if c == "payments:"+sandboxT.GetId() {
			t.Error("a step after the failed one ran")
		}
	}
	e.ageResets(t, app.GetId())
	e.remotes.reset()
	if _, err := ask(owner.id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if again := status(); again.GetState() != sandbox.StateDone || len(e.remotes.called()) != 5 {
		t.Errorf("the retry = %v, calls %v", again, e.remotes.called())
	}

	// Nothing to run: the runner says so.
	if ran, err := e.runner.RunOnce(ctx); err != nil || ran {
		t.Errorf("an empty queue ran %v, %v", ran, err)
	}

	// Only a sandbox can be reset: the database says so too.
	if _, err := e.system.Exec(ctx, `INSERT INTO sandbox_resets (id, app_id, tenant_id, kind) VALUES (gen_random_uuid(), $1::uuid, $2::uuid, 'manual')`, app.GetId(), live.GetId()); err == nil {
		t.Error("a reset of a live tenant was queued")
	}

	// Every table of plans_db that holds tenant rows is deleted from or kept on purpose.
	missing, err := sandboxreset.MissingTables(ctx, e.system, sandbox.Tables, sandbox.Kept)
	if err != nil || len(missing) != 0 {
		t.Errorf("tables a reset does not cover: %v, %v", missing, err)
	}
}

// testSandboxRetention: a sandbox untouched for the retention period is queued for a reset by the sweep, once; one in use, or just reset, is not.
func testSandboxRetention(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	idle := e.account(t, "Idle Co")[plansv1.Environment_ENVIRONMENT_SANDBOX].GetId()
	busy := e.account(t, "Busy Co")[plansv1.Environment_ENVIRONMENT_SANDBOX].GetId()
	keyed := e.account(t, "Keyed Co")[plansv1.Environment_ENVIRONMENT_SANDBOX].GetId()
	for _, id := range []string{idle, busy, keyed} {
		if _, err := e.system.Exec(ctx, `UPDATE tenants SET created_at = now() - interval '200 days' WHERE id = $1::uuid`, id); err != nil {
			t.Fatal(err)
		}
	}
	// A customer made yesterday keeps a sandbox alive, and so does a key used last week.
	if _, err := e.plans.CreateCustomer(as(busy), &plansv1.CreateCustomerRequest{ExternalId: "c", Name: "N", Email: "n@example.com"}); err != nil {
		t.Fatal(err)
	}
	k, err := e.admin.CreateApiKey(operator(), &plansv1.CreateApiKeyRequest{TenantId: keyed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.system.Exec(ctx, `UPDATE api_keys SET last_used_at = now() - interval '7 days' WHERE id = $1::uuid`, k.GetApiKey().GetId()); err != nil {
		t.Fatal(err)
	}

	count := func(tenantID string) int {
		var n int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM sandbox_resets WHERE tenant_id = $1::uuid AND kind = 'retention'`, tenantID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// Other tests' sandboxes in this database are old too (they have none of this test's activity), so the sweep may queue those as well; only these three matter.
	if _, err := e.runnerWithRetention(time.Hour * 24 * 90).Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if count(idle) != 1 || count(busy) != 0 || count(keyed) != 0 {
		t.Fatalf("queued: idle %d, busy %d, keyed %d; want 1, 0, 0", count(idle), count(busy), count(keyed))
	}
	// Once only, even if the sweep runs again before the job does.
	if _, err := e.runnerWithRetention(time.Hour * 24 * 90).Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if count(idle) != 1 {
		t.Errorf("the idle sandbox was queued %d times", count(idle))
	}
	// A retention job is a reset like any other and a person is not rate-limited by it.
	for {
		ran, err := e.runner.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ran {
			break
		}
	}
	var state string
	if err := e.system.QueryRow(ctx, `SELECT state FROM sandbox_resets WHERE tenant_id = $1::uuid AND kind = 'retention'`, idle).Scan(&state); err != nil || state != sandbox.StateDone {
		t.Errorf("the retention reset = %q, %v", state, err)
	}
	// ...and having just been reset, the sandbox is not idle again for another period.
	if _, err := e.runnerWithRetention(time.Hour * 24 * 90).Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if count(idle) != 1 {
		t.Errorf("the sandbox that was just reset was queued again (%d)", count(idle))
	}
	// A retention of zero is off.
	if n, err := e.runnerWithRetention(0).Sweep(ctx); err != nil || n != 0 {
		t.Errorf("the sweep with no retention queued %d, %v", n, err)
	}
}

// runnerWithRetention is the test runner with another retention period.
func (e *env) runnerWithRetention(d time.Duration) *sandbox.Runner {
	return sandbox.NewRunner(e.system, e.remotes.steps(), sandbox.Settings{Retention: d}, nil)
}
