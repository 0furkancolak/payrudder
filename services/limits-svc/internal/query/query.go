// Package query implements LimitsService on top of the Redis counters and the entitlement map.
package query

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"time"
	_ "time/tzdata" // tenant time zones must load in distroless images

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	limitsv1 "github.com/0furkancolak/quotify/gen/go/quotify/limits/v1"
	"github.com/0furkancolak/quotify/pkg/period"
	"github.com/0furkancolak/quotify/pkg/quantity"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/limits-svc/internal/engine"
	"github.com/0furkancolak/quotify/services/limits-svc/internal/entitlements"
	"github.com/0furkancolak/quotify/services/limits-svc/internal/meters"
)

var periodRe = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

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

func invalid(msg string) error { return status.Error(codes.InvalidArgument, msg) }

func (s *Service) periodFor(ti tenant.Info, id string) (string, error) {
	if id != "" {
		if !periodRe.MatchString(id) {
			return "", invalid("period_id must look like 2026-11")
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
	customer, err := uuid.Parse(req.GetCustomerId())
	if err != nil {
		return nil, invalid("customer_id must be a UUID")
	}
	if req.GetMeterCode() == "" {
		return nil, invalid("meter_code is required")
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
		if errors.Is(err, meters.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "meter not found")
		}
		if err != nil {
			return nil, status.Error(codes.Unavailable, "meter lookup failed")
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
	customer, err := uuid.Parse(req.GetCustomerId())
	if err != nil {
		return nil, invalid("customer_id must be a UUID")
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
		return nil, status.Error(codes.Unavailable, "counter store unavailable")
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

// ReconcileNow implements limitsv1.LimitsServiceServer.
func (s *Service) ReconcileNow(ctx context.Context, _ *limitsv1.ReconcileNowRequest) (*limitsv1.ReconcileNowResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	if s.reconciler == nil {
		return nil, status.Error(codes.Unimplemented, "reconciliation is not configured")
	}
	res, err := s.reconciler.RunOnce(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "reconcile: %v", err)
	}
	return &limitsv1.ReconcileNowResponse{KeysChecked: res.Checked, Corrections: res.Corrections, PartitionsSkipped: res.Skipped}, nil
}
