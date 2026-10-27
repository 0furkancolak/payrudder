//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/safehttp"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	affiliatescmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/command"
	saascmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/sandbox"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	trialcmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/trialnotices/command"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
	webhooksworker "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/worker"
)

// The jobs that look across tenants (the outbox relay, the webhook dispatcher, the affiliate commission approver, the trial reminder, the sandbox reset runner and its
// retention sweep, the SaaS account sweep) run per tenant by placement: the tenants in the pooled database as ever, and each tenant in a cell in its own schema, in a
// transaction of its own. These tests run every job in the three states a deployment can be in: no cells (the pooled database), every tenant in a cell, and some
// tenants in a cell and some still pooled (a deployment half way through its placements). For each of them: every tenant is served once and none is skipped, a tenant that
// is moving is left alone and served when it is not, and a row of one tenant that is somehow in the schema of another is never served as the other's.

type placementMode struct {
	name  string
	cells bool
	// stay is how many of the first tenants of a test stay in the pooled database although cells are on.
	stay int
}

var placementModes = []placementMode{{"pooled", false, 0}, {"cells", true, 0}, {"mixed", true, 1}}

// jobWorld is one world (a Postgres, and a cell database in the modes that have cells) with a plans-svc over it, for the tests of the jobs.
type jobWorld struct {
	e    *env
	w    *world
	mode placementMode
}

func TestCellsJobs(t *testing.T) {
	for _, m := range placementModes {
		t.Run(m.name, func(t *testing.T) {
			w := startWorld(t, m.cells)
			j := &jobWorld{e: newEnvIn(t, w, saascmd.Config{}), w: w, mode: m}
			t.Run("OutboxRelay", j.relay)
			t.Run("WebhookDispatcher", j.dispatcher)
			t.Run("AffiliateCommissionApprover", j.approver)
			t.Run("TrialReminder", j.trials)
			t.Run("SandboxResetAndRetention", j.sandboxes)
			t.Run("SaasAccountSweep", j.saasSweep)
			t.Run("TheSharedPartOfARequestPath", j.requests)
			if m.cells {
				t.Run("ARowOfAnotherTenantIsNeverServed", j.strays)
			}
		})
	}
}

// tenants makes n accounts and returns their tenants of one environment, the live ones active. In a mode with stay tenants the first of them are kept in the pooled
// database (before anything asks where they are); cellOnly keeps none there, for a test about the cells.
func (j *jobWorld) tenants(t *testing.T, name string, n int, environment plansv1.Environment, cellOnly bool) []*plansv1.Tenant {
	t.Helper()
	var out []*plansv1.Tenant
	for i := range n {
		tn := j.e.account(t, fmt.Sprintf("%s %d", name, i))[environment]
		if j.mode.cells && i < j.mode.stay && !cellOnly {
			j.w.dir.keepPooled(uuid.MustParse(tn.GetId()))
		} else if j.mode.cells {
			// The tenant is placed, and its schema built, as soon as it exists, as a placement through the directory does it (the harness would otherwise do it at its
			// first request).
			if _, err := j.w.dir.Placement(context.Background(), uuid.MustParse(tn.GetId())); err != nil {
				t.Fatal(err)
			}
		}
		out = append(out, tn)
	}
	return out
}

func (j *jobWorld) live(t *testing.T, name string, n int) []*plansv1.Tenant {
	return j.tenants(t, name, n, plansv1.Environment_ENVIRONMENT_LIVE, false)
}

// inCell says whether the tenant's rows are in a cell in this world.
func (j *jobWorld) inCell(id string) bool { return j.w.cells && j.w.dir.inCell(uuid.MustParse(id)) }

// count counts the rows of a table of a tenant, where the tenant is.
func (j *jobWorld) count(t *testing.T, tenantID, table string) int {
	t.Helper()
	var n int
	if err := j.e.scan(context.Background(), tenantID, fmt.Sprintf(`SELECT count(*) FROM %s WHERE tenant_id = $1::uuid`, table), []any{tenantID}, &n); err != nil {
		t.Fatalf("count %s of %s: %v", table, tenantID, err)
	}
	return n
}

