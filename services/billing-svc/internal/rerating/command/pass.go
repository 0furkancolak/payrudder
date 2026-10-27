package command

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// reratePass is what one pass over a job reads before it prices any customer.
type reratePass struct {
	job        *model.Job
	start, end time.Time // the period
	tctx       context.Context
	currency   string
	meters     map[string]inputs.MeterInfo
	taxes      inputs.TaxTable
	subs       map[string]inputs.Subscription
	target     rating.PlanVersion
	planCode   string
	usage      map[string][]rating.Usage
	billed     map[string]map[string]int64
	snaps      map[string]json.RawMessage
	codes      map[string]string // plan version id -> plan code
}

func (c *Commands) rerateOnce(ctx context.Context, job *model.Job) error {
	rp, err := c.loadRerating(ctx, job)
	if err != nil {
		return err
	}
	rated := 0
	for _, cid := range rp.customers() {
		priced, err := c.rerateCustomer(ctx, rp, cid)
		if err != nil {
			return err
		}
		if priced {
			rated++
		}
	}
	return c.finish(ctx, job, rated)
}

// loadRerating reads the tenant, the period, the subscriptions, the target plan version, the period's usage and what was billed for it.
func (c *Commands) loadRerating(ctx context.Context, job *model.Job) (*reratePass, error) {
	tid := job.TenantID
	rp := &reratePass{job: job, tctx: inputs.TenantCtx(ctx, tid), codes: map[string]string{}}
	t, err := c.cfg.Inputs.Tenant(ctx, tid)
	if err != nil {
		return nil, err
	}
	rp.currency = t.GetCurrency()
	loc, err := time.LoadLocation(t.GetTimezone())
	if err != nil {
		return nil, inputs.Permanent(err)
	}
	start, end, err := period.Bounds(job.PeriodID, loc)
	if err != nil {
		return nil, inputs.Permanent(err)
	}
	rp.start, rp.end = start, end
	if rp.meters, err = c.cfg.Inputs.Meters(rp.tctx); err != nil {
		return nil, err
	}
	if rp.taxes, err = c.cfg.Inputs.TaxRates(ctx, tid); err != nil {
		return nil, err
	}
	if rp.subs, err = c.cfg.Inputs.Subscriptions(ctx, tid, start, end); err != nil {
		return nil, err
	}
	if err := c.loadTarget(rp); err != nil {
		return nil, err
	}
	if err := c.loadUsage(ctx, rp); err != nil {
		return nil, err
	}
	if rp.billed, rp.snaps, err = c.billedComponents(ctx, tid, job.PeriodID); err != nil {
		return nil, err
	}
	return rp, nil
}

// loadTarget reads the plan version the job prices under and the plan it belongs to.
func (c *Commands) loadTarget(rp *reratePass) error {
	resp, err := c.cfg.Inputs.Plans.GetPlanVersion(rp.tctx, &plansv1.GetPlanVersionRequest{PlanVersionId: rp.job.PlanVersionID.String()})
	if err != nil {
		return fmt.Errorf("get plan version: %w", err)
	}
	if rp.target, err = inputs.ConvertPlanVersion(resp.GetPlanVersion(), "", ""); err != nil {
		return inputs.Permanent(err)
	}
	rp.planCode = resp.GetPlanVersion().GetPlanCode()
	return nil
}

// loadUsage reads the usage of the period at the tenant's active generation (a replay may have corrected it since the run).
func (c *Commands) loadUsage(ctx context.Context, rp *reratePass) error {
	job := rp.job
	usage, gen, err := c.cfg.Inputs.PeriodUsage(rp.tctx, job.PeriodID, 0, rp.meters)
	if err != nil {
		return err
	}
	rp.usage = usage
	if gen != 0 && gen != job.Generation {
		if err := c.cfg.DB.Q(job.TenantID).SetReratingJobGeneration(ctx, sqlc.SetReratingJobGenerationParams{Generation: gen, TenantID: job.TenantID, ID: job.ID}); err != nil {
			return err
		}
		job.Generation = gen
	}
	return nil
}

