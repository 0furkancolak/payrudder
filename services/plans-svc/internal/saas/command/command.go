// Package command is the write side of Payrudder as a SaaS (phase 55, ADR 0024): it creates the platform tenant and seeds its plans, makes every
// organisation a customer of it with a subscription, refuses a resource beyond the plan, and moves an organisation between plans.
//
// The platform tenant is an ordinary tenant, so everything it owns is written through the PlansService handlers in a context that acts as that tenant:
// the same code, the same validation and the same row-level security as for any customer of Payrudder. The system pool is used for what is not tenant
// data (accounts, apps, members) and for reads that name the platform tenant explicitly.
package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/model"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Plans is the part of PlansService the SaaS mode drives. The server of plans-svc satisfies it; a test may pass a fake.
type Plans interface {
	CreateMeter(context.Context, *plansv1.CreateMeterRequest) (*plansv1.CreateMeterResponse, error)
	CreatePlan(context.Context, *plansv1.CreatePlanRequest) (*plansv1.CreatePlanResponse, error)
	ListPlans(context.Context, *plansv1.ListPlansRequest) (*plansv1.ListPlansResponse, error)
	PublishPlanVersion(context.Context, *plansv1.PublishPlanVersionRequest) (*plansv1.PublishPlanVersionResponse, error)
	CreateCustomer(context.Context, *plansv1.CreateCustomerRequest) (*plansv1.CreateCustomerResponse, error)
	CreateSubscription(context.Context, *plansv1.CreateSubscriptionRequest) (*plansv1.CreateSubscriptionResponse, error)
	CancelSubscription(context.Context, *plansv1.CancelSubscriptionRequest) (*plansv1.CancelSubscriptionResponse, error)
	SetEntitlementOverride(context.Context, *plansv1.SetEntitlementOverrideRequest) (*plansv1.SetEntitlementOverrideResponse, error)
	DeleteEntitlementOverride(context.Context, *plansv1.DeleteEntitlementOverrideRequest) (*plansv1.DeleteEntitlementOverrideResponse, error)
}

// Config is the SaaS mode of one plans-svc (PAYRUDDER_SAAS and its siblings).
type Config struct {
	// Enabled switches the whole mode on. Off: no platform tenant, no limits, every call answers "not enabled".
	Enabled bool
	// Environment of the platform tenant when it is created: live (real providers) or sandbox (mock-psp, for development).
	Environment env.Env
	// Currency and Timezone of the platform tenant when it is created.
	Currency string
	Timezone string
	// TrialDays of a first move to a paid plan; 0 gives none.
	TrialDays int
	// SweepEvery is how often the sweep makes the organisations that are not customers yet customers.
	SweepEvery time.Duration
	// Now is the clock (default time.Now).
	Now func() time.Time
}

