// Package entitlements keeps each pod's in-memory map of customer limits, built from the compacted
// plans.entitlements.v1 topic, and mirrors it into Redis.
package entitlements

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/0furkancolak/quotify/gen/go/quotify/events/v1"
)

// State is the limit of one customer on one meter.
type State struct {
	Tenant      uuid.UUID
	Customer    uuid.UUID
	Meter       string
	PlanVersion string
	// Limit in scaled units; 0 = unlimited.
	Limit      int64
	Thresholds []int32
	From       time.Time
	Until      time.Time // zero = open-ended
	Scale      int
}

// Active reports whether the entitlement applies at t.
func (s State) Active(t time.Time) bool {
	return !t.Before(s.From) && (s.Until.IsZero() || t.Before(s.Until))
}

// MirrorKey is the Redis key mirroring the entitlement.
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
	next       map[int32]int64                // partition -> next offset to read
	targets    map[int32]int64                // partition -> end offset at start; nil until known
}

// New returns an empty Store. rdb may be nil (no mirror).
func New(rdb *redis.Client, log *slog.Logger) *Store {
	if log == nil {
		log = slog.Default()
	}
	return &Store{rdb: rdb, log: log, byKey: map[string]State{}, byCustomer: map[string]map[string]struct{}{}, next: map[int32]int64{}}
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
	st := State{
		Tenant: t, Customer: c, Meter: pb.GetMeterCode(), PlanVersion: pb.GetPlanVersionId(), Limit: pb.GetLimitUnits(),
		Thresholds: pb.GetAlertThresholdsPct(), Scale: int(pb.GetQuantityScale()),
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
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Meter < out[j-1].Meter; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
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
