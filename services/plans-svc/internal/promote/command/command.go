// Package command promotes an app's sandbox configuration to its live environment (phase 45 D).
//
// It reads both environments through the modules that own the data (meters, plans, tax, coupons, mail settings, webhook endpoints), each as the tenant it is
// about, decides with the pure rules of promote/query, and writes only through those modules' commands, as the live tenant. So every rule a module has about its own
// objects (validation, quotas, the go-live gate on webhook endpoints) applies to a promoted object as to a typed one, and row-level security keeps each side to
// its own tenant. Customers, usage, invoices and payments are not configuration and are not touched.
package command

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	couponscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/command"
	couponsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/query"
	golive "github.com/0furkancolak/payrudder/services/plans-svc/internal/golive/query"
	idquery "github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	mailcmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/mailsettings/command"
	mailqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/mailsettings/query"
	meterscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/meters/command"
	metersqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/meters/query"
	planscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/command"
	plansqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/promote/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	taxcmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/tax/command"
	taxqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tax/query"
	hookscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhookendpoints/command"
	hooksqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhookendpoints/query"
)

// Modules are the exported commands and queries of the modules whose configuration is promoted.
type Modules struct {
	MetersQ  *metersqry.Queries
	MetersC  *meterscmd.Commands
	PlansQ   *plansqry.Queries
	PlansC   *planscmd.Commands
	TaxQ     *taxqry.Queries
	TaxC     *taxcmd.Commands
	CouponsQ *couponsqry.Queries
	CouponsC *couponscmd.Commands
	MailQ    *mailqry.Queries
	MailC    *mailcmd.Commands
	HooksQ   *hooksqry.Queries
	HooksC   *hookscmd.Commands
}

// Commands promotes configuration.
type Commands struct {
	pool *pgxpool.Pool // plans_system: membership, tenants, the audit row
	m    Modules
	log  *slog.Logger
}

// New returns the promotion commands.
func New(systemPool *pgxpool.Pool, m Modules, log *slog.Logger) *Commands {
	return &Commands{pool: systemPool, m: m, log: log}
}

// step is a create waiting to be applied.
type step struct {
	change *plansv1.PromoteChange
	apply  func(ctx context.Context) (secret string, err error)
}

