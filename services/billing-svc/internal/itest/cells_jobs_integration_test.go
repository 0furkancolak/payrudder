//go:build integration

package itest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	runcmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/command"
	creditnotecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/creditnotes/command"
	creditcmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/credits/command"
	einvoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/einvoices/command"
	feecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/fees/command"
	hostedcmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/hosted/command"
	invoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	reratecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating/command"
	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantevents"
	"github.com/0furkancolak/payrudder/services/billing-svc/migrations"
)

// The cross-tenant jobs of billing-svc run per tenant by placement (ADR 0018). Each job is run on four tenants in three worlds, and must do each tenant's work exactly
// once whatever the mix of places:
//
//	pooled  every tenant is in the pooled database, and cells are off: the code as it was before cells
//	cells   every tenant is in a schema of a cell
//	mixed   two tenants are pooled and two are in a cell
//
// A tenant that is moving is left alone by every job (in the places that know about moves), and picked up by the next pass once it serves again; no tenant is ever
// done twice, and one tenant's failure does not undo another's work.

// jobTenant is a tenant of a job world.
type jobTenant struct {
	id     uuid.UUID
	inCell bool
	// mover tenants are the ones a case marks as moving.
	mover bool
}

// jobWorld is a billing database (and, in the worlds with cells, a cell database) with four tenants in it.
type jobWorld struct {
	t       *testing.T
	kind    string
	db      *tenantdb.DB
	pooled  *pgxpool.Pool
	cellSys *pgxpool.Pool
	dir     *testkit.Directory
	tenants []jobTenant
	// migrator DSNs, for the statements the roles of the service cannot run.
	pooledMigrator, cellMigrator string
}

var jobWorlds = []string{"pooled", "cells", "mixed"}

func newJobWorld(t *testing.T, kind string) *jobWorld {
	t.Helper()
	pg := testkit.StartPostgres(t)
	pdb := testkit.MigratedDB(t, pg, "billing", migrations.FS)
	w := &jobWorld{t: t, kind: kind, dir: testkit.NewDirectory(), pooledMigrator: pdb.Migrator}
	var err error
	if w.pooled, err = pgxx.NewPool(context.Background(), pdb.System); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.pooled.Close)
	var router *pgxx.Router
	if kind != "pooled" {
		cell := testkit.CreateCellDB(t, pg, "billing", "c1")
		w.cellMigrator = cell.Migrator
		cellApp, err := pgxx.NewPool(context.Background(), cell.App)
		if err != nil {
			t.Fatal(err)
		}
		if w.cellSys, err = pgxx.NewPool(context.Background(), cell.System); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cellApp.Close)
		t.Cleanup(w.cellSys.Close)
		if router, err = pgxx.NewRouter(w.pooled, map[string]*pgxpool.Pool{"c1": w.cellSys}, w.dir); err != nil {
			t.Fatal(err)
		}
	}
	w.db = tenantdb.New(w.pooled, router)
	for i := range 4 {
		jt := jobTenant{id: uuid.New(), mover: i >= 2}
		switch kind {
		case "cells":
			jt.inCell = true
		case "mixed":
			jt.inCell = i%2 == 1
		}
		if jt.inCell {
			if _, err := pgxx.ProvisionTenant(context.Background(), w.cellMigrator, tenantSchemas(), jt.id, nil); err != nil {
				t.Fatal(err)
			}
			w.dir.Place(jt.id, "c1")
		}
		w.tenants = append(w.tenants, jt)
	}
	return w
}

func (w *jobWorld) cellsOn() bool { return w.kind != "pooled" }

// run executes fn in a transaction where the tenant's rows are.
func (w *jobWorld) run(tid uuid.UUID, fn func(ctx context.Context, tx pgx.Tx) error) {
	w.t.Helper()
	ctx := context.Background()
	var err error
	if w.inCell(tid) {
		err = pgxx.InTenantSchemaTx(ctx, w.cellSys, tid, func(tx pgx.Tx) error { return fn(ctx, tx) })
	} else {
		err = pgxx.InTx(ctx, w.pooled, pgx.TxOptions{}, func(tx pgx.Tx) error { return fn(ctx, tx) })
	}
	if err != nil {
		w.t.Fatal(err)
	}
}