// pooledCount counts them in the pooled database, which does not hold the rows of a tenant in a cell.
func (j *jobWorld) pooledCount(t *testing.T, tenantID, table string) int {
	t.Helper()
	var n int
	if err := j.e.system.QueryRow(context.Background(), fmt.Sprintf(`SELECT count(*) FROM %s WHERE tenant_id = $1::uuid`, table), tenantID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// moving fences a tenant that is in a cell the way a move does, and releases it when the test ends. A pooled tenant is not fenced (the fence is the directory's, and a
// pooled tenant is never moved by these jobs); it reports false.
func (j *jobWorld) moving(t *testing.T, id string) bool {
	t.Helper()
	if !j.inCell(id) {
		return false
	}
	u := uuid.MustParse(id)
	j.w.dir.setMoving(u, true)
	t.Cleanup(func() { j.w.dir.setMoving(u, false) })
	return true
}

func (j *jobWorld) release(id string) { j.w.dir.setMoving(uuid.MustParse(id), false) }

// The rows a test sees after a job, per tenant.
type capture struct {
	mu   sync.Mutex
	rows []outbox.Row
}

func (c *capture) Publish(_ context.Context, rows []outbox.Row) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rows = append(c.rows, rows...)
	return nil
}

func (c *capture) of(tenantID string) []outbox.Row {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []outbox.Row
	for _, r := range c.rows {
		if r.TenantID == tenantID {
			out = append(out, r)
		}
	}
	return out
}

// relay: the outbox rows of every tenant are published once, from the table they are in, with the relay running as the service runs it (woken by NOTIFY, or by the sweep).
func (j *jobWorld) relay(t *testing.T) {
	e := j.e
	ts := j.live(t, "Relay", 3)
	ids := make([]string, len(ts))
	want := make([]int, len(ts))
	for i, tn := range ts {
		ids[i] = tn.GetId()
		seed(t, e, ids[i], fmt.Sprintf("r%d", i))
		want[i] = j.count(t, ids[i], "outbox")
		if want[i] == 0 {
			t.Fatalf("tenant %d has no outbox row to relay", i)
		}
		// The tenant's rows are in one place only.
		if j.inCell(ids[i]) != (j.pooledCount(t, ids[i], "outbox") == 0) {
			t.Fatalf("tenant %d: in a cell %v, but %d outbox rows in the pooled database", i, j.inCell(ids[i]), j.pooledCount(t, ids[i], "outbox"))
		}
	}
	// The last tenant is being moved when the relay starts, if it can be.
	fenced := j.moving(t, ids[2])

	sink := &capture{}
	r, err := outbox.NewRelay(e.system, sink, outbox.Config{PollInterval: 20 * time.Millisecond, CellSweep: 50 * time.Millisecond}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.WithCells(e.places.Router()).Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()

	published := func(i int) bool { return len(sink.of(ids[i])) == want[i] }
	testkit.Eventually(t, 15*time.Second, "the rows of the serving tenants are published", func() bool { return published(0) && published(1) && (fenced || published(2)) })
	if fenced {
		time.Sleep(300 * time.Millisecond) // several sweeps
		if got := len(sink.of(ids[2])); got != 0 {
			t.Fatalf("%d rows of a tenant that is moving were published", got)
		}
		j.release(ids[2])
		testkit.Eventually(t, 15*time.Second, "the rows of the tenant are published once it serves again", func() bool { return published(2) })
	}
	// Once each, from the right table, and nothing is left behind.
	time.Sleep(200 * time.Millisecond)
	for i, id := range ids {
		rows := sink.of(id)
		if len(rows) != want[i] {
			t.Errorf("tenant %d: %d rows published, want %d", i, len(rows), want[i])
		}
		seen := map[int64]bool{}
		for _, row := range rows {
			if seen[row.ID] {
				t.Errorf("tenant %d: row %d published twice", i, row.ID)
			}
			seen[row.ID] = true
		}
		if left := j.count(t, id, "outbox"); left != 0 {
			t.Errorf("tenant %d: %d rows left in the outbox", i, left)
		}
	}
}

// dispatcher: each tenant's deliveries go to its own endpoint, once, from the place where they are queued; a tenant that is moving waits.
func (j *jobWorld) dispatcher(t *testing.T) {
	e := j.e
	// The dispatcher works across tenants: the deliveries other tests queued (to addresses that mean nothing here) must not be in the way.
	e.clearDeliveries(t)
	ts := j.live(t, "Hooks", 3)
	rcv := make([]*receiver, len(ts))
	for i, tn := range ts {
		ctx := as(tn.GetId())
		rcv[i] = newReceiver(t)
		if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: 1000}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_hook", Name: "Hook", Email: "hook@example.com"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.hooks.CreateWebhookEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: rcv[i].URL, EventTypes: []string{webhooksmodel.SubscriptionCreated}}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_hook", PlanCode: "basic", StartTime: timestamppb.New(time.Now().Add(-time.Hour))}); err != nil {
			t.Fatal(err)
		}
		if n := j.count(t, tn.GetId(), "webhook_deliveries"); n != 1 {
			t.Fatalf("tenant %d has %d deliveries queued, want 1", i, n)
		}
	}
	fenced := j.moving(t, ts[2].GetId())
	now := time.Now().Add(time.Minute) // the dispatcher's clock runs a little ahead of the rows the database stamps
	disp := &webhooksworker.Dispatcher{Pool: e.system, Places: e.places, Box: e.box, Client: safehttp.NewClient(2*time.Second, true), Now: func() time.Time { return now }}
	pass := func() int {
		t.Helper()
		total := 0
		for {
			n, err := disp.Once(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			total += n
			if n == 0 {
				return total
			}
		}
	}
	first := pass()
	want := len(ts)
	if fenced {
		want--
	}
	if first != want {
		t.Fatalf("the dispatcher claimed %d deliveries, want %d", first, want)
	}
	for i, tn := range ts {
		calls := rcv[i].calls()
		if fenced && i == 2 {
			if len(calls) != 0 {
				t.Fatalf("a tenant that is moving got %d deliveries", len(calls))
			}
			continue
		}
		if len(calls) != 1 {
			t.Fatalf("tenant %d got %d deliveries, want 1", i, len(calls))
		}
		var body struct {
			TenantID string `json:"tenant_id"`
		}
		if err := json.Unmarshal(calls[0].body, &body); err != nil || body.TenantID != tn.GetId() {
			t.Fatalf("tenant %d was sent %s", i, calls[0].body)
		}
	}
	if fenced {
		j.release(ts[2].GetId())
		if n := pass(); n != 1 || len(rcv[2].calls()) != 1 {
			t.Fatalf("after the move: claimed %d, delivered %d; want one", n, len(rcv[2].calls()))
		}
	}
	if n := pass(); n != 0 {
		t.Fatalf("a delivered event was claimed again (%d)", n)
	}
	for i := range ts {
		if len(rcv[i].calls()) != 1 {
			t.Errorf("tenant %d got %d deliveries in all, want 1", i, len(rcv[i].calls()))
		}
	}
}