// Promote computes the diff of the app's sandbox against its live environment and, unless dry_run, applies the creates in it. The caller must be an owner or an
// admin of the account. Applying is idempotent (run again, everything made is "unchanged") and not atomic: a failure leaves what was made, shows the error on
// its change, and running it again completes the rest.
func (c *Commands) Promote(ctx context.Context, req *plansv1.PromoteMyAppRequest) (*plansv1.PromoteMyAppResponse, error) {
	db := sqlc.New(c.pool)
	app, role, err := idquery.AppOf(ctx, db, req.GetUserId(), req.GetAppId())
	if err != nil {
		return nil, err
	}
	if !idquery.CanManage(role) {
		return nil, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "your role in this account does not allow this")
	}
	live, err := golive.LiveTenant(app)
	if err != nil {
		return nil, err
	}
	var sandbox *plansv1.Tenant
	for _, t := range app.GetTenants() {
		if t.GetEnvironment() == env.Sandbox.Proto() {
			sandbox = t
		}
	}
	if sandbox == nil {
		return nil, idquery.NotFound("tenant")
	}
	sctx, err := asTenant(ctx, sandbox)
	if err != nil {
		return nil, err
	}
	lctx, err := asTenant(ctx, live)
	if err != nil {
		return nil, err
	}
	liveActive := live.GetStatus() == plansv1.TenantStatus_TENANT_STATUS_ACTIVE
	if live.GetStatus() == plansv1.TenantStatus_TENANT_STATUS_SUSPENDED {
		return nil, errkey.New(codes.FailedPrecondition, "identity.go_live.suspended", "this environment is suspended: contact support")
	}

	all, err := c.plan(sctx, lctx, liveActive)
	if err != nil {
		return nil, err
	}
	changes := make([]*plansv1.PromoteChange, 0, len(all))
	for _, s := range all {
		changes = append(changes, s.change)
	}
	resp := &plansv1.PromoteMyAppResponse{DryRun: req.GetDryRun(), PlanHash: hashOf(changes), Changes: changes}
	if !req.GetDryRun() {
		if h := req.GetPlanHash(); h != "" && h != resp.GetPlanHash() {
			return nil, errkey.New(codes.FailedPrecondition, "identity.promote.stale", "the configuration changed since the preview: preview again")
		}
		for _, s := range all {
			if s.apply == nil {
				continue
			}
			secret, err := s.apply(lctx)
			switch {
			case err == nil:
				s.change.Applied, s.change.Secret = true, secret
			case status.Code(err) == codes.AlreadyExists:
				// Someone made it between the preview and now (or a retry): it is there, which is what was wanted.
				s.change.Applied, s.change.Detail = true, "already there"
			default:
				s.change.Error = errText(err)
			}
		}
	}
	for _, ch := range changes {
		switch {
		case ch.GetAction() == plansv1.PromoteChange_ACTION_CREATE && ch.GetError() != "":
			resp.Failed++
		case ch.GetAction() == plansv1.PromoteChange_ACTION_CREATE && ch.GetApplied():
			resp.Created++
		case ch.GetAction() == plansv1.PromoteChange_ACTION_CREATE:
			resp.Created++ // a dry run counts what it would make
		case ch.GetAction() == plansv1.PromoteChange_ACTION_UNCHANGED:
			resp.Unchanged++
		case ch.GetAction() == plansv1.PromoteChange_ACTION_CONFLICT:
			resp.Conflicts++
		case ch.GetAction() == plansv1.PromoteChange_ACTION_SKIPPED:
			resp.Skipped++
		case ch.GetAction() == plansv1.PromoteChange_ACTION_UNSPECIFIED:
		}
	}
	if !req.GetDryRun() {
		if err := c.audit(ctx, db, app, live, sandbox, req.GetUserId(), resp); err != nil {
			// The configuration is in place; the trail of it is not. Say so loudly, do not undo it.
			c.log.ErrorContext(ctx, "promotion applied but not audited", "app_id", app.GetId(), "user_id", req.GetUserId(), "err", err)
		}
	}
	return resp, nil
}

func asTenant(ctx context.Context, t *plansv1.Tenant) (context.Context, error) {
	id, err := uuid.Parse(t.GetId())
	if err != nil {
		return nil, idquery.NotFound("tenant")
	}
	e, err := env.FromProto(t.GetEnvironment())
	if err != nil {
		return nil, env.Refuse(err)
	}
	return tenant.NewContext(ctx, tenant.Info{ID: id, Environment: e.String(), Timezone: t.GetTimezone()}), nil
}

func errText(err error) string {
	if s, ok := status.FromError(err); ok {
		return s.Message()
	}
	return err.Error()
}

