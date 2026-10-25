// Package query answers how much of a limit a customer has used, from the Redis counters and the entitlement map. It never writes.
package query

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

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

// Queries reads limit status.
type Queries struct {
	rdb    *redis.Client
	ents   *entitlements.Store
	meters meters.Source
	now    func() time.Time
}

// New returns the status queries.
func New(rdb *redis.Client, ents *entitlements.Store, ms meters.Source) *Queries {
	return &Queries{rdb: rdb, ents: ents, meters: ms, now: time.Now}
}

// invalid is an InvalidArgument status with the error key key.
func invalid(key, msg string) error { return errkey.New(codes.InvalidArgument, key, msg) }

func (s *Queries) periodFor(ti tenant.Info, id string) (string, error) {
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

// caller is what both queries start from: the calling tenant and the customer asked about.
func caller(ctx context.Context, customerID string) (tenant.Info, uuid.UUID, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return tenant.Info{}, uuid.Nil, err
	}
	customer, err := errkey.ParseUUID("limits.customer.bad_id", "customer_id", customerID)
	if err != nil {
		return tenant.Info{}, uuid.Nil, err
	}
	return ti, customer, nil
}

// Check is the status of one meter of a customer. A meter the customer has no entitlement on is unlimited.
func (s *Queries) Check(ctx context.Context, customerID, meterCode, periodID string) (*limitsv1.LimitStatus, error) {
	ti, customer, err := caller(ctx, customerID)
	if err != nil {
		return nil, err
	}
	if meterCode == "" {
		return nil, invalid("limits.meter.code_required", "meter_code is required")
	}
	pid, err := s.periodFor(ti, periodID)
	if err != nil {
		return nil, err
	}
	var ent *entitlements.State
	if st, ok := s.ents.Get(ti.ID, customer, meterCode, s.now()); ok {
		ent = &st
	}
	scale := 0
	if ent != nil {
		scale = ent.Scale
	} else {
		m, err := s.meters.Get(ctx, ti.ID, meterCode)
		if errors.Is(err, engine.ErrMeterNotFound) {
			return nil, engine.ErrMeterNotFound
		}
		if err != nil {
			slog.WarnContext(ctx, "meter lookup failed", "tenant", ti.ID, "meter", meterCode, "error", err)
			return nil, errkey.New(codes.Unavailable, "limits.meter.unavailable", "meter lookup failed")
		}
		scale = m.Scale
	}
	return s.statusOf(ctx, ti.ID, customer, meterCode, pid, ent, scale)
}

// List is the status of every meter the customer has an entitlement on, ordered by meter code.
func (s *Queries) List(ctx context.Context, customerID, periodID string) ([]*limitsv1.LimitStatus, error) {
	ti, customer, err := caller(ctx, customerID)
	if err != nil {
		return nil, err
	}
	pid, err := s.periodFor(ti, periodID)
	if err != nil {
		return nil, err
	}
	out := []*limitsv1.LimitStatus{}
	for _, ent := range s.ents.ForCustomer(ti.ID, customer, s.now()) {
		st, err := s.statusOf(ctx, ti.ID, customer, ent.Meter, pid, &ent, ent.Scale)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

func (s *Queries) statusOf(ctx context.Context, tid, customer uuid.UUID, meter, pid string, ent *entitlements.State, scale int) (*limitsv1.LimitStatus, error) {
	vals, err := s.rdb.MGet(ctx, engine.CounterKey(tid, customer, meter, pid), engine.TSKey(tid)).Result()
	if err != nil {
		slog.WarnContext(ctx, "counter read failed", "tenant", tid, "error", err)
		return nil, errkey.New(codes.Unavailable, "limits.counter.unavailable", "counter store unavailable")
	}
	var used, limit int64
	if v, ok := vals[0].(string); ok {
		if used, err = strconv.ParseInt(v, 10, 64); err != nil {
			slog.WarnContext(ctx, "counter holds a non-number", "tenant", tid, "value", v)
			used = 0
		}
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