func (w *jobWorld) inCell(tid uuid.UUID) bool {
	for _, jt := range w.tenants {
		if jt.id == tid {
			return jt.inCell
		}
	}
	w.t.Fatalf("tenant %s is not of the world", tid)
	return false
}

func (w *jobWorld) exec(tid uuid.UUID, sql string, args ...any) {
	w.t.Helper()
	w.run(tid, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, sql, args...)
		return err
	})
}

func (w *jobWorld) scalar(tid uuid.UUID, sql string, args ...any) int64 {
	w.t.Helper()
	var n int64
	w.run(tid, func(ctx context.Context, tx pgx.Tx) error { return tx.QueryRow(ctx, sql, args...).Scan(&n) })
	return n
}

// setMoving marks the movers as moving (or serving again). A mover in the pooled database is moving too, on its way to a cell.
func (w *jobWorld) setMoving(moving bool) {
	for _, jt := range w.tenants {
		if !jt.mover || !w.cellsOn() {
			continue
		}
		p := tenantdir.Placement{TenantID: jt.id, Status: tenantdir.StatusReady}
		if jt.inCell {
			p.Cell = "c1"
		}
		if moving {
			p.Status = tenantdir.StatusMoving
		}
		w.dir.Set(p)
	}
}

const jobSnapshot = `{"environment":"live","currency_exponent":2,"customer_external_id":"cus","customer_email":"c@example.com","payment_method":"pm_sandbox_success"}`

// invoice inserts a draft or other invoice of the tenant.
func (w *jobWorld) invoice(tid uuid.UUID, status string) uuid.UUID {
	w.t.Helper()
	id := uuid.New()
	w.exec(tid, `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot)
		VALUES ($1,$2,$3,'2026-10','REGULAR',$4,'USD',500,500,$5,$6)`, tid, id, uuid.New(), uuid.New(), status, jobSnapshot)
	return id
}

// A jobCase is one job: how to give a tenant work, how to run one pass, and how often the pass did a tenant's work.
type jobCase struct {
	name string
	seed func(t *testing.T, w *jobWorld, tid uuid.UUID)
	pass func(t *testing.T, w *jobWorld) error
	// effects is the cumulative number of times the job did the tenant's work; want is how many each pass of a serving tenant adds.
	effects func(t *testing.T, w *jobWorld, tid uuid.UUID) int64
	want    int64
	// repeating jobs do the work on every pass (a scheduler tick); the others do it once, and later passes find nothing.
	repeating bool
}

func TestJobsRunOncePerTenantByPlacement(t *testing.T) {
	for _, kind := range jobWorlds {
		t.Run(kind, func(t *testing.T) {
			w := newJobWorld(t, kind)
			for _, c := range jobCases(w) {
				t.Run(c.name, func(t *testing.T) { runJobCase(t, w, c) })
			}
			t.Run("credit expiry: one tenant failing does not undo another", func(t *testing.T) { creditExpiryIsolatesFailures(t, w) })
			t.Run("gauges count every tenant once", func(t *testing.T) { gaugesCountEveryTenantOnce(t, w) })
		})
	}
}