// approver: the commissions whose hold is over are approved, each tenant's in its own place, each once.
func (j *jobWorld) approver(t *testing.T) {
	e := j.e
	ts := j.live(t, "Approver", 3)
	ws := make([]*affiliateWorld, len(ts))
	invs := make([]invoice, len(ts))
	now := time.Now()
	for i, tn := range ts {
		ws[i] = startAffiliateWorld(t, e, tn, nil)
		invs[i] = ws[i].newInvoice(ws[i].cust, 10000, 0)
		ws[i].paid(t, invs[i], now.Add(-40*24*time.Hour), "") // the hold is 30 days: it is over
		if c := ws[i].commission(t, invs[i]); c.GetStatus() != "pending" {
			t.Fatalf("tenant %d: commission %v, want pending", i, c)
		}
	}
	fenced := j.moving(t, ts[2].GetId())
	job := affiliatescmd.NewApprover(e.system, time.Hour, slog.Default()).WithPlaces(e.places).WithClock(func() time.Time { return now })
	n, err := job.Once(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := int64(len(ts))
	if fenced {
		want--
	}
	if n != want {
		t.Fatalf("approved %d, want %d", n, want)
	}
	if fenced {
		j.release(ts[2].GetId())
	}
	for i := range ts {
		if c := ws[i].commission(t, invs[i]); c.GetStatus() != "pending" && c.GetStatus() != "approved" || (c.GetStatus() == "pending") != (fenced && i == 2) {
			t.Fatalf("tenant %d: commission is %q (moving: %v)", i, c.GetStatus(), fenced && i == 2)
		}
	}
	rest := int64(0)
	if fenced {
		rest = 1
	}
	if n, err := job.Once(context.Background()); err != nil || n != rest {
		t.Fatalf("second pass approved %d, %v; want %d", n, err, rest)
	}
	if n, err := job.Once(context.Background()); err != nil || n != 0 {
		t.Fatalf("third pass approved %d, %v; want none", n, err)
	}
	// The statement of a tenant in a cell names the tenant as well as the schema: asked about another tenant it touches nothing.
	if j.inCell(ts[1].GetId()) {
		other := uuid.MustParse(ts[0].GetId())
		err := e.tx(context.Background(), ts[1].GetId(), func(tx pgx.Tx) error {
			got, err := sqlc.New(tx).SystemApproveDueCommissionsOfTenant(context.Background(), sqlc.SystemApproveDueCommissionsOfTenantParams{TenantID: other, Now: now.AddDate(1, 0, 0)})
			if got != 0 {
				return fmt.Errorf("approved %d rows of another tenant", got)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// trials: the trial reminder visits every serving tenant in a cell once per pass, and reminds each trial once.
func (j *jobWorld) trials(t *testing.T) {
	e := j.e
	ts := j.live(t, "Trials", 3)
	for i, tn := range ts {
		ctx := as(tn.GetId())
		if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "trial", Name: "Trial"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "trial", BaseFeeMinor: 5000, Fees: yearlyFees("trial", 5000)}); err != nil {
			t.Fatal(err)
		}
		cus := fmt.Sprintf("cus_trial_%d", i)
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: cus, Name: cus, Email: cus + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: cus, PlanCode: "trial", TrialDays: proto.Int32(10)}); err != nil {
			t.Fatal(err)
		}
	}
	fenced := j.moving(t, ts[2].GetId())
	at := time.Now().Add(8 * 24 * time.Hour) // two days left: inside the three-day window
	visits := map[uuid.UUID]int{}
	var mu sync.Mutex
	job := trialcmd.New(e.system, e.appDB, trialcmd.Config{Days: 3}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithPlaces(e.places).
		WithClock(func() time.Time { return at }).
		WithVisits(func(id uuid.UUID) { mu.Lock(); visits[id]++; mu.Unlock() })
	reminded, looked, err := job.Once(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := len(ts)
	if fenced {
		want--
	}
	if reminded != want || looked != want {
		t.Fatalf("reminded %d of %d looked at, want %d", reminded, looked, want)
	}
	for i, tn := range ts {
		id := uuid.MustParse(tn.GetId())
		if j.inCell(tn.GetId()) && !(fenced && i == 2) && visits[id] != 1 {
			t.Errorf("tenant %d was visited %d times in one pass, want once", i, visits[id])
		}
		if fenced && i == 2 && visits[id] != 0 {
			t.Errorf("a tenant that is moving was visited %d times", visits[id])
		}
		if !j.inCell(tn.GetId()) && visits[id] != 0 {
			t.Errorf("a pooled tenant was visited as a tenant in a cell")
		}
	}
	for id, n := range visits {
		if n != 1 {
			t.Errorf("tenant %s was visited %d times in one pass", id, n)
		}
	}
	for i, tn := range ts {
		if fenced && i == 2 {
			continue
		}
		if n := j.count(t, tn.GetId(), "subscription_trial_notices"); n != 1 {
			t.Errorf("tenant %d has %d notices, want 1", i, n)
		}
		if n := j.count(t, tn.GetId(), "outbox"); n < 1 {
			t.Errorf("tenant %d has no fact for the notifier", i)
		}
	}
	if fenced {
		j.release(ts[2].GetId())
		if r, _, err := job.Once(context.Background()); err != nil || r != 1 {
			t.Fatalf("after the move: reminded %d, %v; want one", r, err)
		}
	}
	if r, _, err := job.Once(context.Background()); err != nil || r != 0 {
		t.Fatalf("a second pass reminded %d, %v; want none", r, err)
	}
	for i, tn := range ts {
		if n := j.count(t, tn.GetId(), "subscription_trial_notices"); n != 1 {
			t.Errorf("tenant %d has %d notices in all, want 1", i, n)
		}
	}
}

// sandboxes: the retention sweep asks each sandbox in its own place whether it was used; the runner empties a sandbox where it is and no other.
func (j *jobWorld) sandboxes(t *testing.T) {
	e := j.e
	ctx := context.Background()
	ts := j.tenants(t, "Sandbox", 4, plansv1.Environment_ENVIRONMENT_SANDBOX, false)
	ids := make([]string, len(ts))
	for i, tn := range ts {
		ids[i] = tn.GetId()
		if _, err := e.system.Exec(ctx, `UPDATE tenants SET created_at = now() - interval '200 days' WHERE id = $1::uuid`, ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	idle, busy, idle2, withData := ids[0], ids[1], ids[2], ids[3]
	// A customer made just now keeps a sandbox alive, wherever its rows are; a sandbox with data is reset by hand below.
	for _, id := range []string{busy, withData} {
		if _, err := e.plans.CreateCustomer(as(id), &plansv1.CreateCustomerRequest{ExternalId: "c", Name: "N", Email: "n@example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	queued := func(id string) int {
		var n int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM sandbox_resets WHERE tenant_id = $1::uuid AND kind = 'retention'`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	fenced := j.moving(t, idle2)
	sweep := func() {
		t.Helper()
		if _, err := e.runnerWithRetention(90 * 24 * time.Hour).Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sweep()
	// withData was made 200 days ago and has a customer made now: it is busy too.
	wantIdle2 := 1
	if fenced {
		wantIdle2 = 0
	}
	if queued(idle) != 1 || queued(busy) != 0 || queued(withData) != 0 || queued(idle2) != wantIdle2 {
		t.Fatalf("queued: idle %d, busy %d, with data %d, idle2 %d (moving %v); want 1, 0, 0 and 1 unless moving", queued(idle), queued(busy), queued(withData), queued(idle2), fenced)
	}
	if fenced {
		j.release(idle2)
	}
	sweep()
	sweep()
	for _, id := range []string{idle, idle2} {
		if queued(id) != 1 {
			t.Errorf("sandbox %s was queued %d times, want once", id, queued(id))
		}
	}
	if queued(busy) != 0 {
		t.Errorf("a sandbox in use was queued")
	}
	// The runner works the jobs; a reset is plans-svc's step in the place of the tenant.
	for {
		ran, err := e.runner.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ran {
			break
		}
	}
	for _, id := range []string{idle, idle2} {
		var state string
		if err := e.system.QueryRow(ctx, `SELECT state FROM sandbox_resets WHERE tenant_id = $1::uuid AND kind = 'retention'`, id).Scan(&state); err != nil || state != sandbox.StateDone {
			t.Errorf("the retention reset of %s = %q, %v", id, state, err)
		}
	}
	// Emptying a sandbox deletes its customers where they are, and touches no other tenant's.
	got, err := sandbox.NewLocal(e.places).Reset(ctx, uuid.MustParse(withData))
	if err != nil || got["customers"] != 1 {
		t.Fatalf("reset = %v, %v; want one customer deleted", got, err)
	}
	if n := j.count(t, withData, "customers"); n != 0 {
		t.Errorf("%d customers left after a reset", n)
	}
	if n := j.count(t, busy, "customers"); n != 1 {
		t.Errorf("the reset of another sandbox changed this one: %d customers", n)
	}
}

// saasSweep: every organisation becomes a customer of the platform tenant with one subscription, once, wherever the platform tenant's rows are.
func (j *jobWorld) saasSweep(t *testing.T) {
	es := newEnvIn(t, j.w, saasConfig())
	ctx := context.Background()
	if err := es.saas.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	pt := es.platformOf(t)
	var accounts []string
	for i := range 3 {
		a, err := es.admin.CreateAccount(operator(), &plansv1.CreateAccountRequest{Name: fmt.Sprintf("Org %d", i), Currency: "USD", Timezone: "UTC", GracePeriod: durationpb.New(24 * time.Hour),
			Tier: plansv1.TenantTier_TENANT_TIER_STANDARD})
		if err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, a.GetAccount().GetId())
	}
	for {
		n, err := es.saas.SweepOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n < 50 {
			break
		}
	}
	for _, a := range accounts {
		var subs int
		if err := es.scan(ctx, pt.GetId(), `SELECT count(*) FROM subscriptions s JOIN customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id WHERE c.external_id = $1 AND s.state <> 'cancelled'`,
			[]any{a}, &subs); err != nil || subs != 1 {
			t.Errorf("account %s has %d subscriptions after the sweep, %v; want 1", a, subs, err)
		}
	}
	if es.places.CellsOn() {
		if in, _ := es.places.InCell(ctx, uuid.MustParse(pt.GetId())); in != (j.mode.cells) {
			t.Errorf("the platform tenant is in a cell: %v, mode %s", in, j.mode.name)
		}
	}
	if n, err := es.saas.SweepOnce(ctx); err != nil || n != 0 {
		t.Errorf("a second sweep handled %d accounts, %v; want none", n, err)
	}
}

// requests: what the request paths of the tenants in cells share with the jobs: the tenant's own row (environment, status, tier) is known in a cell without a tenants
// table, and a call for a tenant that is moving is refused, never answered from another place.
func (j *jobWorld) requests(t *testing.T) {
	e := j.e
	ts := j.live(t, "Requests", 2)
	a, b := ts[0].GetId(), ts[1].GetId()
	ctx := as(a)
	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_a", Name: "A", Email: "a@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.GetCustomer(as(b), &plansv1.GetCustomerRequest{ExternalId: "cus_a"}); err == nil {
		t.Fatal("a tenant read another's customer")
	}
	// The status of the tenant is read where the tenant is: a tenant suspended by an operator is refused webhook endpoints (a trigger reads current_tenant()).
	if _, err := e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: a, Status: plansv1.TenantStatus_TENANT_STATUS_SUSPENDED, Reason: "itest"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.hooks.CreateWebhookEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: "https://example.com/hook"}); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("an endpoint for a suspended live tenant: %v", err)
	}
	if _, err := e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: a, Status: plansv1.TenantStatus_TENANT_STATUS_ACTIVE, Reason: "itest"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.hooks.CreateWebhookEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: "https://example.com/hook"}); err != nil {
		t.Fatalf("an endpoint for the same tenant once it is active: %v", err)
	}
	if j.moving(t, a) {
		_, err := e.plans.GetCustomer(ctx, &plansv1.GetCustomerRequest{ExternalId: "cus_a"})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("a call for a tenant that is moving: %v, want Unavailable (retry is safe)", err)
		}
		j.release(a)
		if _, err := e.plans.GetCustomer(ctx, &plansv1.GetCustomerRequest{ExternalId: "cus_a"}); err != nil {
			t.Fatalf("after the move: %v", err)
		}
	}
}

// stray copies the rows of a table that a query selects from the schema of one tenant into the schema of another, as a superuser with foreign keys and triggers off:
// a row of one tenant in the schema of another, which nothing in plans-svc writes.
func (j *jobWorld) stray(t *testing.T, table, columns string, from, to string, where string) {
	t.Helper()
	ctx := context.Background()
	u, err := url.Parse(j.w.pg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/plans_c1_db"
	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	err = pgxx.InTx(ctx, txBeginner{conn}, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
			return err
		}
		q := fmt.Sprintf(`INSERT INTO %[3]s.%[1]s (%[2]s) OVERRIDING SYSTEM VALUE SELECT %[2]s FROM %[4]s.%[1]s %[5]s`,
			table, columns, pgx.Identifier{tenantdir.Schema(uuid.MustParse(to))}.Sanitize(), pgx.Identifier{tenantdir.Schema(uuid.MustParse(from))}.Sanitize(), where)
		_, err := tx.Exec(ctx, q)
		return err
	})
	if err != nil {
		t.Fatalf("copy %s of %s into %s: %v", table, from, to, err)
	}
}

// txBeginner makes a single connection a pgxx.Beginner.
type txBeginner struct{ c *pgx.Conn }

func (b txBeginner) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	return b.c.BeginTx(ctx, opts)
}

// columnsOf lists the columns of a table of the tenant schema (the same in every schema).
func (j *jobWorld) columnsOf(t *testing.T, tenantID, table string) string {
	t.Helper()
	var cols []string
	err := j.e.tx(context.Background(), tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT quote_ident(column_name) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 ORDER BY ordinal_position`, table)
		if err != nil {
			return err
		}
		cols, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil || len(cols) == 0 {
		t.Fatalf("columns of %s: %v, %v", table, cols, err)
	}
	return strings.Join(cols, ", ")
}

// strays: a row of tenant B that is in the schema of tenant A (a bug, a bad restore) is never served as A's or B's twice: the jobs that visit A's schema refuse the batch,
// they do not deliver, remind or publish it under the wrong tenant, and they serve B from B's own schema as before.
func (j *jobWorld) strays(t *testing.T) {
	e := j.e
	ctx := context.Background()
	ts := j.tenants(t, "Strays", 2, plansv1.Environment_ENVIRONMENT_LIVE, true)
	a, b := ts[0].GetId(), ts[1].GetId()
	if !j.inCell(a) || !j.inCell(b) {
		t.Fatal("both tenants must be in a cell")
	}
	for _, id := range []string{a, b} {
		actx := as(id)
		if _, err := e.plans.CreatePlan(actx, &plansv1.CreatePlanRequest{Code: "trial", Name: "Trial"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.PublishPlanVersion(actx, &plansv1.PublishPlanVersionRequest{PlanCode: "trial", BaseFeeMinor: 5000, Fees: yearlyFees("trial", 5000)}); err != nil {
			t.Fatal(err)
		}
	}
	rcvA, rcvB := newReceiver(t), newReceiver(t)
	for id, rcv := range map[string]*receiver{a: rcvA, b: rcvB} {
		if _, err := e.hooks.CreateWebhookEndpoint(as(id), &plansv1.CreateWebhookEndpointRequest{Url: rcv.URL, EventTypes: []string{webhooksmodel.SubscriptionCreated, webhooksmodel.SubscriptionTrialWillEnd}}); err != nil {
			t.Fatal(err)
		}
	}
	// B has a delivery due, an outbox row and a trial about to end; A has none of them yet.
	cus := "cus_stray"
	if _, err := e.plans.CreateCustomer(as(b), &plansv1.CreateCustomerRequest{ExternalId: cus, Name: cus, Email: cus + "@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateSubscription(as(b), &plansv1.CreateSubscriptionRequest{CustomerExternalId: cus, PlanCode: "trial", TrialDays: proto.Int32(10)}); err != nil {
		t.Fatal(err)
	}
	// ... and copies of them are in A's schema.
	for _, table := range []string{"webhook_deliveries", "subscriptions"} {
		j.stray(t, table, j.columnsOf(t, b, table), b, a, "")
	}
	if j.count(t, a, "webhook_deliveries") != 0 || j.count(t, a, "subscriptions") != 0 {
		// count() filters on the tenant of the visited schema, so the strays (tenant_id = b) are not in these counts: they are in A's schema all the same.
		t.Fatal("setup: A has rows of its own")
	}

	t.Run("the dispatcher does not deliver a stray delivery, and serves B from B's schema", func(t *testing.T) {
		now := time.Now().Add(time.Minute)
		disp := &webhooksworker.Dispatcher{Pool: e.system, Places: e.places, Box: e.box, Client: safehttp.NewClient(2*time.Second, true), Now: func() time.Time { return now }}
		for range 3 {
			if _, err := disp.Once(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if got := len(rcvB.calls()); got != 1 {
			t.Fatalf("B's endpoint got %d deliveries, want its own, once", got)
		}
		if got := len(rcvA.calls()); got != 0 {
			t.Fatalf("A's endpoint got %d deliveries", got)
		}
		var attempts int
		err := e.places.EachCell(ctx, func(ctx context.Context, p pgxx.PlacedTenant) error {
			if p.TenantID.String() != a {
				return nil
			}
			return e.places.CellTx(ctx, p, func(tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT COALESCE(sum(attempts), 0) FROM webhook_deliveries WHERE tenant_id = $1`, uuid.MustParse(b)).Scan(&attempts)
			})
		})
		if err != nil || attempts != 0 {
			t.Fatalf("the stray delivery was leased: attempts %d, %v", attempts, err)
		}
	})

	t.Run("the trial reminder refuses the schema and reminds B once from its own", func(t *testing.T) {
		at := time.Now().Add(8 * 24 * time.Hour)
		job := trialcmd.New(e.system, e.appDB, trialcmd.Config{Days: 3}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithPlaces(e.places).WithClock(func() time.Time { return at })
		_, _, err := job.Once(ctx)
		if err == nil || !strings.Contains(err.Error(), "is in the schema of tenant") {
			t.Fatalf("a trial of B in the schema of A: %v", err)
		}
		if n := j.count(t, b, "subscription_trial_notices"); n != 1 {
			t.Fatalf("B has %d notices, want 1 from its own schema", n)
		}
		if n := j.count(t, a, "subscription_trial_notices"); n != 0 {
			t.Fatalf("A has %d notices", n)
		}
	})

	t.Run("the relay refuses a stray outbox row", func(t *testing.T) {
		// B has facts to publish by now (the trial reminder wrote one): a copy of them is in A's schema.
		j.stray(t, "outbox", j.columnsOf(t, b, "outbox"), b, a, "")
		sink := &capture{}
		r, err := outbox.NewRelay(e.system, sink, outbox.Config{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var placed pgxx.PlacedTenant
		_ = e.places.EachCell(ctx, func(_ context.Context, p pgxx.PlacedTenant) error {
			if p.TenantID.String() == a {
				placed = p
			}
			return nil
		})
		if _, err := r.RelayTenantOnce(ctx, placed); err == nil || !strings.Contains(err.Error(), "is in the schema of tenant") {
			t.Fatalf("a row of B in the schema of A: %v", err)
		}
		if got := len(sink.of(b)) + len(sink.of(a)); got != 0 {
			t.Fatalf("%d rows were published from the schema of A", got)
		}
	})
}