// customers are the subscribed customers, in a stable order.
func (rp *reratePass) customers() []string {
	ids := make([]string, 0, len(rp.subs))
	for c := range rp.subs {
		ids = append(ids, c)
	}
	sort.Strings(ids)
	return ids
}

// planCodeOf is the plan code of a plan version.
func (c *Commands) planCodeOf(rp *reratePass, pvID string) (string, error) {
	if code, ok := rp.codes[pvID]; ok {
		return code, nil
	}
	resp, err := c.cfg.Inputs.Plans.GetPlanVersion(rp.tctx, &plansv1.GetPlanVersionRequest{PlanVersionId: pvID})
	if err != nil {
		return "", fmt.Errorf("get plan version %s: %w", pvID, err)
	}
	rp.codes[pvID] = resp.GetPlanVersion().GetPlanCode()
	return rp.codes[pvID], nil
}

// rerateCustomer prices one customer under the target and records the difference; it reports whether the customer was priced.
func (c *Commands) rerateCustomer(ctx context.Context, rp *reratePass, cid string) (bool, error) {
	job, sub := rp.job, rp.subs[cid]
	snap, invoiced := rp.snaps[cid]
	if !invoiced {
		return false, nil // never billed for the period
	}
	// A subscription already on the target version is still priced: what was billed may differ from it because an earlier job corrected the period to another
	// version, and running the job again for the original version is how that is undone. When nothing differs there is nothing to record.
	onTarget := sub.PlanVersionID == job.PlanVersionID.String()
	if (sub.Currency != "" && sub.Currency != rp.currency) || len(sub.Chain) > 1 {
		// Re-rating prices in the tenant currency and compares whole-period fees; a subscription sold in another
		// currency or changed inside the period would be compared against the wrong amounts.
		c.cfg.Log.WarnContext(ctx, "re-rating skips a subscription in another currency or changed inside the period", "tenant", job.TenantID, "customer", cid, "period", job.PeriodID)
		return false, nil
	}
	code, err := c.planCodeOf(rp, sub.PlanVersionID)
	if err != nil {
		return false, err
	}
	if code != rp.planCode {
		return false, nil // another plan: not what this job re-prices
	}
	deltas, total, err := correctionDeltas(rp.target, rp.usage[cid], rp.billed[cid], rp.meters, inputs.FeeWaived(sub, rp.start, rp.end))
	if err != nil {
		return true, err
	}
	if total == 0 {
		return !onTarget, nil
	}
	cuid, err := uuid.Parse(cid)
	if err != nil {
		return true, inputs.Permanent(err)
	}
	name, bps, err := c.taxRate(ctx, rp, sub, snap)
	if err != nil {
		return true, err
	}
	return true, c.record(ctx, job, cuid, snap, rp.currency, deltas, total, rp.meters, taxRate{name, bps})
}

// taxRate is the rate the customer's invoices carry: the tenant's rate for the country of the customer the regular invoice is out to. A correction is taxed like
// the invoice it corrects, so what a provider charges for (and taxes itself) has none.
func (c *Commands) taxRate(ctx context.Context, rp *reratePass, sub inputs.Subscription, snap json.RawMessage) (string, int32, error) {
	if sub.ProviderOwned() {
		return "", 0, nil
	}
	var s struct {
		External string `json:"customer_external_id"`
	}
	if err := json.Unmarshal(snap, &s); err != nil {
		return "", 0, inputs.Permanent(fmt.Errorf("invoice snapshot: %w", err))
	}
	cu, err := c.cfg.Inputs.Plans.GetCustomer(rp.tctx, &plansv1.GetCustomerRequest{ExternalId: s.External})
	if err != nil {
		return "", 0, fmt.Errorf("get customer %s: %w", s.External, err)
	}
	name, bps, _ := rp.taxes.RateFor(cu.GetCustomer().GetCountry())
	return name, bps, nil
}
