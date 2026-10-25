// Package query implements LimitsService on top of the Redis counters and the entitlement map.
package query

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"
	_ "time/tzdata" // tenant time zones must load in distroless images

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/meters"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/quantity"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/engine"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/entitlements"
)

// Service implements limitsv1.LimitsServiceServer.
type Service struct {
	limitsv1.UnimplementedLimitsServiceServer
	rdb        *redis.Client
	ents       *entitlements.Store
	meters     meters.Source
	reconciler *engine.Reconciler
	now        func() time.Time
}

// New returns the service. reconciler may be nil (ReconcileNow then answers UNIMPLEMENTED).
func New(rdb *redis.Client, ents *entitlements.Store, ms meters.Source, rec *engine.Reconciler) *Service {
	return &Service{rdb: rdb, ents: ents, meters: ms, reconciler: rec, now: time.Now}
}

// invalid is an InvalidArgument status with the error key key.
func invalid(key, msg string) error { return errkey.New(codes.InvalidArgument, key, msg) }

func (s *Service) periodFor(ti tenant.Info, id string) (string, error) {
	if id != "" {
		if !period.Valid(id) {
			return "", invalid("limits.period.bad_id", "period_id must look like 2026-11")
		}
		return id, nil
	}
	loc := time.UTC
	if ti.Timezone != "" {
		if l, err := time.LoadLocation(ti.Timezone); err == nil {
			loc = l
		}
	}
	return period.PeriodOf(s.now(), loc), nil
}

// CheckEntitlement implements limitsv1.LimitsServiceServer.
func (s *Service) CheckEntitlement(ctx context.Context, req *limitsv1.CheckEntitlementRequest) (*limitsv1.CheckEntitlementResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	customer, err := errkey.ParseUUID("limits.customer.bad_id", "customer_id", req.GetCustomerId())
	if err != nil {
		return nil, err
	}
	if req.GetMeterCode() == "" {
		return nil, invalid("limits.meter.code_required", "meter_code is required")
	}
	pid, err := s.periodFor(ti, req.GetPeriodId())
	if err != nil {
		return nil, err
	}
	var ent *entitlements.State
	if st, ok := s.ents.Get(ti.ID, customer, req.GetMeterCode(), s.now()); ok {
		ent = &st
	}
	scale := 0
	if ent != nil {
		scale = ent.Scale
	} else {
		m, err := s.meters.Get(ctx, ti.ID, req.GetMeterCode())
		if errors.Is(err, engine.ErrMeterNotFound) {
			return nil, engine.ErrMeterNotFound
		}
		if err != nil {
			return nil, errkey.New(codes.Unavailable, "limits.meter.unavailable", "meter lookup failed")
		}
		scale = m.Scale
	}
	st, err := s.statusOf(ctx, ti.ID, customer, req.GetMeterCode(), pid, ent, scale)
	if err != nil {
		return nil, err
	}
	return &limitsv1.CheckEntitlementResponse{Status: st}, nil
}

// GetLimitStatus implements limitsv1.LimitsServiceServer.
func (s *Service) GetLimitStatus(ctx context.Context, req *limitsv1.GetLimitStatusRequest) (*limitsv1.GetLimitStatusResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	customer, err := errkey.ParseUUID("limits.customer.bad_id", "customer_id", req.GetCustomerId())
	if err != nil {
		return nil, err
	}
	pid, err := s.periodFor(ti, req.GetPeriodId())
	if err != nil {
		return nil, err
	}
	resp := &limitsv1.GetLimitStatusResponse{Statuses: []*limitsv1.LimitStatus{}}
	for _, ent := range s.ents.ForCustomer(ti.ID, customer, s.now()) {
		st, err := s.statusOf(ctx, ti.ID, customer, ent.Meter, pid, &ent, ent.Scale)
		if err != nil {
			return nil, err
		}
		resp.Statuses = append(resp.Statuses, st)
	}
	return resp, nil
}

func (s *Service) statusOf(ctx context.Context, tid, customer uuid.UUID, meter, pid string, ent *entitlements.State, scale int) (*limitsv1.LimitStatus, error) {
	vals, err := s.rdb.MGet(ctx, engine.CounterKey(tid, customer, meter, pid), engine.TSKey(tid)).Result()
	if err != nil {
		return nil, errkey.New(codes.Unavailable, "limits.counter.unavailable", "counter store unavailable")
	}
	var used, limit int64
	if v, ok := vals[0].(string); ok {
		used, _ = strconv.ParseInt(v, 10, 64)
	}
	if ent != nil {
		limit = ent.Limit
	}
	out := &limitsv1.LimitStatus{
		CustomerId: customer.String(), MeterCode: meter, PeriodId: pid,
		Used: quantity.FormatUnits(used, scale), Limit: quantity.FormatUnits(limit, scale),
		UsedUnits: used, LimitUnits: limit, QuantityScale: uint32(scale), //nolint:gosec // 0..6
		Unlimited: limit == 0,
	}
	out.Allowed = out.Unlimited || used < limit
	if v, ok := vals[1].(string); ok {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil && ms > 0 {
			out.AsOf = timestamppb.New(time.UnixMilli(ms).UTC())
		}
	}
	return out, nil
}

// reconcileFailed answers for a reconciliation that did not run: the cause goes to the log, the client gets a fixed message.
func reconcileFailed(ctx context.Context, err error) error {
	slog.WarnContext(ctx, "reconcile failed", "error", err)
	return errkey.New(codes.Unavailable, "limits.reconcile.unavailable", "reconciliation could not run right now")
}

// ReconcileNow implements limitsv1.LimitsServiceServer.
func (s *Service) ReconcileNow(ctx context.Context, _ *limitsv1.ReconcileNowRequest) (*limitsv1.ReconcileNowResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	if s.reconciler == nil {
		return nil, errkey.New(codes.Unimplemented, "limits.reconcile.not_configured", "reconciliation is not configured")
	}
	res, err := s.reconciler.RunOnce(ctx)
	if err != nil {
		return nil, reconcileFailed(ctx, err)
	}
	return &limitsv1.ReconcileNowResponse{KeysChecked: res.Checked, Corrections: res.Corrections, PartitionsSkipped: res.Skipped}, nil
}