func runJobCase(t *testing.T, w *jobWorld, c jobCase) {
	for _, jt := range w.tenants {
		c.seed(t, w, jt.id)
	}
	base := map[uuid.UUID]int64{}
	check := func(label string, serving func(jobTenant) bool, passes int) {
		t.Helper()
		for _, jt := range w.tenants {
			got := c.effects(t, w, jt.id) - base[jt.id]
			want := int64(0)
			if serving(jt) {
				want = c.want * int64(passes)
			}
			if got != want {
				t.Errorf("%s: tenant %s (in cell: %v, mover: %v) had its work done %d times, want %d", label, jt.id, jt.inCell, jt.mover, got, want)
			}
		}
	}
	notMoving := func(jobTenant) bool { return true }
	stillServing := func(jt jobTenant) bool { return !jt.mover || !w.cellsOn() }

	// While the movers move: the others are done once, the movers not at all, and a second pass changes nothing for a job that does its work once.
	w.setMoving(true)
	if err := c.pass(t, w); err != nil {
		t.Fatalf("pass 1: %v", err)
	}
	check("pass 1 with movers moving", stillServing, 1)
	if err := c.pass(t, w); err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	if c.repeating {
		check("pass 2 with movers moving", stillServing, 2)
	} else {
		check("pass 2 with movers moving", stillServing, 1)
	}

	// Serving again: the movers are picked up, once.
	w.setMoving(false)
	for _, jt := range w.tenants {
		base[jt.id] = c.effects(t, w, jt.id)
	}
	if err := c.pass(t, w); err != nil {
		t.Fatalf("pass 3: %v", err)
	}
	if c.repeating {
		check("pass 3 with nobody moving", notMoving, 1)
	} else {
		// A one-shot job: the movers' work is done now (once), the others' was done in pass 1 and is not done again.
		for _, jt := range w.tenants {
			got := c.effects(t, w, jt.id) - base[jt.id]
			want := int64(0)
			if jt.mover && w.cellsOn() {
				want = c.want
			}
			if got != want {
				t.Errorf("pass 3: tenant %s (mover: %v) had its work done %d more times, want %d", jt.id, jt.mover, got, want)
			}
		}
	}
	for _, jt := range w.tenants {
		base[jt.id] = c.effects(t, w, jt.id)
	}
	if err := c.pass(t, w); err != nil {
		t.Fatalf("pass 4: %v", err)
	}
	for _, jt := range w.tenants {
		got := c.effects(t, w, jt.id) - base[jt.id]
		want := int64(0)
		if c.repeating {
			want = c.want
		}
		if got != want {
			t.Errorf("pass 4: tenant %s had its work done %d more times, want %d", jt.id, got, want)
		}
	}
}

// recordedTenants collects the tenants a fake service was called for, read from the outgoing metadata of the call.
type recordedTenants struct {
	mu sync.Mutex
	n  map[uuid.UUID]int64
}

func (r *recordedTenants) add(ctx context.Context) {
	md, _ := metadata.FromOutgoingContext(ctx)
	ids := md.Get(tenant.MDTenantID)
	if len(ids) == 0 {
		return
	}
	id, err := uuid.Parse(ids[0])
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n == nil {
		r.n = map[uuid.UUID]int64{}
	}
	r.n[id]++
}

func (r *recordedTenants) get(id uuid.UUID) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n[id]
}

// jobPlans stands in for plans-svc: it answers what the jobs ask and records for which tenant.
type jobPlans struct {
	inputs.Plans
	applied, reversed recordedTenants
}

func (p *jobPlans) ApplyScheduledChanges(ctx context.Context, _ *plansv1.ApplyScheduledChangesRequest, _ ...grpc.CallOption) (*plansv1.ApplyScheduledChangesResponse, error) {
	p.applied.add(ctx)
	return &plansv1.ApplyScheduledChangesResponse{}, nil
}

func (p *jobPlans) ListSubscriptionAdjustments(context.Context, *plansv1.ListSubscriptionAdjustmentsRequest, ...grpc.CallOption) (*plansv1.ListSubscriptionAdjustmentsResponse, error) {
	return &plansv1.ListSubscriptionAdjustmentsResponse{}, nil
}

func (p *jobPlans) ListSubscriptionsFeeDue(context.Context, *plansv1.ListSubscriptionsFeeDueRequest, ...grpc.CallOption) (*plansv1.ListSubscriptionsFeeDueResponse, error) {
	return &plansv1.ListSubscriptionsFeeDueResponse{}, nil
}

func (p *jobPlans) ReversePromotion(ctx context.Context, _ *plansv1.ReversePromotionRequest, _ ...grpc.CallOption) (*plansv1.ReversePromotionResponse, error) {
	p.reversed.add(ctx)
	return &plansv1.ReversePromotionResponse{}, nil
}