// Default fills what is not set.
func (c Config) Default() Config {
	if c.Environment == "" {
		c.Environment = env.Live
	}
	if c.Currency == "" {
		c.Currency = "USD"
	}
	if c.Timezone == "" {
		c.Timezone = "UTC"
	}
	if c.SweepEvery <= 0 {
		c.SweepEvery = time.Minute
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// Commands changes the SaaS state.
type Commands struct {
	cfg   Config
	pool  *pgxpool.Pool
	plans Plans
	qry   *query.Queries
	log   *slog.Logger
}

// New returns the commands. pool is the system pool.
func New(cfg Config, pool *pgxpool.Pool, plans Plans, qry *query.Queries, log *slog.Logger) *Commands {
	return &Commands{cfg: cfg.Default(), pool: pool, plans: plans, qry: qry, log: log}
}

// Enabled reports whether the SaaS mode is on.
func (c *Commands) Enabled() bool { return c.cfg.Enabled }

// platformAccountName is the name of the platform account.
const platformAccountName = "Payrudder platform"

// Bootstrap creates the platform tenant when it does not exist and seeds the plans that are missing. It is idempotent and safe with several replicas
// starting at once (an advisory lock serialises the creation, and the unique index on accounts allows one platform account). Plans an operator has edited
// are never touched: a plan is seeded only if its code does not exist.
func (c *Commands) Bootstrap(ctx context.Context) error {
	if !c.cfg.Enabled {
		return nil
	}
	for {
		done, err := c.bootstrapOnce(ctx)
		if err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// bootstrapOnce does the bootstrap if it can take the lock, and reports false if another replica has it. One bootstrap at a time: the lock lives in a
// transaction of its own that stays open while the platform tenant is made and the plans are seeded (those commit separately, because the seeding runs as the
// platform tenant and must see it), and is released when this returns. The lock is tried, not waited for, so a waiting replica holds no connection.
func (c *Commands) bootstrapOnce(ctx context.Context) (bool, error) {
	got := false
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ok, err := sqlc.New(tx).SaasTryLockBootstrap(ctx)
		if err != nil || !ok {
			return err
		}
		got = true
		if err := c.createPlatform(ctx); err != nil {
			return fmt.Errorf("saas: platform tenant: %w", err)
		}
		p, err := c.qry.Platform(ctx)
		if err != nil {
			return err
		}
		if err := c.seed(p.Context(ctx)); err != nil {
			return fmt.Errorf("saas: seeding plans: %w", err)
		}
		c.log.InfoContext(ctx, "saas: platform tenant ready", "tenant_id", p.TenantID, "environment", p.Environment)
		return nil
	})
	return got, err
}

func (c *Commands) createPlatform(ctx context.Context) error {
	return pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.SaasGetPlatformAccount(ctx); err == nil {
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		a, err := q.SaasInsertPlatformAccount(ctx, sqlc.SaasInsertPlatformAccountParams{ID: idgen.NewUUIDv7(), Name: platformAccountName})
		if err != nil {
			return err
		}
		app, err := q.InsertApp(ctx, sqlc.InsertAppParams{ID: idgen.NewUUIDv7(), AccountID: a.ID, Name: platformAccountName, Slug: "platform"})
		if err != nil {
			return err
		}
		_, err = q.InsertTenant(ctx, sqlc.InsertTenantParams{
			ID: idgen.NewUUIDv7(), AccountID: a.ID, AppID: app.ID, Environment: c.cfg.Environment.String(), Name: platformAccountName, Tier: "enterprise",
			Currency: c.cfg.Currency, Timezone: c.cfg.Timezone, GracePeriod: convert.Interval(72 * time.Hour), Status: "active",
		})
		return err
	})
}

// seed makes the meters and the plans of the platform tenant that do not exist. Each step tolerates "already there", so a half-finished seed completes
// at the next start.
func (c *Commands) seed(ctx context.Context) error {
	for _, m := range model.Meters() {
		req := &plansv1.CreateMeterRequest{Code: m.Code, Aggregation: plansv1.Aggregation_AGGREGATION_COUNT, UnitLabel: m.Unit, Category: "platform"}
		if !m.Count {
			req.Aggregation, req.ValueProperty = plansv1.Aggregation_AGGREGATION_SUM, m.Code
		}
		if _, err := c.plans.CreateMeter(ctx, req); err != nil && status.Code(err) != codes.AlreadyExists {
			return fmt.Errorf("meter %s: %w", m.Code, err)
		}
	}
	existing, err := c.plans.ListPlans(ctx, &plansv1.ListPlansRequest{})
	if err != nil {
		return err
	}
	have := map[string]*plansv1.Plan{}
	for _, p := range existing.GetPlans() {
		have[p.GetCode()] = p
	}
	for _, spec := range model.DefaultPlans() {
		if _, ok := have[spec.Code]; !ok {
			if _, err := c.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: spec.Code, Name: spec.Name}); err != nil && status.Code(err) != codes.AlreadyExists {
				return fmt.Errorf("plan %s: %w", spec.Code, err)
			}
		} else if have[spec.Code].GetLatestVersion() > 0 {
			continue
		}
		req := &plansv1.PublishPlanVersionRequest{PlanCode: spec.Code, BaseFeeMinor: spec.MonthlyFee}
		if spec.MonthlyFee == 0 {
			// A plan version needs a fee or a price. Free has no fee, so it prices the events at nothing: the line is the place an operator who wants to
			// charge for events above the limit (a soft limit) puts the price.
			req.Prices = []*plansv1.Price{{MeterCode: model.Events.Meter(), Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: "0"}}}}
		}
		for _, r := range model.Resources {
			l := spec.Limits.Of(r)
			req.Entitlements = append(req.Entitlements, &plansv1.Entitlement{MeterCode: r.Meter(), Limit: query.Itoa(l.Units), Mode: modeProto(l.Mode)})
		}
		if _, err := c.plans.PublishPlanVersion(ctx, req); err != nil {
			return fmt.Errorf("plan %s version: %w", spec.Code, err)
		}
	}
	return nil
}

