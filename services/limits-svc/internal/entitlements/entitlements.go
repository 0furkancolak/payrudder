// Package entitlements keeps each pod's in-memory map of customer limits, built from the compacted
// plans.entitlements.v1 topic, and mirrors it into Redis.
package entitlements

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
)

// State is the limit of one customer on one meter.
type State struct {
	Tenant   uuid.UUID
	Customer uuid.UUID
	Meter    string
	// Limit in scaled units; 0 = unlimited.
	Limit      int64
	Thresholds []int32
	From       time.Time
	Until      time.Time // zero = open-ended
	Scale      int
	// Window is what the limit counts over: the billing month, or a shorter window.
	Window period.Window
	// Mode is "hard", "soft" or "notify".
	Mode string
	// Override marks a customer's own limit in place of its plan version's.
	Override bool
}

// Allows reports whether CheckEntitlement answers allowed for used against this entitlement: always for an unlimited one or a soft or notify
// limit, and below the limit for a hard one.
func (s State) Allows(used int64) bool {
	return s.Limit == 0 || s.Mode != "hard" || used < s.Limit
}

// Active reports whether the entitlement applies at t.
func (s State) Active(t time.Time) bool {
	return !t.Before(s.From) && (s.Until.IsZero() || t.Before(s.Until))
}

// Suspension is a customer the seller has suspended: no meter is allowed while it holds. It is not a limit, so it is kept apart from the entitlements.
type Suspension struct {
	From  time.Time
	Until time.Time // zero = until a tombstone
}

// Active reports whether the suspension holds at t.
func (s Suspension) Active(t time.Time) bool {
	return !t.Before(s.From) && (s.Until.IsZero() || t.Before(s.Until))
}

// MirrorKey is the Redis key mirroring the entitlement. An empty meter is the mirror of the customer's suspension.
func MirrorKey(tenant, customer uuid.UUID, meter string) string {
	return fmt.Sprintf("lim:ent:%s:%s:%s", tenant, customer, meter)
}

// Store is the entitlement map of one pod.
type Store struct {
	rdb *redis.Client
	log *slog.Logger

	mu         sync.RWMutex
	byKey      map[string]State
	byCustomer map[string]map[string]struct{} // "tenant/customer" -> meters
	suspended  map[string]Suspension          // "tenant/customer" -> suspension
	next       map[int32]int64                // partition -> next offset to read
	targets    map[int32]int64                // partition -> end offset at start; nil until known
}

// New returns an empty Store. rdb may be nil (no mirror).
func New(rdb *redis.Client, log *slog.Logger) *Store {
	if log == nil {
		log = slog.Default()
	}
	return &Store{rdb: rdb, log: log, byKey: map[string]State{}, byCustomer: map[string]map[string]struct{}{}, suspended: map[string]Suspension{}, next: map[int32]int64{}}
}

func key(tenant, customer uuid.UUID, meter string) string {
	return tenant.String() + "/" + customer.String() + "/" + meter
}

func custKey(tenant, customer uuid.UUID) string { return tenant.String() + "/" + customer.String() }

// OffsetLoader makes a consumer read every partition from the beginning, whatever its group committed: the map is
// rebuilt from the compacted topic on every start.
func OffsetLoader(_ context.Context, _ string, partitions []int32) (map[int32]int64, error) {
	out := make(map[int32]int64, len(partitions))
	for _, p := range partitions {
		out[p] = 0
	}
	return out, nil
}

// Apply implements kafkax.PartitionHandler. An empty value is a tombstone.
func (s *Store) Apply(ctx context.Context, partition int32, recs []*kgo.Record) error {
	for _, rec := range recs {
		if len(rec.Value) == 0 {
			s.remove(ctx, string(rec.Key))
			continue
		}
		var pb eventsv1.EntitlementState
		if err := proto.Unmarshal(rec.Value, &pb); err != nil {
			s.log.Warn("skipping undecodable entitlement", "key", string(rec.Key), "offset", rec.Offset, "error", err)
			continue
		}
		if pb.GetMeterCode() == "" {
			s.suspend(ctx, &pb, rec)
			continue
		}
		st, err := fromProto(&pb)
		if err != nil {
			s.log.Warn("skipping invalid entitlement", "key", string(rec.Key), "offset", rec.Offset, "error", err)
			continue
		}
		s.mu.Lock()
		k := key(st.Tenant, st.Customer, st.Meter)
		s.byKey[k] = st
		ck := custKey(st.Tenant, st.Customer)
		if s.byCustomer[ck] == nil {
			s.byCustomer[ck] = map[string]struct{}{}
		}
		s.byCustomer[ck][st.Meter] = struct{}{}
		s.mu.Unlock()
		if s.rdb != nil {
			if err := s.rdb.Set(ctx, MirrorKey(st.Tenant, st.Customer, st.Meter), rec.Value, 0).Err(); err != nil {
				s.log.Warn("entitlement mirror write failed", "error", err)
			}
		}
	}
	if len(recs) > 0 {
		s.mu.Lock()
		s.next[partition] = recs[len(recs)-1].Offset + 1
		s.mu.Unlock()
	}
	return nil
}

