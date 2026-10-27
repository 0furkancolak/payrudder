// Package query is the read side of Payrudder as a SaaS (phase 55, ADR 0024): the platform tenant, the standing of an organisation (its subscription, the
// limits it lives by, what it uses) and the views built from them. It reads on the system pool and names the platform tenant in every statement.
package query

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/model"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Platform is the platform tenant: the account and the tenant that own Payrudder's own plans.
type Platform struct {
	AccountID   uuid.UUID
	TenantID    uuid.UUID
	Environment string
	Timezone    string
	Currency    string
	Tier        string
}

// Proto is the platform as the API shows it.
func (p Platform) Proto() *plansv1.PlatformTenant {
	return &plansv1.PlatformTenant{Id: p.TenantID.String(), Environment: p.Environment, Timezone: p.Timezone, Currency: p.Currency}
}

// Info is the tenant context a call to the PlansService handlers runs in.
func (p Platform) Info() tenant.Info {
	return tenant.Info{ID: p.TenantID, Environment: p.Environment, Timezone: p.Timezone, Tier: p.Tier}
}

// Context returns ctx acting as the platform tenant.
func (p Platform) Context(ctx context.Context) context.Context {
	return tenant.NewContext(ctx, p.Info())
}

// NotReady is the answer while SaaS is on but the platform tenant does not exist yet (plans-svc creates it when it starts).
func NotReady() error {
	return errkey.New(codes.Unavailable, "plans.saas.not_ready", "the platform tenant is not set up yet: try again in a moment")
}

// Disabled is the answer to a write while SaaS is off.
func Disabled() error {
	return errkey.New(codes.FailedPrecondition, "plans.saas.disabled", "Payrudder is not running as a SaaS here: there are no plans")
}

// Queries reads the SaaS state.
type Queries struct {
	db        sqlc.DBTX
	trialDays int
	now       func() time.Time
	// places says where the platform tenant's own rows (plans, customers, subscriptions, limits) are; nil, or with cells off, is the pool db.
	places *placement.Places

	mu       sync.RWMutex
	platform *Platform
}

// New returns the queries on the system pool (or a transaction of it, in a test). trialDays is the trial a first move to a paid plan gets.
func New(db sqlc.DBTX, trialDays int, now func() time.Time) *Queries {
	return &Queries{db: db, trialDays: trialDays, now: now}
}

// WithPlaces tells the queries where the platform tenant's rows are (ADR 0018): when the tenant is in a cell, what is about its plans and customers is read there, in
// a transaction of its own, and the rest (accounts, members, keys) on db as before.
func (q *Queries) WithPlaces(p *placement.Places) *Queries {
	q.places = p
	return q
}

// onPlatform runs fn on the connection that sees the platform tenant's rows: db itself when they are in the pooled database.
func (q *Queries) onPlatform(ctx context.Context, db sqlc.DBTX, p Platform, fn func(db sqlc.DBTX) error) error {
	if q.places == nil {
		return fn(db)
	}
	return q.places.On(ctx, p.TenantID, db, fn)
}

// TrialDays is the trial of a first move to a paid plan.
func (q *Queries) TrialDays() int { return q.trialDays }

// Platform returns the platform tenant. It never changes once created, so it is read once.
func (q *Queries) Platform(ctx context.Context) (Platform, error) {
	q.mu.RLock()
	p := q.platform
	q.mu.RUnlock()
	if p != nil {
		return *p, nil
	}
	t, err := sqlc.New(q.db).SaasGetPlatformTenant(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return Platform{}, NotReady()
	}
	if err != nil {
		return Platform{}, convert.Err(err, "platform")
	}
	got := Platform{AccountID: t.AccountID, TenantID: t.ID, Environment: t.Environment, Timezone: t.Timezone, Currency: t.Currency, Tier: t.Tier}
	q.mu.Lock()
	q.platform = &got
	q.mu.Unlock()
	return got, nil
}

// Sub is the subscription an organisation has in the platform tenant.
type Sub struct {
	ID         uuid.UUID
	CustomerID uuid.UUID
	State      string
	TrialEnds  *time.Time
}

// Plan is a plan version of the platform tenant.
type Plan struct {
	VersionID uuid.UUID
	Code      string
	Name      string
	Version   int32
	FeeMinor  int64
}