// jobAdmin lists the world's tenants and describes each; with failTenant set it refuses to (permanently), which ends a re-rating job after one visit.
type jobAdmin struct {
	inputs.Admin
	w          *jobWorld
	failTenant bool
	asked      recordedTenants
}

func (a *jobAdmin) ListTenants(context.Context, *plansv1.ListTenantsRequest, ...grpc.CallOption) (*plansv1.ListTenantsResponse, error) {
	out := &plansv1.ListTenantsResponse{}
	for _, jt := range a.w.tenants {
		out.Tenants = append(out.Tenants, a.describe(jt.id))
	}
	return out, nil
}

func (a *jobAdmin) describe(id uuid.UUID) *plansv1.Tenant {
	return &plansv1.Tenant{Id: id.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE, Name: "Acme", Currency: "USD", Timezone: "UTC",
		CreateTime: timestamppb.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))}
}

func (a *jobAdmin) GetTenant(_ context.Context, in *plansv1.GetTenantRequest, _ ...grpc.CallOption) (*plansv1.GetTenantResponse, error) {
	id, err := uuid.Parse(in.GetTenantId())
	if err != nil {
		return nil, err
	}
	a.asked.mu.Lock()
	if a.asked.n == nil {
		a.asked.n = map[uuid.UUID]int64{}
	}
	a.asked.n[id]++
	a.asked.mu.Unlock()
	if a.failTenant {
		return nil, inputs.Permanent(errors.New("the tenant is refused"))
	}
	return &plansv1.GetTenantResponse{Tenant: a.describe(id)}, nil
}

// jobAggregator counts the watermark questions per tenant: one per run driven.
type jobAggregator struct {
	inputs.Aggregator
	asked recordedTenants
}

func (a *jobAggregator) GetIngestWatermark(ctx context.Context, _ *aggregatorv1.GetIngestWatermarkRequest, _ ...grpc.CallOption) (*aggregatorv1.GetIngestWatermarkResponse, error) {
	a.asked.add(ctx)
	return &aggregatorv1.GetIngestWatermarkResponse{CaughtUp: false}, nil
}

type jobIngest struct{}

func (jobIngest) GetOutboxWatermark(context.Context, *ingestv1.GetOutboxWatermarkRequest, ...grpc.CallOption) (*ingestv1.GetOutboxWatermarkResponse, error) {
	return &ingestv1.GetOutboxWatermarkResponse{}, nil
}