func modeProto(m model.Mode) plansv1.QuotaMode {
	switch m {
	case model.Soft:
		return plansv1.QuotaMode_QUOTA_MODE_SOFT
	case model.Notify:
		return plansv1.QuotaMode_QUOTA_MODE_NOTIFY
	}
	return plansv1.QuotaMode_QUOTA_MODE_HARD
}

// Ensure makes the account a customer of the platform tenant, on Free if it has no subscription in force. It is idempotent and safe to call
// concurrently: the customer's external id is unique and a customer has at most one subscription at a time, so a loser of a race finds the winner's rows.
func (c *Commands) Ensure(ctx context.Context, account uuid.UUID) error {
	if !c.cfg.Enabled {
		return nil
	}
	p, err := c.qry.Platform(ctx)
	if err != nil {
		return err
	}
	if p.AccountID == account {
		return nil
	}
	pctx := p.Context(ctx)
	sq := sqlc.New(c.pool)
	if _, err := sq.SaasGetCustomer(ctx, sqlc.SaasGetCustomerParams{TenantID: p.TenantID, AccountID: account.String()}); errors.Is(err, pgx.ErrNoRows) {
		a, err := sq.SaasGetAccount(ctx, account)
		if err != nil {
			return convert.Err(err, "account")
		}
		email, err := sq.SaasOwnerEmail(ctx, account)
		if errors.Is(err, pgx.ErrNoRows) {
			email = "billing+" + account.String() + "@organisations.invalid"
		} else if err != nil {
			return convert.Err(err, "account")
		}
		if _, err := c.plans.CreateCustomer(pctx, &plansv1.CreateCustomerRequest{ExternalId: account.String(), Name: a.Name, Email: email}); err != nil && status.Code(err) != codes.AlreadyExists {
			return err
		}
	} else if err != nil {
		return convert.Err(err, "customer")
	}
	st, err := c.qry.Standing(ctx, c.pool, p, account)
	if err != nil {
		return err
	}
	if st.Sub != nil {
		return nil
	}
	_, err = c.plans.CreateSubscription(pctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: account.String(), PlanCode: model.PlanFree})
	if status.Code(err) == codes.AlreadyExists {
		return nil // a concurrent Ensure won
	}
	return err
}

// RequireRoom is the guard of the places that create a resource: it refuses, with the key the console turns into an upgrade prompt, when one more of r
// would pass the hard limit of the plan the account is on. It runs in the caller's transaction on the system pool, which must already hold the account's lock
// (LockAccount), so two creations cannot both see room for the last one. With SaaS off, for the platform's own account and for resources a plan does not
// limit it allows.
func (c *Commands) RequireRoom(ctx context.Context, db sqlc.DBTX, account uuid.UUID, r model.Resource) error {
	if !c.cfg.Enabled {
		return nil
	}
	p, err := c.qry.Platform(ctx)
	if err != nil {
		return err
	}
	if p.AccountID == account {
		return nil
	}
	st, err := c.qry.Standing(ctx, db, p, account)
	if err != nil {
		return err
	}
	l := st.Limits.Of(r)
	if l.Unlimited() || l.Mode != model.Hard {
		return nil
	}
	u, err := c.qry.Usage(ctx, db, account)
	if err != nil {
		return err
	}
	if l.Blocks(u.Of(r)) {
		return model.QuotaError(r)
	}
	return nil
}

func downgradeBlocked(r model.Resource) error {
	return errkey.Newf(codes.FailedPrecondition, "plans.saas.downgrade_blocked", "the account uses more %s than that plan allows: remove some first", r)
}

func samePlan() error {
	return errkey.New(codes.FailedPrecondition, "plans.saas.same_plan", "the account is on that plan already")
}