// Standing is where an organisation stands: the plan it is on, the limits it lives by and which of them are its own.
type Standing struct {
	Platform Platform
	Account  uuid.UUID
	// Sub is nil when the organisation has no subscription in force: it is then on Free.
	Sub  *Sub
	Plan Plan
	// PlanLimits are the plan version's; Limits are those with the organisation's own on top.
	PlanLimits model.Limits
	Limits     model.Limits
	Own        map[model.Resource]bool
}

// State is the subscription's state in words ("none" without one).
func (s Standing) State(now time.Time) string {
	if s.Sub == nil {
		return "none"
	}
	return s.Sub.State
}

// versions are the latest versions of the platform tenant's plans by code.
func (q *Queries) versions(ctx context.Context, db sqlc.DBTX, p Platform) (map[string]Plan, error) {
	var out map[string]Plan
	err := q.onPlatform(ctx, db, p, func(db sqlc.DBTX) error {
		var err error
		out, err = q.versionsOn(ctx, db, p)
		return err
	})
	return out, err
}

// versionsOn is versions on the connection that sees the platform tenant's rows.
func (q *Queries) versionsOn(ctx context.Context, db sqlc.DBTX, p Platform) (map[string]Plan, error) {
	rows, err := sqlc.New(db).SaasLatestPlanVersions(ctx, p.TenantID)
	if err != nil {
		return nil, convert.Err(err, "plan")
	}
	out := make(map[string]Plan, len(rows))
	for _, r := range rows {
		out[r.PlanCode] = Plan{VersionID: r.ID, Code: r.PlanCode, Name: r.PlanName, Version: r.Version, FeeMinor: r.BaseFeeMinor}
	}
	return out, nil
}

// Latest returns the latest version of a plan of the platform tenant by its code.
func (q *Queries) Latest(ctx context.Context, db sqlc.DBTX, p Platform, code string) (Plan, error) {
	vs, err := q.versions(ctx, db, p)
	if err != nil {
		return Plan{}, err
	}
	return latestOf(vs, code)
}

func latestOf(vs map[string]Plan, code string) (Plan, error) {
	if pl, ok := vs[code]; ok {
		return pl, nil
	}
	return Plan{}, errkey.Newf(codes.InvalidArgument, "plans.saas.unknown_plan", "there is no plan %q", code)
}

// PlanLimits are the limits of a version of a plan of the platform tenant.
func (q *Queries) PlanLimits(ctx context.Context, db sqlc.DBTX, p Platform, versionID uuid.UUID) (limits model.Limits, err error) {
	err = q.onPlatform(ctx, db, p, func(db sqlc.DBTX) error {
		limits, err = q.planLimits(ctx, db, p, versionID)
		return err
	})
	return limits, err
}

// Customer finds the organisation's customer record in the platform tenant; found is false when there is none yet.
func (q *Queries) Customer(ctx context.Context, db sqlc.DBTX, p Platform, account uuid.UUID) (cust sqlc.SaasGetCustomerRow, found bool, err error) {
	err = q.onPlatform(ctx, db, p, func(db sqlc.DBTX) error {
		var err error
		cust, err = sqlc.New(db).SaasGetCustomer(ctx, sqlc.SaasGetCustomerParams{TenantID: p.TenantID, AccountID: account.String()})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		found = err == nil
		return err
	})
	return cust, found, err
}

func (q *Queries) planLimits(ctx context.Context, db sqlc.DBTX, p Platform, versionID uuid.UUID) (model.Limits, error) {
	rows, err := sqlc.New(db).SaasEntitlements(ctx, sqlc.SaasEntitlementsParams{TenantID: p.TenantID, PlanVersionID: versionID})
	if err != nil {
		return nil, convert.Err(err, "entitlement")
	}
	es := make([]model.Entitlement, len(rows))
	for i, r := range rows {
		es[i] = model.Entitlement{Meter: r.MeterCode, Units: r.LimitUnits, Mode: r.Mode}
	}
	return model.FromEntitlements(es), nil
}