// plan reads both sides and decides. The steps are in the order the families must be made in; a step with an apply is a create.
func (c *Commands) plan(sctx, lctx context.Context, liveActive bool) ([]*step, error) {
	var all []*step
	add := func(family string, ds []query.Decision, apply func(d query.Decision) func(context.Context) (string, error)) {
		for _, d := range ds {
			ch := &plansv1.PromoteChange{Family: family, Key: d.Key, Action: d.Action, Detail: d.Detail}
			st := &step{change: ch}
			if d.Action == plansv1.PromoteChange_ACTION_CREATE {
				st.apply = apply(d)
			}
			all = append(all, st)
		}
	}

	// Meters.
	sm, err := c.m.MetersQ.List(sctx)
	if err != nil {
		return nil, err
	}
	lm, err := c.m.MetersQ.List(lctx)
	if err != nil {
		return nil, err
	}
	add(query.FamilyMeters, query.Meters(sm, lm), func(d query.Decision) func(context.Context) (string, error) {
		m := d.Item.(*plansv1.Meter)
		return func(ctx context.Context) (string, error) {
			_, err := c.m.MetersC.Create(ctx, &plansv1.CreateMeterRequest{Code: m.GetCode(), Aggregation: m.GetAggregation(), ValueProperty: m.GetValueProperty(),
				Filter: m.GetFilter(), QuantityScale: m.GetQuantityScale(), UnitLabel: m.GetUnitLabel()})
			return "", err
		}
	})

	// Plans, with their latest published version.
	sp, err := c.planSets(sctx)
	if err != nil {
		return nil, err
	}
	lp, err := c.planSets(lctx)
	if err != nil {
		return nil, err
	}
	livePlans := map[string]bool{}
	for _, p := range lp {
		livePlans[p.Plan.GetCode()] = true
	}
	add(query.FamilyPlans, query.Plans(sp, lp), func(d query.Decision) func(context.Context) (string, error) {
		p := d.Item.(query.PlanSet)
		return func(ctx context.Context) (string, error) {
			if !livePlans[p.Plan.GetCode()] {
				if _, err := c.m.PlansC.Create(ctx, &plansv1.CreatePlanRequest{Code: p.Plan.GetCode(), Name: p.Plan.GetName()}); err != nil && status.Code(err) != codes.AlreadyExists {
					return "", err
				}
			}
			_, err := c.m.PlansC.PublishVersion(ctx, query.PublishRequest(p.Plan.GetCode(), p.Version))
			return "", err
		}
	})

	// Taxes.
	st, err := c.m.TaxQ.Rates(sctx)
	if err != nil {
		return nil, err
	}
	lt, err := c.m.TaxQ.Rates(lctx)
	if err != nil {
		return nil, err
	}
	var newRates []*plansv1.TaxRate
	taxDecisions := query.Taxes(st, lt)
	for _, d := range taxDecisions {
		if d.Action == plansv1.PromoteChange_ACTION_CREATE {
			newRates = append(newRates, d.Item.(*plansv1.TaxRate))
		}
	}
	taxApplied := false
	add(query.FamilyTaxes, taxDecisions, func(query.Decision) func(context.Context) (string, error) {
		// SetRates replaces the list, so the first create writes live's rates plus every new one, and the others have nothing left to do.
		return func(ctx context.Context) (string, error) {
			if taxApplied {
				return "", nil
			}
			taxApplied = true
			_, err := c.m.TaxC.SetRates(ctx, append(append([]*plansv1.TaxRate(nil), lt...), newRates...))
			return "", err
		}
	})

	// Coupons.
	sc, err := c.m.CouponsQ.List(sctx)
	if err != nil {
		return nil, err
	}
	lc, err := c.m.CouponsQ.List(lctx)
	if err != nil {
		return nil, err
	}
	add(query.FamilyCoupons, query.Coupons(sc, lc), func(d query.Decision) func(context.Context) (string, error) {
		cp := d.Item.(*plansv1.Coupon)
		return func(ctx context.Context) (string, error) {
			_, err := c.m.CouponsC.Create(ctx, &plansv1.Coupon{Code: cp.GetCode(), Name: cp.GetName(), PercentBps: cp.GetPercentBps(), AmountMinor: cp.GetAmountMinor(),
				Currency: cp.GetCurrency(), Duration: cp.GetDuration(), DurationPeriods: cp.GetDurationPeriods(), MaxRedemptions: cp.GetMaxRedemptions(),
				ExpireTime: cp.GetExpireTime(), BonusPercentBps: cp.GetBonusPercentBps(), AppliesTo: cp.GetAppliesTo()})
			return "", err
		}
	})

	// Mail settings.
	sMail, err := c.m.MailQ.Get(sctx)
	if err != nil {
		return nil, err
	}
	lMail, err := c.m.MailQ.Get(lctx)
	if err != nil {
		return nil, err
	}
	add(query.FamilyMail, query.Mail(sMail, lMail), func(d query.Decision) func(context.Context) (string, error) {
		settings := d.Item.([]*plansv1.MailCategorySetting)
		return func(ctx context.Context) (string, error) {
			_, err := c.m.MailC.Set(ctx, settings)
			return "", err
		}
	})

	// Webhook endpoints.
	sh, err := c.m.HooksQ.Endpoints(sctx)
	if err != nil {
		return nil, err
	}
	lh, err := c.m.HooksQ.Endpoints(lctx)
	if err != nil {
		return nil, err
	}
	add(query.FamilyWebhooks, query.Webhooks(sh, lh, liveActive), func(d query.Decision) func(context.Context) (string, error) {
		e := d.Item.(*plansv1.WebhookEndpoint)
		return func(ctx context.Context) (string, error) {
			made, err := c.m.HooksC.CreateEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: e.GetUrl(), Description: e.GetDescription(), EventTypes: e.GetEventTypes()})
			return made.GetSecret(), err
		}
	})

	return all, nil
}

