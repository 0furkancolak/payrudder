// Package query reads usage aggregates and the consumer's committed offsets under RLS. It never writes.
package query

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
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

// Batch is the usage for a list of (customer, meter, window) keys and the consumer offsets they are current as of.
type Batch struct {
	Usage           []*aggregatorv1.UsageAggregate
	ConsumerOffsets map[int32]int64
}

// rangeKeys are the keys of a batch that read one stored granule (months or hours), as the arrays the range queries take, and where each
// came from in the request. modes is how the rows of a key combine ("sum", "max" or "last"); the distinct counts of UNIQUE meters read the value sets
// and take no mode.
type rangeKeys struct {
	at             []int
	customers      []uuid.UUID
	meterCodes     []string
	fromIDs, toIDs []string
	modes          []string
}

func (r *rangeKeys) add(i int, customer uuid.UUID, meter, from, to, mode string) {
	r.at, r.customers, r.meterCodes = append(r.at, i), append(r.customers, customer), append(r.meterCodes, meter)
	r.fromIDs, r.toIDs, r.modes = append(r.fromIDs, from), append(r.toIDs, to), append(r.modes, mode)
}

// modeOf is how the rows of a key's window combine: the key's aggregation, or MAX for the legacy `max` flag.
func modeOf(k *aggregatorv1.UsageKey) (mode string, unique bool) {
	switch {
	case k.GetAggregation() == plansv1.Aggregation_AGGREGATION_UNIQUE:
		return "sum", true
	case k.GetAggregation() == plansv1.Aggregation_AGGREGATION_LAST:
		return "last", false
	case k.GetAggregation() == plansv1.Aggregation_AGGREGATION_MAX || k.GetMax():
		return "max", false
	}
	return "sum", false
}

// rangeRow is one answer of a range query, whichever query it came from.
type rangeRow struct {
	found    bool
	quantity string
	scale    int32
	events   int64
	lastAt   string
	lastID   string
}

// UsageBatch reads the usage of up to MaxBatchKeys keys, in the order given, in one snapshot with the offsets: "usage as of these offsets".
// A key names a billing period or any other quota window (period.Span): the usage of a window is what its stored rows combine to (see modeOf), and for a UNIQUE
// meter the number of distinct values in its sets: a single month is the count kept in the month row, any wider window is counted from the sets.
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
	var months, hours, monthSets, hourSets rangeKeys
	for i, k := range keys {
		c, err := errkey.ParseUUID("aggregator.customer.bad_id", fmt.Sprintf("keys[%d].customer_id", i), k.GetCustomerId())
		if err != nil {
			return nil, err
		}
		g, from, to, err := period.Span(k.GetPeriodId())
		if err != nil {
			return nil, invalid("aggregator.period.bad_id", "period_id must be a period (2026-11) or a window id (2026-11-05T13, 2026-11-05, 2026-W45, 2026, all)")
		}
		customers[i] = c
		mode, unique := modeOf(k)
		switch {
		case unique && g == period.Months && from != to:
			monthSets.add(i, c, k.GetMeterCode(), from, to, mode)
		case unique && g == period.Hours:
			hourSets.add(i, c, k.GetMeterCode(), from, to, mode)
		case g == period.Months:
			months.add(i, c, k.GetMeterCode(), from, to, mode)
		default:
			hours.add(i, c, k.GetMeterCode(), from, to, mode)
		}
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
		out.Usage = make([]*aggregatorv1.UsageAggregate, n)
		fill := func(r rangeKeys, rows []rangeRow) {
			for j, i := range r.at {
				k := keys[i]
				agg := &aggregatorv1.UsageAggregate{CustomerId: customers[i].String(), MeterCode: k.GetMeterCode(), PeriodId: k.GetPeriodId(), Quantity: "0"}
				if rows[j].found {
					agg = usageRow(customers[i], k.GetMeterCode(), k.GetPeriodId(), rows[j].quantity, rows[j].scale, rows[j].events)
					if rows[j].lastID != "" {
						if at, err := time.Parse(time.RFC3339Nano, rows[j].lastAt); err == nil {
							agg.LastEventTime, agg.LastEventId = timestamppb.New(at), rows[j].lastID
						}
					}
				}
				out.Usage[i] = agg
			}
		}
		if len(months.at) > 0 {
			rows, err := q.UsageRangeMonths(ctx, sqlc.UsageRangeMonthsParams{Customers: months.customers, MeterCodes: months.meterCodes, FromIds: months.fromIDs, ToIds: months.toIDs, Modes: months.modes, Generation: gen})
			if err != nil {
				return err
			}
			res := make([]rangeRow, len(rows))
			for j, r := range rows {
				res[j] = rangeRow{r.Found, r.Quantity, r.QuantityScale, r.EventCount, r.LastAt, r.LastEventID}
			}
			fill(months, res)
		}
		if len(hours.at) > 0 {
			rows, err := q.UsageRangeHours(ctx, sqlc.UsageRangeHoursParams{Customers: hours.customers, MeterCodes: hours.meterCodes, FromIds: hours.fromIDs, ToIds: hours.toIDs, Modes: hours.modes, Generation: gen})
			if err != nil {
				return err
			}
			res := make([]rangeRow, len(rows))
			for j, r := range rows {
				res[j] = rangeRow{r.Found, r.Quantity, r.QuantityScale, r.EventCount, r.LastAt, r.LastEventID}
			}
			fill(hours, res)
		}
		if len(monthSets.at) > 0 {
			rows, err := q.UsageDistinctMonths(ctx, sqlc.UsageDistinctMonthsParams{Customers: monthSets.customers, MeterCodes: monthSets.meterCodes, FromIds: monthSets.fromIDs, ToIds: monthSets.toIDs, Generation: gen})
			if err != nil {
				return err
			}
			res := make([]rangeRow, len(rows))
			for j, r := range rows {
				res[j] = rangeRow{found: r.Found, quantity: fmt.Sprint(r.DistinctValues), events: r.DistinctValues}
			}
			fill(monthSets, res)
		}
		if len(hourSets.at) > 0 {
			rows, err := q.UsageDistinctHours(ctx, sqlc.UsageDistinctHoursParams{Customers: hourSets.customers, MeterCodes: hourSets.meterCodes, FromIds: hourSets.fromIDs, ToIds: hourSets.toIDs, Generation: gen})
			if err != nil {
				return err
			}
			res := make([]rangeRow, len(rows))
			for j, r := range rows {
				res[j] = rangeRow{found: r.Found, quantity: fmt.Sprint(r.DistinctValues), events: r.DistinctValues}
			}
			fill(hourSets, res)
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
