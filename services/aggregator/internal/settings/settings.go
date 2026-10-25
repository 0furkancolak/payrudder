// Package settings snapshots tenant period settings (time zone, bounds, grace) into period_settings.
package settings

import (
	"context"
	"time"
	_ "time/tzdata" // tenant time zones must load in distroless images

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/tenantinfo"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/store/sqlc"
)

// Source snapshots the period settings of tenants, whose own settings come from pkg/tenantinfo (cached, one load per tenant).
type Source struct{ tenants tenantinfo.Source }

// NewSource returns a Source that loads tenants from plans-svc, cached for ttl (default 1 minute).
func NewSource(admin plansv1.AdminServiceClient, ttl time.Duration) *Source {
	return &Source{tenantinfo.New(admin, ttl)}
}

// NewSourceFunc returns a Source over any loader (tests).
func NewSourceFunc(load tenantinfo.Loader, ttl time.Duration) *Source {
	return &Source{tenantinfo.NewFunc(load, ttl)}
}

// Tenant returns the tenant's current settings.
func (s *Source) Tenant(ctx context.Context, id uuid.UUID) (tenantinfo.Settings, error) {
	return s.tenants.Tenant(ctx, id)
}

// Snapshot is the row to insert into period_settings.
type Snapshot struct {
	Timezone     string
	Start, End   time.Time
	GraceSeconds float64
	Cutoff       time.Time
}

// Compute derives the snapshot of a period for a tenant.
func Compute(t tenantinfo.Settings, periodID string) (Snapshot, error) {
	start, end, err := period.Bounds(periodID, t.Location)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Timezone: t.Timezone, Start: start.UTC(), End: end.UTC(), GraceSeconds: t.Grace.Seconds(), Cutoff: end.Add(t.Grace).UTC()}, nil
}

// Ensure inserts the snapshot of each (tenant, period) that has none yet. Existing rows are never changed: the
// snapshot is what makes classification reproducible.
func (s *Source) Ensure(ctx context.Context, q *sqlc.Queries, keys []Key) error {
	if len(keys) == 0 {
		return nil
	}
	tenants := make([]uuid.UUID, 0, len(keys))
	periods := make([]string, 0, len(keys))
	for _, k := range keys {
		tenants = append(tenants, k.Tenant)
		periods = append(periods, k.Period)
	}
	existing, err := q.PeriodSettingsExist(ctx, sqlc.PeriodSettingsExistParams{Tenants: tenants, Periods: periods})
	if err != nil {
		return err
	}
	have := make(map[Key]bool, len(existing))
	for _, e := range existing {
		have[Key{e.TenantID, e.PeriodID}] = true
	}
	for _, k := range keys {
		if have[k] {
			continue
		}
		t, err := s.Tenant(ctx, k.Tenant)
		if err != nil {
			return err
		}
		snap, err := Compute(t, k.Period)
		if err != nil {
			return err
		}
		if err := q.InsertPeriodSettings(ctx, sqlc.InsertPeriodSettingsParams{
			TenantID: k.Tenant, PeriodID: k.Period, Timezone: snap.Timezone, PeriodStart: snap.Start, PeriodEnd: snap.End,
			GraceSeconds: snap.GraceSeconds, CutoffAt: snap.Cutoff,
		}); err != nil {
			return err
		}
	}
	return nil
}

// Key identifies a period of a tenant.
type Key struct {
	Tenant uuid.UUID
	Period string
}
