// Package query reads usage aggregates and the consumer's committed offsets under RLS. It never writes.
package query

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/store"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/store/sqlc"
)

// MaxBatchKeys is the size limit of the batch read.
const MaxBatchKeys = 1000

// Queries reads usage on the app pool (RLS enforced).
type Queries struct {
	app *pgxpool.Pool
	// group and topic key the consumer_offsets rows of the live consumer.
	group, topic string
}

// New returns the usage queries.
func New(app *pgxpool.Pool, group, topic string) *Queries {
	return &Queries{app: app, group: group, topic: topic}
}

func invalid(key, msg string) error { return errkey.New(codes.InvalidArgument, key, msg) }

func validPeriod(p string) error {
	if !period.Valid(p) {
		return invalid("aggregator.period.bad_id", "period_id must look like 2026-11")
	}
	return nil
}

// generation returns the pinned generation, or the tenant's active one.
func generation(ctx context.Context, q *sqlc.Queries, pinned int32) (int32, error) {
	if pinned > 0 {
		return pinned, nil
	}
	return q.ActiveGeneration(ctx)
}

// readGeneration runs read in a tenant transaction with the generation to read: the pinned one, or the tenant's active one.
func (s *Queries) readGeneration(ctx context.Context, pinned int32, read func(q *sqlc.Queries, gen int32) error) error {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		gen, err := generation(ctx, q, pinned)
		if err != nil {
			return err
		}
		return read(q, gen)
	})
	if err != nil {
		return store.MapErr(ctx, err)
	}
	return nil
}

func usageRow(customer uuid.UUID, meter, period, qty string, scale int32, events int64) *aggregatorv1.UsageAggregate {
	return &aggregatorv1.UsageAggregate{
		CustomerId: customer.String(), MeterCode: meter, PeriodId: period,
		Quantity: formatScaled(qty, int(scale)), QuantityScale: uint32(scale), EventCount: events, //nolint:gosec // 0..6
	}
}

// CustomerUsage is the usage of one customer in a period, one row per meter.
func (s *Queries) CustomerUsage(ctx context.Context, customerID, periodID string, pinned int32) ([]*aggregatorv1.UsageAggregate, error) {
	if _, err := tenant.Require(ctx); err != nil {
		return nil, err
	}
	customer, err := errkey.ParseUUID("aggregator.customer.bad_id", "customer_id", customerID)
	if err != nil {
		return nil, err
	}
	if err := validPeriod(periodID); err != nil {
		return nil, err
	}
	var out []*aggregatorv1.UsageAggregate
	err = s.readGeneration(ctx, pinned, func(q *sqlc.Queries, gen int32) error {
		rows, err := q.CustomerUsage(ctx, sqlc.CustomerUsageParams{CustomerID: customer, PeriodID: periodID, Generation: gen})
		if err != nil {
			return err
		}
		for _, r := range rows {
			u := usageRow(r.CustomerID, r.MeterCode, r.PeriodID, r.Quantity, r.QuantityScale, r.EventCount)
			u.Generation = gen
			out = append(out, u)
		}
		return nil
	})
	return out, err
}

// Batch is the usage for a list of (customer, meter, period) keys and the consumer offsets they are current as of.
type Batch struct {
	Usage           []*aggregatorv1.UsageAggregate
	ConsumerOffsets map[int32]int64
}

