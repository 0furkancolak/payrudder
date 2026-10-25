package command

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/cockroachdb/apd/v3"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// lateKey identifies the late usage of one meter of one customer that belongs to an earlier period.
type lateKey struct{ Customer, Meter, Origin string }

// streamUsage reads the on-time usage of the period at the generation the run pins, pinning the tenant's active one when the run has none yet.
func (m *Manager) streamUsage(ctx context.Context, rr *rateRun) (int32, error) {
	run := rr.run
	pinned := int32(0)
	if run.Generation != nil {
		pinned = *run.Generation
	}
	usage, gen, err := m.in.PeriodUsage(rr.tctx, run.PeriodID, pinned, rr.meters)
	if err != nil {
		return 0, err
	}
	rr.usage = usage
	if gen == 0 {
		gen = 1 // no usage rows: the tenant has only ever had generation 1
	}
	if run.Generation == nil {
		if err := m.q.SetRunGeneration(ctx, sqlc.SetRunGenerationParams{Generation: gen, TenantID: run.TenantID, PeriodID: run.PeriodID}); err != nil {
			return 0, err
		}
		run.Generation = &gen
	}
	return gen, nil
}

// streamLateUsage reads the late usage billed in this period.
func (m *Manager) streamLateUsage(rr *rateRun, gen int32) (map[lateKey]*apd.Decimal, error) {
	late := map[lateKey]*apd.Decimal{}
	ls, err := m.in.Aggregator.StreamLateUsage(rr.tctx, &aggregatorv1.StreamLateUsageRequest{BilledPeriodId: rr.run.PeriodID, Generation: gen})
	if err != nil {
		return nil, fmt.Errorf("stream late usage: %w", err)
	}
	for {
		r, err := ls.Recv()
		if errors.Is(err, io.EOF) {
			return late, nil
		}
		if err != nil {
			return nil, fmt.Errorf("stream late usage: %w", err)
		}
		l := r.GetLateUsage()
		q, err := inputs.Decimal(l.GetQuantity())
		if err != nil {
			return nil, inputs.Permanent(err)
		}
		late[lateKey{l.GetCustomerId(), l.GetMeterCode(), l.GetOriginPeriodId()}] = q
	}
}