// Standing reads where the organisation stands. db is the pool or a transaction (the guard reads inside the identity transaction that holds the account's
// lock). An organisation with no subscription in force is on the latest Free.
func (q *Queries) Standing(ctx context.Context, db sqlc.DBTX, p Platform, account uuid.UUID) (st Standing, err error) {
	err = q.onPlatform(ctx, db, p, func(db sqlc.DBTX) error {
		var err error
		st, err = q.standingOn(ctx, db, p, account)
		return err
	})
	return st, err
}

// standingOn is Standing on the connection that sees the platform tenant's rows.
func (q *Queries) standingOn(ctx context.Context, db sqlc.DBTX, p Platform, account uuid.UUID) (Standing, error) {
	st := Standing{Platform: p, Account: account}
	sq := sqlc.New(db)
	row, err := sq.SaasCurrentSubscription(ctx, sqlc.SaasCurrentSubscriptionParams{TenantID: p.TenantID, AccountID: account.String()})
	switch {
	case err == nil:
		state := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, q.now())
		st.Sub = &Sub{ID: row.ID, CustomerID: row.CustomerID, State: string(state), TrialEnds: row.TrialEndsAt}
		st.Plan = Plan{VersionID: row.PlanVersionID, Code: row.PlanCode, Name: row.PlanName, Version: row.PlanVersion, FeeMinor: row.BaseFeeMinor}
	case errors.Is(err, pgx.ErrNoRows):
		vs, ferr := q.versionsOn(ctx, db, p)
		if ferr != nil {
			return st, ferr
		}
		free, ferr := latestOf(vs, model.PlanFree)
		if ferr != nil {
			// The operator deleted nothing (plans cannot be deleted) but the seed may not have run: no plan, no limits.
			if errkey.Of(ferr) != "plans.saas.unknown_plan" {
				return st, ferr
			}
			st.Limits, st.PlanLimits = model.Limits{}, model.Limits{}
			return st, nil
		}
		st.Plan = free
	default:
		return st, convert.Err(err, "subscription")
	}
	if st.PlanLimits, err = q.planLimits(ctx, db, p, st.Plan.VersionID); err != nil {
		return st, err
	}
	rows, err := sq.SaasOverrides(ctx, sqlc.SaasOverridesParams{TenantID: p.TenantID, AccountID: account.String()})
	if err != nil {
		return st, convert.Err(err, "override")
	}
	es := make([]model.Entitlement, len(rows))
	for i, r := range rows {
		es[i] = model.Entitlement{Meter: r.MeterCode, Units: r.LimitUnits, Mode: r.Mode}
	}
	st.Limits, st.Own = model.Merge(st.PlanLimits, model.FromEntitlements(es))
	return st, nil
}

// Usage is what an account uses of the resources counted in the database. The events are counted by limits-svc.
type Usage struct {
	Apps, Members, Invites, Keys int64
}

// Of is the usage of a resource: members include the open invitations, which hold a seat.
func (u Usage) Of(r model.Resource) int64 {
	switch r {
	case model.Apps:
		return u.Apps
	case model.Members:
		return u.Members + u.Invites
	case model.APIKeys:
		return u.Keys
	}
	return 0
}

// Usage counts an account's resources.
func (q *Queries) Usage(ctx context.Context, db sqlc.DBTX, account uuid.UUID) (Usage, error) {
	sq := sqlc.New(db)
	var u Usage
	var err error
	if u.Apps, err = sq.SaasCountApps(ctx, account); err != nil {
		return u, convert.Err(err, "apps")
	}
	if u.Members, err = sq.SaasCountMembers(ctx, account); err != nil {
		return u, convert.Err(err, "members")
	}
	if u.Invites, err = sq.SaasCountOpenInvites(ctx, account); err != nil {
		return u, convert.Err(err, "invitations")
	}
	if u.Keys, err = sq.SaasCountActiveKeys(ctx, account); err != nil {
		return u, convert.Err(err, "api keys")
	}
	return u, nil
}

func limitProto(r model.Resource, l model.Limit) *plansv1.PlanLimit {
	return &plansv1.PlanLimit{Resource: string(r), Limit: l.Units, Mode: string(l.Mode)}
}

func offerOf(pl Plan, limits model.Limits, currency string, trialDays int32, current bool) *plansv1.PlanOffer {
	o := &plansv1.PlanOffer{Code: pl.Code, Name: pl.Name, Version: pl.Version, BaseFeeMinor: pl.FeeMinor, Currency: currency, Interval: "month", TrialDays: trialDays, Current: current}
	for _, r := range model.Resources {
		o.Limits = append(o.Limits, limitProto(r, limits.Of(r)))
	}
	return o
}