// suspend applies a record on the empty meter, the customer's suspension: one that says suspended holds from its valid_from until its valid_until (open-ended when
// unset), one that does not lifts it.
func (s *Store) suspend(ctx context.Context, pb *eventsv1.EntitlementState, rec *kgo.Record) {
	t, err1 := uuid.Parse(pb.GetTenantId())
	c, err2 := uuid.Parse(pb.GetCustomerId())
	if err1 != nil || err2 != nil {
		s.log.Warn("skipping invalid suspension", "key", string(rec.Key), "offset", rec.Offset)
		return
	}
	if !pb.GetSuspended() {
		s.remove(ctx, string(rec.Key))
		return
	}
	sp := Suspension{}
	if pb.GetValidFrom() != nil {
		sp.From = pb.GetValidFrom().AsTime()
	}
	if pb.GetValidUntil() != nil {
		sp.Until = pb.GetValidUntil().AsTime()
	}
	s.mu.Lock()
	s.suspended[custKey(t, c)] = sp
	s.mu.Unlock()
	if s.rdb != nil {
		if err := s.rdb.Set(ctx, MirrorKey(t, c, ""), rec.Value, 0).Err(); err != nil {
			s.log.Warn("suspension mirror write failed", "error", err)
		}
	}
}

func (s *Store) remove(ctx context.Context, k string) {
	parts := strings.SplitN(k, "/", 3)
	if len(parts) != 3 {
		return
	}
	t, err1 := uuid.Parse(parts[0])
	c, err2 := uuid.Parse(parts[1])
	if err1 != nil || err2 != nil {
		return
	}
	s.mu.Lock()
	if parts[2] == "" {
		delete(s.suspended, custKey(t, c))
	}
	delete(s.byKey, k)
	if m := s.byCustomer[custKey(t, c)]; m != nil {
		delete(m, parts[2])
	}
	s.mu.Unlock()
	if s.rdb != nil {
		if err := s.rdb.Del(ctx, MirrorKey(t, c, parts[2])).Err(); err != nil {
			s.log.Warn("entitlement mirror delete failed", "error", err)
		}
	}
}

func fromProto(pb *eventsv1.EntitlementState) (State, error) {
	t, err := uuid.Parse(pb.GetTenantId())
	if err != nil {
		return State{}, fmt.Errorf("tenant_id: %w", err)
	}
	c, err := uuid.Parse(pb.GetCustomerId())
	if err != nil {
		return State{}, fmt.Errorf("customer_id: %w", err)
	}
	if pb.GetMeterCode() == "" || pb.GetLimitUnits() < 0 || pb.GetQuantityScale() > 6 {
		return State{}, fmt.Errorf("invalid meter, limit or scale")
	}
	w, ok := period.ParseWindow(pb.GetWindow())
	if !ok {
		return State{}, fmt.Errorf("unknown window %q", pb.GetWindow())
	}
	mode := pb.GetMode()
	switch mode {
	case "":
		mode = "hard"
	case "hard", "soft", "notify":
	default:
		return State{}, fmt.Errorf("unknown mode %q", mode)
	}
	st := State{
		Tenant: t, Customer: c, Meter: pb.GetMeterCode(), Limit: pb.GetLimitUnits(),
		Thresholds: pb.GetAlertThresholdsPct(), Scale: int(pb.GetQuantityScale()), Window: w, Mode: mode, Override: pb.GetOverride(),
	}
	if pb.GetValidFrom() != nil {
		st.From = pb.GetValidFrom().AsTime()
	}
	if pb.GetValidUntil() != nil {
		st.Until = pb.GetValidUntil().AsTime()
	}
	return st, nil
}

// Get returns the entitlement of the customer on the meter if it applies at now.
func (s *Store) Get(tenantID, customer uuid.UUID, meter string, now time.Time) (State, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.byKey[key(tenantID, customer, meter)]
	if !ok || !st.Active(now) {
		return State{}, false
	}
	return st, true
}

// Suspended reports whether the seller has suspended the customer at now: every meter of that customer answers "not allowed".
func (s *Store) Suspended(tenantID, customer uuid.UUID, now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sp, ok := s.suspended[custKey(tenantID, customer)]
	return ok && sp.Active(now)
}

// ForCustomer returns the entitlements of the customer that apply at now, ordered by meter code.
func (s *Store) ForCustomer(tenantID, customer uuid.UUID, now time.Time) []State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []State
	for meter := range s.byCustomer[custKey(tenantID, customer)] {
		if st, ok := s.byKey[key(tenantID, customer, meter)]; ok && st.Active(now) {
			out = append(out, st)
		}
	}
	slices.SortFunc(out, func(a, b State) int { return strings.Compare(a.Meter, b.Meter) })
	return out
}

// Len is the number of entitlements held.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byKey)
}

// SetTargets records the end offsets the store must reach before it is ready.
func (s *Store) SetTargets(ends map[int32]int64) {
	s.mu.Lock()
	s.targets = ends
	s.mu.Unlock()
}

// Ready reports whether the store has read every partition up to the end offsets recorded by SetTargets.
func (s *Store) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.targets == nil {
		return false
	}
	for p, end := range s.targets {
		if end > 0 && s.next[p] < end {
			return false
		}
	}
	return true
}