// ChangePlan moves the account to the latest version of a plan: the subscription in force ends now (the unused part of a fee paid in advance is credited)
// and the new one starts now. A first move to a paid plan starts with the trial. byOperator skips the checks a person's move has (the trial, the usage that
// must fit) and does not treat "already on that plan" as an error.
//
// The move is two steps, each atomic, in an order that is safe to stop between: the end of the old subscription, then the start of the new. A stop between
// leaves the account with no subscription, which reads as Free, and the sweep or the next call completes it; a failed start puts the old plan back.
func (c *Commands) ChangePlan(ctx context.Context, account uuid.UUID, code string, byOperator bool) error {
	if !c.cfg.Enabled {
		return query.Disabled()
	}
	if err := c.Ensure(ctx, account); err != nil {
		return err
	}
	p, err := c.qry.Platform(ctx)
	if err != nil {
		return err
	}
	if p.AccountID == account {
		return errkey.New(codes.InvalidArgument, "plans.saas.platform_account", "the platform's own account has no plan")
	}
	target, err := c.qry.Latest(ctx, c.pool, p, code)
	if err != nil {
		return err
	}
	st, err := c.qry.Standing(ctx, c.pool, p, account)
	if err != nil {
		return err
	}
	if st.Sub != nil && st.Plan.Code == target.Code {
		if byOperator {
			return nil
		}
		return samePlan()
	}
	trialDays := int32(0)
	if !byOperator {
		if err := c.requireFits(ctx, p, account, target); err != nil {
			return err
		}
		if st.Plan.FeeMinor == 0 && target.FeeMinor > 0 && c.cfg.TrialDays > 0 {
			had, err := c.qry.HadTrial(ctx, c.pool, p, account)
			if err != nil {
				return err
			}
			if !had {
				trialDays = int32(c.cfg.TrialDays) //nolint:gosec // a config value of a few days
			}
		}
	}
	pctx := p.Context(ctx)
	if st.Sub != nil {
		if err := c.endSubscription(pctx, st); err != nil {
			return err
		}
	}
	_, err = c.plans.CreateSubscription(pctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: account.String(), PlanCode: target.Code, TrialDays: trialDays})
	if err != nil {
		if st.Sub != nil && st.Plan.Code != "" {
			// Put the old plan back so a failed upgrade does not silently drop a paying account to Free.
			if _, rerr := c.plans.CreateSubscription(pctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: account.String(), PlanCode: st.Plan.Code}); rerr != nil {
				c.log.ErrorContext(ctx, "saas: the old plan was not restored after a failed plan change", "account_id", account, "plan", st.Plan.Code, "error", rerr)
			}
		}
		return err
	}
	c.log.InfoContext(ctx, "saas: plan changed", "account_id", account, "from", st.Plan.Code, "to", target.Code, "operator", byOperator)
	return nil
}

// endSubscription ends the subscription in force now, crediting the unused part of a fee paid in advance when the engine allows it for this one.
func (c *Commands) endSubscription(pctx context.Context, st query.Standing) error {
	id := st.Sub.ID.String()
	if st.Plan.FeeMinor > 0 && st.Sub.State == "active" {
		_, err := c.plans.CancelSubscription(pctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: id, CreditUnused: true})
		if err == nil || status.Code(err) != codes.FailedPrecondition && status.Code(err) != codes.InvalidArgument {
			return err
		}
	}
	_, err := c.plans.CancelSubscription(pctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: id})
	return err
}

// requireFits refuses a move to a plan whose limits the account's usage does not fit.
func (c *Commands) requireFits(ctx context.Context, p query.Platform, account uuid.UUID, target query.Plan) error {
	sq := sqlc.New(c.pool)
	rows, err := sq.SaasEntitlements(ctx, sqlc.SaasEntitlementsParams{TenantID: p.TenantID, PlanVersionID: target.VersionID})
	if err != nil {
		return convert.Err(err, "entitlement")
	}
	es := make([]model.Entitlement, len(rows))
	for i, r := range rows {
		es[i] = model.Entitlement{Meter: r.MeterCode, Units: r.LimitUnits, Mode: r.Mode}
	}
	limits := model.FromEntitlements(es)
	u, err := c.qry.Usage(ctx, c.pool, account)
	if err != nil {
		return err
	}
	for _, r := range []model.Resource{model.Apps, model.Members, model.APIKeys} {
		if l := limits.Of(r); l.Mode == model.Hard && !l.Fits(u.Of(r)) {
			return downgradeBlocked(r)
		}
	}
	return nil
}