// Offers are the plans an organisation can be on: the latest version of each plan of the platform tenant, cheapest first.
func (q *Queries) Offers(ctx context.Context, db sqlc.DBTX, st Standing, hadTrial bool) ([]*plansv1.PlanOffer, error) {
	vs, err := q.versions(ctx, db, st.Platform)
	if err != nil {
		return nil, err
	}
	limitsOf := make(map[uuid.UUID]model.Limits, len(vs))
	for _, pl := range vs {
		if limitsOf[pl.VersionID], err = q.PlanLimits(ctx, db, st.Platform, pl.VersionID); err != nil {
			return nil, err
		}
	}
	plans := make([]Plan, 0, len(vs))
	for _, pl := range vs {
		plans = append(plans, pl)
	}
	sort.Slice(plans, func(i, j int) bool {
		if plans[i].FeeMinor != plans[j].FeeMinor {
			return plans[i].FeeMinor < plans[j].FeeMinor
		}
		return plans[i].Code < plans[j].Code
	})
	out := make([]*plansv1.PlanOffer, 0, len(plans))
	for _, pl := range plans {
		limits := limitsOf[pl.VersionID]
		trial := int32(0)
		if pl.FeeMinor > 0 && !hadTrial {
			trial = int32(q.trialDays) //nolint:gosec // a config value of a few days
		}
		out = append(out, offerOf(pl, limits, st.Platform.Currency, trial, pl.Code == st.Plan.Code))
	}
	return out, nil
}

// HadTrial reports whether the organisation has had a trial already: a trial is given once.
func (q *Queries) HadTrial(ctx context.Context, db sqlc.DBTX, p Platform, account uuid.UUID) (had bool, err error) {
	err = q.onPlatform(ctx, db, p, func(db sqlc.DBTX) error {
		var err error
		had, err = sqlc.New(db).SaasHadTrial(ctx, sqlc.SaasHadTrialParams{TenantID: p.TenantID, AccountID: account.String()})
		return err
	})
	return had, convert.Err(err, "subscription")
}

