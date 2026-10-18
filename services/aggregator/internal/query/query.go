// Package query implements UsageQueryService: usage reads under RLS and the period operations billing needs.
package query

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	aggregatorv1 "github.com/0furkancolak/quotify/gen/go/quotify/aggregator/v1"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/aggregator/internal/apply"
	"github.com/0furkancolak/quotify/services/aggregator/internal/settings"
	"github.com/0furkancolak/quotify/services/aggregator/internal/store/sqlc"
)

// MaxBatchKeys is the size limit of GetUsageBatch.
const MaxBatchKeys = 1000

var periodRe = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

// Service implements aggregatorv1.UsageQueryServiceServer on the app pool (RLS enforced).
type Service struct {
	aggregatorv1.UnimplementedUsageQueryServiceServer
	app      *pgxpool.Pool
	settings *settings.Source
	group    string
	topic    string
}

// New returns a Service. group and topic key the consumer_offsets rows of the live consumer (defaults: the
// production group and topic).
func New(app *pgxpool.Pool, src *settings.Source, group, topic string) *Service {
	if group == "" {
		group = apply.Group
	}
	if topic == "" {
		topic = apply.Topic
	}
	return &Service{app: app, settings: src, group: group, topic: topic}
}

func invalid(msg string) error { return status.Error(codes.InvalidArgument, msg) }

func validPeriod(p string) error {
	if !periodRe.MatchString(p) {
		return invalid("period_id must look like 2026-11")
	}
	return nil
}

func mapErr(err error) error {
	if _, ok := status.FromError(err); ok && status.Code(err) != codes.Unknown {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return status.FromContextError(err).Err()
	}
	return status.Error(codes.Unavailable, "database unavailable")
}

// generation returns the pinned generation, or the tenant's active one.
func generation(ctx context.Context, q *sqlc.Queries, pinned int32) (int32, error) {
	if pinned > 0 {
		return pinned, nil
	}
	g, err := q.ActiveGeneration(ctx)
	return g, err
}

func usageRow(customer uuid.UUID, meter, period, qty string, scale int32, events int64) *aggregatorv1.UsageAggregate {
	return &aggregatorv1.UsageAggregate{
		CustomerId: customer.String(), MeterCode: meter, PeriodId: period,
		Quantity: formatScaled(qty, int(scale)), QuantityScale: uint32(scale), EventCount: events, //nolint:gosec // 0..6
	}
}

// GetCustomerUsage implements aggregatorv1.UsageQueryServiceServer.
func (s *Service) GetCustomerUsage(ctx context.Context, req *aggregatorv1.GetCustomerUsageRequest) (*aggregatorv1.GetCustomerUsageResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	customer, err := uuid.Parse(req.GetCustomerId())
	if err != nil {
		return nil, invalid("customer_id must be a UUID")
	}
	if err := validPeriod(req.GetPeriodId()); err != nil {
		return nil, err
	}
	resp := &aggregatorv1.GetCustomerUsageResponse{}
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		gen, err := generation(ctx, q, req.GetGeneration())
		if err != nil {
			return err
		}
		rows, err := q.CustomerUsage(ctx, sqlc.CustomerUsageParams{CustomerID: customer, PeriodID: req.GetPeriodId(), Generation: gen})
		if err != nil {
			return err
		}
		for _, r := range rows {
			resp.Usage = append(resp.Usage, usageRow(r.CustomerID, r.MeterCode, r.PeriodID, r.Quantity, r.QuantityScale, r.EventCount))
		}
		return nil
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return resp, nil
}