// SetLimits gives the account limits of its own, replacing its plan's on those resources (0 is unlimited). A resource that is not named keeps what it has.
func (c *Commands) SetLimits(ctx context.Context, account uuid.UUID, limits map[string]int64) error {
	if !c.cfg.Enabled {
		return query.Disabled()
	}
	for name, n := range limits {
		if _, ok := model.ParseResource(name); !ok {
			return errkey.Newf(codes.InvalidArgument, "plans.saas.bad_resource", "%q is not a limited resource: events, apps, members or api_keys", name)
		}
		if n < 0 {
			return domain.Invalid("limits", "a limit is zero (unlimited) or more")
		}
	}
	if err := c.Ensure(ctx, account); err != nil {
		return err
	}
	p, err := c.qry.Platform(ctx)
	if err != nil {
		return err
	}
	st, err := c.qry.Standing(ctx, c.pool, p, account)
	if err != nil {
		return err
	}
	pctx := p.Context(ctx)
	for _, r := range model.Resources {
		n, ok := limits[string(r)]
		if !ok {
			continue
		}
		mode := st.Limits.Of(r).Mode
		if _, err := c.plans.SetEntitlementOverride(pctx, &plansv1.SetEntitlementOverrideRequest{CustomerExternalId: account.String(), MeterCode: r.Meter(), Limit: strconv.FormatInt(n, 10), Mode: modeProto(mode)}); err != nil {
			return err
		}
	}
	return nil
}

// ClearLimits drops the account's own limits, so its plan's apply again.
func (c *Commands) ClearLimits(ctx context.Context, account uuid.UUID) error {
	if !c.cfg.Enabled {
		return query.Disabled()
	}
	p, err := c.qry.Platform(ctx)
	if err != nil {
		return err
	}
	st, err := c.qry.Standing(ctx, c.pool, p, account)
	if err != nil {
		return err
	}
	pctx := p.Context(ctx)
	for r := range st.Own {
		if _, err := c.plans.DeleteEntitlementOverride(pctx, &plansv1.DeleteEntitlementOverrideRequest{CustomerExternalId: account.String(), MeterCode: r.Meter()}); err != nil && status.Code(err) != codes.NotFound {
			return err
		}
	}
	return nil
}

// sweepBatch is how many accounts one pass of the sweep takes.
const sweepBatch = 50

// SweepOnce makes the organisations that are not customers of the platform tenant (or have no subscription in force) customers on Free, and returns how
// many it handled. Accounts are made in many places (sign-up, passwordless sign-in, Google, the operator); this one pass is what guarantees none is missed.
func (c *Commands) SweepOnce(ctx context.Context) (int, error) {
	if !c.cfg.Enabled {
		return 0, nil
	}
	p, err := c.qry.Platform(ctx)
	if err != nil {
		return 0, err
	}
	ids, err := sqlc.New(c.pool).SaasAccountsToEnsure(ctx, sqlc.SaasAccountsToEnsureParams{TenantID: p.TenantID, Batch: sweepBatch})
	if err != nil {
		return 0, convert.Err(err, "account")
	}
	n := 0
	for _, id := range ids {
		if err := c.Ensure(ctx, id); err != nil {
			c.log.WarnContext(ctx, "saas: could not make an account a customer", "account_id", id, "error", err)
			continue
		}
		n++
	}
	return n, nil
}

// Run sweeps until ctx ends. A pass that fills a whole batch runs again at once.
func (c *Commands) Run(ctx context.Context) error {
	if !c.cfg.Enabled {
		<-ctx.Done()
		return nil
	}
	t := time.NewTicker(c.cfg.SweepEvery)
	defer t.Stop()
	for {
		for {
			n, err := c.SweepOnce(ctx)
			if err != nil {
				c.log.WarnContext(ctx, "saas: sweep failed", "error", err)
			}
			if err != nil || n < sweepBatch || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