// UsageBatch reads the usage of up to MaxBatchKeys keys, in the order given, in one snapshot with the offsets: "usage as of these offsets".
func (s *Queries) UsageBatch(ctx context.Context, keys []*aggregatorv1.UsageKey, pinned int32) (*Batch, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	n := len(keys)
	if n == 0 || n > MaxBatchKeys {
		return nil, invalid("aggregator.usage.bad_batch_size", fmt.Sprintf("keys: need 1..%d, got %d", MaxBatchKeys, n))
	}
	customers := make([]uuid.UUID, n)
	meterCodes := make([]string, n)
	periods := make([]string, n)
	for i, k := range keys {
		c, err := errkey.ParseUUID("aggregator.customer.bad_id", fmt.Sprintf("keys[%d].customer_id", i), k.GetCustomerId())
		if err != nil {
			return nil, err
		}
		if err := validPeriod(k.GetPeriodId()); err != nil {
			return nil, err
		}
		customers[i], meterCodes[i], periods[i] = c, k.GetMeterCode(), k.GetPeriodId()
	}
	out := &Batch{ConsumerOffsets: map[int32]int64{}}
	err = pgxx.InTx(ctx, s.app, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := q.SetAppTenant(ctx, ti.ID.String()); err != nil {
			return err
		}
		gen, err := generation(ctx, q, pinned)
		if err != nil {
			return err
		}
		rows, err := q.UsageBatch(ctx, sqlc.UsageBatchParams{Customers: customers, MeterCodes: meterCodes, Periods: periods, Generation: gen})
		if err != nil {
			return err
		}
		for i, r := range rows {
			agg := &aggregatorv1.UsageAggregate{CustomerId: customers[i].String(), MeterCode: meterCodes[i], PeriodId: periods[i], Quantity: "0"}
			if r.Found {
				agg = usageRow(customers[i], meterCodes[i], periods[i], r.Quantity, r.QuantityScale, r.EventCount)
			}
			out.Usage = append(out.Usage, agg)
		}
		offs, err := q.ListOffsets(ctx, sqlc.ListOffsetsParams{ConsumerGroup: s.group, Topic: s.topic})
		if err != nil {
			return err
		}
		out.ConsumerOffsets = offsetMap(offs)
		return nil
	})
	if err != nil {
		return nil, store.MapErr(ctx, err)
	}
	return out, nil
}

// PeriodUsage is the usage of every customer and meter in a period.
func (s *Queries) PeriodUsage(ctx context.Context, periodID string, pinned int32) ([]*aggregatorv1.UsageAggregate, error) {
	if _, err := tenant.Require(ctx); err != nil {
		return nil, err
	}
	if err := validPeriod(periodID); err != nil {
		return nil, err
	}
	var out []*aggregatorv1.UsageAggregate
	err := s.readGeneration(ctx, pinned, func(q *sqlc.Queries, gen int32) error {
		rows, err := q.PeriodUsage(ctx, sqlc.PeriodUsageParams{PeriodID: periodID, Generation: gen})
		if err != nil {
			return err
		}
		for _, r := range rows {
			u := usageRow(r.CustomerID, r.MeterCode, r.PeriodID, r.Quantity, r.QuantityScale, r.EventCount)
			u.Generation = gen
			out = append(out, u)
		}
		return nil
	})
	return out, err
}

// LateUsage is the usage that arrived late for earlier periods and is billed in billedPeriodID.
func (s *Queries) LateUsage(ctx context.Context, billedPeriodID string, pinned int32) ([]*aggregatorv1.LateUsage, error) {
	if _, err := tenant.Require(ctx); err != nil {
		return nil, err
	}
	if err := validPeriod(billedPeriodID); err != nil {
		return nil, err
	}
	var out []*aggregatorv1.LateUsage
	err := s.readGeneration(ctx, pinned, func(q *sqlc.Queries, gen int32) error {
		rows, err := q.LateUsageBilledIn(ctx, sqlc.LateUsageBilledInParams{BilledPeriodID: billedPeriodID, Generation: gen})
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, &aggregatorv1.LateUsage{
				CustomerId: r.CustomerID.String(), MeterCode: r.MeterCode, OriginPeriodId: r.OriginPeriodID, BilledPeriodId: r.BilledPeriodID,
				Quantity: formatScaled(r.Quantity, int(r.QuantityScale)), QuantityScale: uint32(r.QuantityScale), EventCount: r.EventCount, //nolint:gosec // 0..6
				Generation: gen,
			})
		}
		return nil
	})
	return out, err
}

// Watermark is the offsets the live consumer has committed, and whether they reach every required offset.
func (s *Queries) Watermark(ctx context.Context, required map[int32]int64) (committed map[int32]int64, caughtUp bool, err error) {
	offs, err := sqlc.New(s.app).ListOffsets(ctx, sqlc.ListOffsetsParams{ConsumerGroup: s.group, Topic: s.topic})
	if err != nil {
		return nil, false, store.MapErr(ctx, err)
	}
	committed = offsetMap(offs)
	caughtUp = true
	for p, need := range required {
		if need > 0 && committed[p] < need {
			caughtUp = false
		}
	}
	return committed, caughtUp, nil
}

func offsetMap(rows []sqlc.ListOffsetsRow) map[int32]int64 {
	m := make(map[int32]int64, len(rows))
	for _, o := range rows {
		m[o.Partition] = o.NextOffset
	}
	return m
}