// planSets lists the plans of the tenant of ctx with their latest published version.
func (c *Commands) planSets(ctx context.Context) ([]query.PlanSet, error) {
	plans, err := c.m.PlansQ.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]query.PlanSet, 0, len(plans))
	for _, p := range plans {
		set := query.PlanSet{Plan: p}
		if p.GetLatestVersion() > 0 {
			v, err := c.m.PlansQ.Version(ctx, &plansv1.GetPlanVersionRequest{PlanCode: p.GetCode(), Version: p.GetLatestVersion()})
			if err != nil {
				return nil, err
			}
			set.Version = v
		}
		out = append(out, set)
	}
	return out, nil
}

// hashOf identifies the diff a person looked at: the creates, conflicts and skips with their keys, in a fixed order. Unchanged objects are left out, so
// the hash does not move for something that does not matter.
func hashOf(changes []*plansv1.PromoteChange) string {
	var lines []string
	for _, ch := range changes {
		if ch.GetAction() != plansv1.PromoteChange_ACTION_UNCHANGED {
			lines = append(lines, fmt.Sprintf("%s|%s|%s|%s", ch.GetFamily(), ch.GetKey(), ch.GetAction(), ch.GetDetail()))
		}
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// audit writes the row of an applied promotion. The detail holds what each change was and came to, never a secret.
func (c *Commands) audit(ctx context.Context, db *sqlc.Queries, app *plansv1.App, live, sandbox *plansv1.Tenant, userID string, r *plansv1.PromoteMyAppResponse) error {
	type entry struct {
		Family, Key, Action, Detail, Error string
		Applied                            bool
	}
	var detail []entry
	for _, ch := range r.GetChanges() {
		if ch.GetAction() == plansv1.PromoteChange_ACTION_UNCHANGED {
			continue
		}
		detail = append(detail, entry{ch.GetFamily(), ch.GetKey(), ch.GetAction().String(), ch.GetDetail(), ch.GetError(), ch.GetApplied()})
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	appID, e1 := uuid.Parse(app.GetId())
	uid, e2 := uuid.Parse(userID)
	sid, e3 := uuid.Parse(sandbox.GetId())
	lid, e4 := uuid.Parse(live.GetId())
	if err := errors.Join(e1, e2, e3, e4); err != nil {
		return err
	}
	return db.InsertConfigPromotion(ctx, sqlc.InsertConfigPromotionParams{ID: uuid.New(), AppID: appID, UserID: uid, SandboxTenantID: sid, LiveTenantID: lid,
		Created: r.GetCreated(), Unchanged: r.GetUnchanged(), Conflicts: r.GetConflicts(), Skipped: r.GetSkipped(), Failed: r.GetFailed(), Detail: raw})
}