func jobCases(w *jobWorld) []jobCase {
	ctx := context.Background()
	later := &clock{t: time.Now().Add(time.Hour)}
	engine := func(clk *clock) *sagacommand.Engine {
		eng, err := sagacommand.NewEngine(sagacommand.Config{Now: clk.Now, StepTimeout: time.Minute, Log: testkit.Logger()})
		if err != nil {
			w.t.Fatal(err)
		}
		return eng
	}
	nowClock := &clock{t: time.Now()}
	eng, laterEng := engine(nowClock), engine(later)
	invoices, err := invoicecmd.New(invoicecmd.Config{DB: w.db, Engine: eng, Now: nowClock.Now})
	if err != nil {
		w.t.Fatal(err)
	}
	plans := &jobPlans{}
	admin := &jobAdmin{w: w}
	failing := &jobAdmin{w: w, failTenant: true}
	agg := &jobAggregator{}
	in := &inputs.Inputs{Plans: plans, Admin: admin, Aggregator: agg}
	events := &fakeEvents{}
	// The scheduler's clock: after the October close of tenants created in January.
	tick := &clock{t: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	mgr, err := runcmd.NewManager(runcmd.Config{DB: w.db, Engine: eng, Inputs: in, Invoices: invoices, Ingest: jobIngest{}, Now: tick.Now, WatermarkDelay: 30 * time.Second,
		EndOffsets: func(context.Context) (map[int32]int64, error) { return map[int32]int64{0: 1}, nil }, Log: testkit.Logger()})
	if err != nil {
		w.t.Fatal(err)
	}

	return []jobCase{
		{
			name: "tenant event flusher",
			seed: func(t *testing.T, w *jobWorld, tid uuid.UUID) {
				for range 2 {
					w.exec(tid, `INSERT INTO tenant_event_outbox (tenant_id, event_id, type, data, occurred_at) VALUES ($1,$2,'invoice.paid','{}'::jsonb,now())`, tid, uuid.New())
				}
			},
			pass: func(t *testing.T, w *jobWorld) error {
				return tenantevents.NewRelay(w.db, events, testkit.Logger()).Flush(ctx)
			},
			effects: func(t *testing.T, w *jobWorld, tid uuid.UUID) int64 { return int64(len(events.recorded(tid))) },
			want:    2,
		},
		{
			name: "credit expiry",
			seed: func(t *testing.T, w *jobWorld, tid uuid.UUID) {
				cid := uuid.New()
				for range 2 {
					w.exec(tid, `INSERT INTO credit_lots (tenant_id, id, customer_id, granted, remaining, source, expires_at) VALUES ($1,$2,$3,100,100,'manual',now() - interval '1 hour')`, tid, uuid.New(), cid)
				}
				w.exec(tid, `INSERT INTO credit_lots (tenant_id, id, customer_id, granted, remaining, source, expires_at) VALUES ($1,$2,$3,100,100,'manual',now() + interval '1 day')`, tid, uuid.New(), cid)
			},
			pass: func(t *testing.T, w *jobWorld) error {
				_, err := creditcmd.ExpireDue(ctx, w.db, 200)
				return err
			},
			effects: func(t *testing.T, w *jobWorld, tid uuid.UUID) int64 {
				return w.scalar(tid, `SELECT count(*) FROM credit_ledger WHERE tenant_id = $1 AND kind = 'expire'`, tid)
			},
			want: 2,
		},
		{
			name: "saga sweeper",
			seed: func(t *testing.T, w *jobWorld, tid uuid.UUID) {
				id := w.invoice(tid, "draft")
				if err := invoices.Finalize(ctx, tid, id); err != nil {
					t.Fatal(err)
				}
			},
			pass: func(t *testing.T, w *jobWorld) error {
				_, err := laterEng.Sweep(ctx, w.db, 100)
				return err
			},
			effects: func(t *testing.T, w *jobWorld, tid uuid.UUID) int64 {
				return w.scalar(tid, `SELECT COALESCE(sum(step_attempt), 0) - count(*) FROM invoice_sagas WHERE tenant_id = $1`, tid)
			},
			want: 1,
		},
		{
			name: "e-invoice sweep",
			seed: func(t *testing.T, w *jobWorld, tid uuid.UUID) {
				id := w.invoice(tid, "open")
				err := w.db.InTx(ctx, tid, pgx.TxOptions{}, func(tx pgx.Tx) error {
					_, err := einvoicecmd.Enqueue(ctx, tx, time.Now(), tid, "live", "invoice", id, nil, "INV-1", &commandsv1.IssueEInvoice{})
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
			},
			pass: func(t *testing.T, w *jobWorld) error {
				_, err := (&einvoicecmd.Commands{DB: w.db, Now: later.Now}).Sweep(ctx, 100)
				return err
			},
			effects: func(t *testing.T, w *jobWorld, tid uuid.UUID) int64 {
				return w.scalar(tid, `SELECT COALESCE(sum(attempts), 0) - count(*) FROM einvoice_documents WHERE tenant_id = $1`, tid)
			},
			want: 1,
		},
		{
			name: "credit note refund sweep",
			seed: func(t *testing.T, w *jobWorld, tid uuid.UUID) {
				inv := w.invoice(tid, "paid")
				w.exec(tid, `INSERT INTO invoice_sagas (tenant_id, id, invoice_id, state, version) VALUES ($1,$2,$3,'PAID',1)`, tid, uuid.New(), inv)
				w.exec(tid, `INSERT INTO credit_notes (tenant_id, id, customer_id, invoice_id, number, currency, total_minor, disposition, refund_state, refund_attempt, refund_command_id, refund_requested_at)
					VALUES ($1,$2,$3,$4,'CN-2026-000001','USD',100,'refund','requested',1,$5, now() - interval '1 hour')`, tid, uuid.New(), uuid.New(), inv, uuid.New())
			},
			pass: func(t *testing.T, w *jobWorld) error {
				return creditnotecmd.New(creditnotecmd.Config{DB: w.db, Engine: eng, Log: testkit.Logger()}).SweepRefunds(ctx)
			},
			effects: func(t *testing.T, w *jobWorld, tid uuid.UUID) int64 {
				return w.scalar(tid, `SELECT COALESCE(sum(refund_attempt), 0) - count(*) FROM credit_notes WHERE tenant_id = $1`, tid)
			},
			want: 1,
		},
		{
			name: "payment link expiry",
			seed: func(t *testing.T, w *jobWorld, tid uuid.UUID) {
				inv := w.invoice(tid, "open")
				w.exec(tid, `INSERT INTO payment_links (tenant_id, id, invoice_id, token_hash, expires_at) VALUES ($1,$2,$3,decode(md5(random()::text) || md5(random()::text), 'hex'), now() - interval '1 day')`,
					tid, uuid.New(), inv)
			},
			pass: func(t *testing.T, w *jobWorld) error {
				return hostedcmd.NewBackend(hostedcmd.BackendConfig{DB: w.db, Engine: eng, Log: testkit.Logger()}).ExpireLinks(ctx)
			},
			effects: func(t *testing.T, w *jobWorld, tid uuid.UUID) int64 {
				return w.scalar(tid, `SELECT count(*) FROM tenant_event_outbox WHERE tenant_id = $1 AND type = 'payment_link.expired'`, tid)
			},
			want: 1,
		},
		{
			name: "promotion reversals",
			seed: func(t *testing.T, w *jobWorld, tid uuid.UUID) {
				inv := w.invoice(tid, "void")
				w.exec(tid, `INSERT INTO promotion_reversals (tenant_id, invoice_id, target_kind) VALUES ($1,$2,'payment')`, tid, inv)
			},
			pass: func(t *testing.T, w *jobWorld) error {
				return hostedcmd.NewBackend(hostedcmd.BackendConfig{DB: w.db, Inputs: in, Engine: eng, Log: testkit.Logger()}).ReversePromotions(ctx)
			},
			effects: func(t *testing.T, w *jobWorld, tid uuid.UUID) int64 { return plans.reversed.get(tid) },
			want:    1,
		},
		{
			name: "re-rating resume",
			seed: func(t *testing.T, w *jobWorld, tid uuid.UUID) {
				w.exec(tid, `INSERT INTO rerating_jobs (tenant_id, id, period_id, plan_version_id, generation, status, created_at) VALUES ($1,$2,'2026-10',$3,1,'RUNNING', now() - interval '1 hour')`,
					tid, uuid.New(), uuid.New())
			},
			pass: func(t *testing.T, w *jobWorld) error {
				// The tenant lookup fails for good, which ends the job after its one visit: the visits are what is counted.
				return reratecmd.New(reratecmd.Config{DB: w.db, Inputs: &inputs.Inputs{Plans: plans, Admin: failing, Aggregator: agg}, Invoices: invoices, Log: testkit.Logger()}).Resume(ctx)
			},
			effects: func(t *testing.T, w *jobWorld, tid uuid.UUID) int64 { return failing.asked.get(tid) },
			want:    1,
		},
		{
			name:      "billing scheduler tick",
			seed:      func(*testing.T, *jobWorld, uuid.UUID) {},
			pass:      func(t *testing.T, w *jobWorld) error { return mgr.Tick(ctx) },
			effects:   func(t *testing.T, w *jobWorld, tid uuid.UUID) int64 { return agg.asked.get(tid) },
			want:      3, // the three closed periods of the lookback, each driven once per tick
			repeating: true,
		},
		{
			name: "fee run",
			seed: func(*testing.T, *jobWorld, uuid.UUID) {},
			pass: func(t *testing.T, w *jobWorld) error {
				return feecmd.New(feecmd.Config{DB: w.db, Inputs: in, Invoices: invoices, Now: nowClock.Now, Log: testkit.Logger()}).Bill(ctx)
			},
			effects:   func(t *testing.T, w *jobWorld, tid uuid.UUID) int64 { return plans.applied.get(tid) },
			want:      1,
			repeating: true,
		},
	}
}

// creditExpiryIsolatesFailures: when writing one tenant's expiry fails, the others are still written off and the failure is reported; the tenant's lots are untouched
// and are written off by the next pass once the failure is gone.
func creditExpiryIsolatesFailures(t *testing.T, w *jobWorld) {
	ctx := context.Background()
	for _, jt := range w.tenants {
		w.exec(jt.id, `INSERT INTO credit_lots (tenant_id, id, customer_id, granted, remaining, source, expires_at) VALUES ($1,$2,$3,50,50,'manual',now() - interval '1 hour')`, jt.id, uuid.New(), uuid.New())
	}
	bad := w.tenants[1].id
	// The ledger refuses one tenant's rows, in the place that tenant is in.
	breaker := func(create bool) {
		w.t.Helper()
		dsn, path := w.pooledMigrator, ""
		if w.inCell(bad) {
			dsn, path = w.cellMigrator, `SET search_path TO "`+tenantdir.Schema(bad)+`"; `
		}
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close(ctx) }()
		sql := path + `DROP TRIGGER IF EXISTS zz_refuse ON credit_ledger; DROP FUNCTION IF EXISTS zz_refuse_fn()`
		if create {
			sql = path + fmt.Sprintf(`CREATE FUNCTION zz_refuse_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.tenant_id = '%s' THEN RAISE EXCEPTION 'refused'; END IF; RETURN NEW; END $$;
			CREATE TRIGGER zz_refuse BEFORE INSERT ON credit_ledger FOR EACH ROW EXECUTE FUNCTION zz_refuse_fn()`, bad)
		}
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	expired := func(tid uuid.UUID) int64 {
		return w.scalar(tid, `SELECT count(*) FROM credit_lots WHERE tenant_id = $1 AND remaining = 0`, tid)
	}
	// What the earlier cases of the world left expired is not this case's business: only the lots of this case are counted by a baseline.
	before := map[uuid.UUID]int64{}
	for _, jt := range w.tenants {
		before[jt.id] = expired(jt.id)
	}
	breaker(true)
	_, err := creditcmd.ExpireDue(ctx, w.db, 200)
	if err == nil {
		t.Fatal("ExpireDue did not report the tenant it could not write off")
	}
	for _, jt := range w.tenants {
		got := expired(jt.id) - before[jt.id]
		want := int64(1)
		if jt.id == bad {
			want = 0
		}
		if got != want {
			t.Errorf("tenant %s: %d lots written off while %s was failing, want %d", jt.id, got, bad, want)
		}
	}
	breaker(false)
	if _, err := creditcmd.ExpireDue(ctx, w.db, 200); err != nil {
		t.Fatal(err)
	}
	if got := expired(bad) - before[bad]; got != 1 {
		t.Errorf("the failed tenant had %d lots written off by the next pass, want 1", got)
	}
}

// gaugesCountEveryTenantOnce: the run and saga gauges add up the places, so a tenant is counted once whichever place it is in.
func gaugesCountEveryTenantOnce(t *testing.T, w *jobWorld) {
	ctx := context.Background()
	mgr, err := runcmd.NewManager(runcmd.Config{DB: w.db, Log: testkit.Logger()})
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := mgr.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, jt := range w.tenants {
		for _, p := range []string{"2025-01", "2025-02"} {
			w.exec(jt.id, `INSERT INTO billing_runs (tenant_id, period_id, status, cutoff_at) VALUES ($1,$2,'FAILED',now())`, jt.id, p)
		}
	}
	after, _, err := mgr.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := after["FAILED"] - before["FAILED"]; got != 8 {
		t.Errorf("FAILED runs counted: %d more, want 8 (4 tenants, 2 each, each tenant once)", got)
	}
}
