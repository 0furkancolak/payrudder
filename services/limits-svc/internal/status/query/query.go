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
	return period.PeriodOf(s.now(), location(ti)), nil
}

// location is the tenant's time zone (UTC when it cannot be loaded).
func location(ti tenant.Info) *time.Location {
	if ti.Timezone != "" {
		if l, err := time.LoadLocation(ti.Timezone); err == nil {
			return l
		}
	}
	return time.UTC
}

// windowOf is the counter, window and reset time a status reads: the billing period pid for a monthly limit (or none), the current window for
// any other. The reset time of the lifetime window is the zero time.
func (s *Queries) windowOf(ti tenant.Info, customer uuid.UUID, meter, pid string, ent *entitlements.State) (key string, w period.Window, id string, resets time.Time) {
	loc := location(ti)
	if ent != nil && ent.Window != period.Month {
		id, _, resets = period.WindowOf(ent.Window, s.now(), loc)
		return engine.WindowKey(ti.ID, customer, meter, id), ent.Window, id, resets
	}
	_, resets, _ = period.Bounds(pid, loc)
	return engine.CounterKey(ti.ID, customer, meter, pid), period.Month, pid, resets
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
	return s.statusOf(ctx, ti, customer, meterCode, pid, ent, scale)
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
		st, err := s.statusOf(ctx, ti, customer, ent.Meter, pid, &ent, ent.Scale)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

func (s *Queries) statusOf(ctx context.Context, ti tenant.Info, customer uuid.UUID, meter, pid string, ent *entitlements.State, scale int) (*limitsv1.LimitStatus, error) {
	tid := ti.ID
	key, window, windowID, resets := s.windowOf(ti, customer, meter, pid, ent)
	vals, err := s.rdb.MGet(ctx, key, engine.TSKey(tid)).Result()
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
	mode := "hard"
	if ent != nil {
		limit, mode = ent.Limit, ent.Mode
	}
	out := &limitsv1.LimitStatus{
		CustomerId: customer.String(), MeterCode: meter, PeriodId: pid,
		Used: quantity.FormatUnits(used, scale), Limit: quantity.FormatUnits(limit, scale),
		UsedUnits: used, LimitUnits: limit, QuantityScale: uint32(scale), //nolint:gosec // 0..6
		Unlimited: limit == 0, Window: string(window), WindowId: windowID, Mode: mode,
		OverLimit: limit > 0 && used >= limit, Overridden: ent != nil && ent.Override,
	}
	if !resets.IsZero() { // the lifetime window never ends
		out.ResetsAt = timestamppb.New(resets)
	}
	out.Allowed = ent == nil || ent.Allows(used)
	if v, ok := vals[1].(string); ok {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil && ms > 0 {
			out.AsOf = timestamppb.New(time.UnixMilli(ms).UTC())
		}
	}
	return out, nil
}