// AccountsToEnsure lists up to batch accounts that are not customers of the platform tenant yet, or whose customer has no subscription in force, oldest first. With
// the platform tenant in the pooled database it is one statement over both sides; with the tenant in a cell the accounts (pooled) and the customers (the tenant's
// schema) are in two databases, so the accounts are read a page at a time and the tenant is asked which of them already have a subscription.
func (q *Queries) AccountsToEnsure(ctx context.Context, p Platform, batch int32) ([]uuid.UUID, error) {
	in, err := q.places.InCell(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	sq := sqlc.New(q.db)
	if !in {
		return sq.SaasAccountsToEnsure(ctx, sqlc.SaasAccountsToEnsureParams{TenantID: p.TenantID, Batch: batch})
	}
	var out []uuid.UUID
	afterAt, afterID := time.Unix(0, 0), uuid.Nil
	for int32(len(out)) < batch { //nolint:gosec // a small batch
		page, err := sq.SaasAccountsPage(ctx, sqlc.SaasAccountsPageParams{AfterAt: afterAt, AfterID: afterID, PageSize: accountsPage})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		ids := make([]string, len(page))
		for i, a := range page {
			ids[i] = a.ID.String()
		}
		var have []string
		if err := q.places.OnTenant(ctx, p.TenantID, q.db, func(tq *sqlc.Queries) (err error) {
			have, err = tq.SaasCustomersInForce(ctx, sqlc.SaasCustomersInForceParams{TenantID: p.TenantID, ExternalIds: ids})
			return err
		}); err != nil {
			return nil, err
		}
		set := make(map[string]bool, len(have))
		for _, h := range have {
			set[h] = true
		}
		for _, a := range page {
			if !set[a.ID.String()] && int32(len(out)) < batch { //nolint:gosec // a small batch
				out = append(out, a.ID)
			}
		}
		last := page[len(page)-1]
		afterAt, afterID = last.CreatedAt, last.ID
		if len(page) < accountsPage {
			break
		}
	}
	return out, nil
}

// accountsPage is how many accounts AccountsToEnsure reads at a time when it has to ask the platform tenant about them.
const accountsPage = 200

// View is the plan page of an account: the plan, the quotas against the usage of the database-counted resources, the plans on offer. The events used and
// the permission to change the plan are filled by the caller (limits-svc counts events; the handler knows the role).
func (q *Queries) View(ctx context.Context, account uuid.UUID) (*plansv1.OrganizationPlan, error) {
	p, err := q.Platform(ctx)
	if err != nil {
		return nil, err
	}
	st, err := q.Standing(ctx, q.db, p, account)
	if err != nil {
		return nil, err
	}
	usage, err := q.Usage(ctx, q.db, account)
	if err != nil {
		return nil, err
	}
	had, err := q.HadTrial(ctx, q.db, p, account)
	if err != nil {
		return nil, err
	}
	offers, err := q.Offers(ctx, q.db, st, had)
	if err != nil {
		return nil, err
	}
	out := &plansv1.OrganizationPlan{Enabled: true, AccountId: account.String(), State: st.State(q.now()), Offers: offers, Platform: p.Proto(), Overridden: len(st.Own) > 0}
	for _, o := range offers {
		if o.GetCurrent() {
			out.Plan = o
		}
	}
	// The plan on offer is the latest version; the one the organisation is on may be an older one.
	cur := offerOf(st.Plan, st.PlanLimits, p.Currency, 0, true)
	if out.Plan == nil || out.Plan.GetVersion() != cur.GetVersion() {
		out.Plan = cur
	}
	if st.Sub != nil {
		out.CustomerId, out.SubscriptionId = st.Sub.CustomerID.String(), st.Sub.ID.String()
		if st.Sub.TrialEnds != nil && st.Sub.State == "trialing" {
			out.TrialEndTime = timestamppb.New(*st.Sub.TrialEnds)
		}
	}
	for _, r := range model.Resources {
		l := st.Limits.Of(r)
		used := usage.Of(r)
		out.Quotas = append(out.Quotas, &plansv1.PlanQuota{Resource: string(r), Used: used, Limit: l.Units, Unlimited: l.Unlimited(), Mode: string(l.Mode),
			OverLimit: l.Over(used), Overridden: st.Own[r]})
	}
	return out, nil
}

// Row is an organisation of the operator's list.
type Row struct {
	AccountID  uuid.UUID
	Name       string
	OwnerEmail string
	CreatedAt  time.Time
	Apps       int64
	Members    int64
	Keys       int64
	Standing   Standing
}

// List pages through the organisations in the order of their ids, with their standing. after is the last id of the previous page (uuid.Nil: the start).
func (q *Queries) List(ctx context.Context, after uuid.UUID, query string, pageSize int) ([]Row, error) {
	p, err := q.Platform(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(q.db).SaasListOrganizations(ctx, sqlc.SaasListOrganizationsParams{After: after, Query: query, PageSize: int32(pageSize)}) //nolint:gosec // 1..200
	if err != nil {
		return nil, convert.Err(err, "organisation")
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		st, err := q.Standing(ctx, q.db, p, r.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, Row{AccountID: r.ID, Name: r.Name, OwnerEmail: r.OwnerEmail, CreatedAt: r.CreatedAt, Apps: r.Apps, Members: r.Members, Keys: r.ApiKeys, Standing: st})
	}
	return out, nil
}

// RowProto renders a row; the events used are filled by the gateway.
func (r Row) Proto(now time.Time) *plansv1.OrganizationRow {
	out := &plansv1.OrganizationRow{AccountId: r.AccountID.String(), Name: r.Name, OwnerEmail: r.OwnerEmail, PlanCode: r.Standing.Plan.Code, PlanName: r.Standing.Plan.Name,
		State: r.Standing.State(now), Overridden: len(r.Standing.Own) > 0, Apps: r.Apps, Members: r.Members, ApiKeys: r.Keys, EventsLimit: r.Standing.Limits.Of(model.Events).Units,
		CreateTime: timestamppb.New(r.CreatedAt)}
	if r.Standing.Sub != nil {
		out.CustomerId = r.Standing.Sub.CustomerID.String()
	}
	return out
}

// Itoa is the decimal text of a limit, for the entitlement requests.
func Itoa(n int64) string { return strconv.FormatInt(n, 10) }