// GetUsageBatch implements aggregatorv1.UsageQueryServiceServer.
func (s *Service) GetUsageBatch(ctx context.Context, req *aggregatorv1.GetUsageBatchRequest) (*aggregatorv1.GetUsageBatchResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	n := len(req.GetKeys())
	if n == 0 || n > MaxBatchKeys {
		return nil, invalid(fmt.Sprintf("keys: need 1..%d, got %d", MaxBatchKeys, n))
	}
	customers := make([]uuid.UUID, n)
	meterCodes := make([]string, n)
	periods := make([]string, n)
	for i, k := range req.GetKeys() {
		c, err := uuid.Parse(k.GetCustomerId())
		if err != nil {
			return nil, invalid(fmt.Sprintf("keys[%d].customer_id must be a UUID", i))
		}
		if err := validPeriod(k.GetPeriodId()); err != nil {
			return nil, err
		}
		customers[i], meterCodes[i], periods[i] = c, k.GetMeterCode(), k.GetPeriodId()
	}
	resp := &aggregatorv1.GetUsageBatchResponse{ConsumerOffsets: map[int32]int64{}}
	// One snapshot for the usage and the offsets: "usage as of these offsets".
	err = pgxx.InTx(ctx, s.app, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, ti.ID.String()); err != nil {
			return err
		}
		q := sqlc.New(tx)
		gen, err := generation(ctx, q, req.GetGeneration())
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT a.quantity::text, a.quantity_scale::int, a.event_count
			FROM unnest($1::uuid[], $2::text[], $3::text[]) WITH ORDINALITY AS k(c, m, p, i)
			LEFT JOIN usage_aggregates a ON a.customer_id = k.c AND a.meter_code = k.m AND a.period_id = k.p AND a.generation = $4
			ORDER BY k.i`, customers, meterCodes, periods, gen)
		if err != nil {
			return err
		}
		defer rows.Close()
		i := 0
		for rows.Next() {
			var qty *string
			var scale *int32
			var events *int64
			if err := rows.Scan(&qty, &scale, &events); err != nil {
				return err
			}
			agg := &aggregatorv1.UsageAggregate{CustomerId: customers[i].String(), MeterCode: meterCodes[i], PeriodId: periods[i], Quantity: "0"}
			if qty != nil {
				agg = usageRow(customers[i], meterCodes[i], periods[i], *qty, *scale, *events)
			}
			resp.Usage = append(resp.Usage, agg)
			i++
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		offs, err := q.ListOffsets(ctx, sqlc.ListOffsetsParams{ConsumerGroup: s.group, Topic: s.topic})
		if err != nil {
			return err
		}
		for _, o := range offs {
			resp.ConsumerOffsets[o.Partition] = o.NextOffset
		}
		return nil
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return resp, nil
}

// StreamPeriodUsage implements aggregatorv1.UsageQueryServiceServer.
func (s *Service) StreamPeriodUsage(req *aggregatorv1.StreamPeriodUsageRequest, stream aggregatorv1.UsageQueryService_StreamPeriodUsageServer) error {
	ctx := stream.Context()
	ti, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	if err := validPeriod(req.GetPeriodId()); err != nil {
		return err
	}
	var out []*aggregatorv1.UsageAggregate
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		gen, err := generation(ctx, q, req.GetGeneration())
		if err != nil {
			return err
		}
		rows, err := q.PeriodUsage(ctx, sqlc.PeriodUsageParams{PeriodID: req.GetPeriodId(), Generation: gen})
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, usageRow(r.CustomerID, r.MeterCode, r.PeriodID, r.Quantity, r.QuantityScale, r.EventCount))
		}
		return nil
	})
	if err != nil {
		return mapErr(err)
	}
	for _, u := range out {
		if err := stream.Send(&aggregatorv1.StreamPeriodUsageResponse{Usage: u}); err != nil {
			return err
		}
	}
	return nil
}

// StreamLateUsage implements aggregatorv1.UsageQueryServiceServer.
func (s *Service) StreamLateUsage(req *aggregatorv1.StreamLateUsageRequest, stream aggregatorv1.UsageQueryService_StreamLateUsageServer) error {
	ctx := stream.Context()
	ti, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	if err := validPeriod(req.GetBilledPeriodId()); err != nil {
		return err
	}
	var out []*aggregatorv1.LateUsage
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		gen, err := generation(ctx, q, req.GetGeneration())
		if err != nil {
			return err
		}
		rows, err := q.LateUsageBilledIn(ctx, sqlc.LateUsageBilledInParams{BilledPeriodID: req.GetBilledPeriodId(), Generation: gen})
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, &aggregatorv1.LateUsage{
				CustomerId: r.CustomerID.String(), MeterCode: r.MeterCode, OriginPeriodId: r.OriginPeriodID, BilledPeriodId: r.BilledPeriodID,
				Quantity: formatScaled(r.Quantity, int(r.QuantityScale)), QuantityScale: uint32(r.QuantityScale), EventCount: r.EventCount, //nolint:gosec // 0..6
			})
		}
		return nil
	})
	if err != nil {
		return mapErr(err)
	}
	for _, u := range out {
		if err := stream.Send(&aggregatorv1.StreamLateUsageResponse{LateUsage: u}); err != nil {
			return err
		}
	}
	return nil
}

// GetIngestWatermark implements aggregatorv1.UsageQueryServiceServer.
func (s *Service) GetIngestWatermark(ctx context.Context, req *aggregatorv1.GetIngestWatermarkRequest) (*aggregatorv1.GetIngestWatermarkResponse, error) {
	offs, err := sqlc.New(s.app).ListOffsets(ctx, sqlc.ListOffsetsParams{ConsumerGroup: s.group, Topic: s.topic})
	if err != nil {
		return nil, mapErr(err)
	}
	resp := &aggregatorv1.GetIngestWatermarkResponse{CaughtUp: true, CommittedOffsets: map[int32]int64{}}
	for _, o := range offs {
		resp.CommittedOffsets[o.Partition] = o.NextOffset
	}
	for p, required := range req.GetRequiredOffsets() {
		if required > 0 && resp.CommittedOffsets[p] < required {
			resp.CaughtUp = false
		}
	}
	return resp, nil
}

// ensurePeriod makes sure the tenant's period has a settings row (a period without events has none yet).
func (s *Service) ensurePeriod(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, periodID string) error {
	t, err := s.settings.Tenant(ctx, tenantID)
	if err != nil {
		return err
	}
	snap, err := settings.Compute(t, periodID)
	if err != nil {
		return invalid(err.Error())
	}
	return sqlc.New(tx).EnsurePeriodSettingsApp(ctx, sqlc.EnsurePeriodSettingsAppParams{
		PeriodID: periodID, Timezone: snap.Timezone, PeriodStart: snap.Start, PeriodEnd: snap.End,
		GraceSeconds: snap.GraceSeconds, CutoffAt: snap.Cutoff,
	})
}

// FreezePeriod implements aggregatorv1.UsageQueryServiceServer.
func (s *Service) FreezePeriod(ctx context.Context, req *aggregatorv1.FreezePeriodRequest) (*aggregatorv1.FreezePeriodResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	if err := validPeriod(req.GetPeriodId()); err != nil {
		return nil, err
	}
	var frozen *timestamppb.Timestamp
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		if err := s.ensurePeriod(ctx, tx, ti.ID, req.GetPeriodId()); err != nil {
			return err
		}
		at, err := sqlc.New(tx).FreezePeriod(ctx, req.GetPeriodId())
		if err != nil {
			return err
		}
		if at != nil {
			frozen = timestamppb.New(*at)
		}
		return nil
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return &aggregatorv1.FreezePeriodResponse{FrozenAt: frozen}, nil
}

// ClosePeriodEarly implements aggregatorv1.UsageQueryServiceServer.
func (s *Service) ClosePeriodEarly(ctx context.Context, req *aggregatorv1.ClosePeriodEarlyRequest) (*aggregatorv1.ClosePeriodEarlyResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	// The environment comes from the authenticated tenant, never from the request.
	if ti.Environment != tenant.EnvSandbox {
		return nil, status.Error(codes.FailedPrecondition, "periods can only be closed early in a sandbox tenant")
	}
	if err := validPeriod(req.GetPeriodId()); err != nil {
		return nil, err
	}
	var cutoff *timestamppb.Timestamp
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		if err := s.ensurePeriod(ctx, tx, ti.ID, req.GetPeriodId()); err != nil {
			return err
		}
		at, err := sqlc.New(tx).ClosePeriodEarly(ctx, req.GetPeriodId())
		if errors.Is(err, pgx.ErrNoRows) {
			return status.Error(codes.FailedPrecondition, "period is frozen or already closes within 5 seconds")
		}
		if err != nil {
			return err
		}
		cutoff = timestamppb.New(at)
		return nil
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return &aggregatorv1.ClosePeriodEarlyResponse{CutoffAt: cutoff}, nil
}
