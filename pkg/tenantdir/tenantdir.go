// Package tenantdir is the tenant directory of ADR 0018 as a service sees it: where a tenant's data lives (the pooled database under row-level security, or a schema
// in a cell), cached in process. plans-svc owns the table (tenants.cell, region, placement_status); every other service asks it through DirectoryService and
// never per request: an answer is served for a while, and when plans-svc is unreachable the last answer is served for much longer (stale-if-error), because a
// tenant's placement changes only when an operator moves it.
//
// What the directory cannot tell is whether a cached answer is the latest. A move therefore does not rely on caches expiring: it also fences the data plane
// (the source schema is renamed away, so a writer with a stale answer fails instead of writing to the old place), and a router drops an answer when the
// database says the schema is not there (Directory.Invalidate).
package tenantdir

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"golang.org/x/sync/singleflight"
)

// Status is the fence of a placement. Only Ready serves the tenant's traffic.
type Status string

const (
	// StatusReady is a placement that serves.
	StatusReady Status = "ready"
	// StatusProvisioning is a cell placement whose schema is being created.
	StatusProvisioning Status = "provisioning"
	// StatusMoving is a tenant being moved between places: nothing reads or writes it until the move ends.
	StatusMoving Status = "moving"
)

// Valid reports whether s is one of the three statuses.
func (s Status) Valid() bool { return s == StatusReady || s == StatusProvisioning || s == StatusMoving }

// Placement is where one tenant lives.
type Placement struct {
	TenantID uuid.UUID
	// Cell names the cell holding the tenant's schema; "" is the pooled database.
	Cell   string
	Region string
	Status Status
}

// Pooled reports whether the tenant is in the pooled database (row-level security).
func (p Placement) Pooled() bool { return p.Cell == "" }

// Serving reports whether the tenant's traffic is served now.
func (p Placement) Serving() bool { return p.Status == StatusReady }

// Errors of the directory.
var (
	// ErrNotFound is what a Loader returns (itself or wrapped) for a tenant the directory does not know.
	ErrNotFound = errors.New("tenantdir: tenant not found")
	// ErrNotServing wraps a placement that is provisioning or moving: the caller must refuse the request, never fall back to another place.
	ErrNotServing = errors.New("tenantdir: tenant is not serving")
)

// NotServing is the error for a placement that is not Ready; it matches ErrNotServing.
func NotServing(p Placement) error {
	return fmt.Errorf("%w: tenant %s is %s", ErrNotServing, p.TenantID, p.Status)
}

// Schema is the name of a tenant's schema in a cell: t_ and the 32 hex digits of the tenant id. The name is built from a parsed UUID only, so it is safe to
// quote into SQL, and it never starts with pg_.
func Schema(id uuid.UUID) string { return "t_" + strings.ReplaceAll(id.String(), "-", "") }

// TenantOfSchema is the inverse of Schema; ok is false for any other name.
func TenantOfSchema(name string) (uuid.UUID, bool) {
	hex, found := strings.CutPrefix(name, "t_")
	if !found || len(hex) != 32 {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(hex)
	if err != nil || Schema(id) != name {
		return uuid.Nil, false
	}
	return id, true
}

// Source answers where a tenant lives. *Directory is the implementation; tests use fakes.
type Source interface {
	Placement(ctx context.Context, id uuid.UUID) (Placement, error)
	// Invalidate drops the cached answer for id, so the next call asks again. It is for a caller that learned from the database that the answer is wrong.
	Invalidate(id uuid.UUID)
}

// Loader fetches one tenant's placement from its owner.
type Loader func(ctx context.Context, id uuid.UUID) (Placement, error)

// Lister fetches the placements of the tenants in a cell ("" for every cell); pooled tenants are not listed.
type Lister func(ctx context.Context, cell string) ([]Placement, error)

// Options tune a Directory. Zero values take the defaults noted.
type Options struct {
	// TTL is how long an answer is served without asking again (default 30s).
	TTL time.Duration
	// MaxStale is how long an answer is still served when asking fails (default 15m).
	MaxStale time.Duration
	// Size bounds the number of cached tenants (default 10000).
	Size int
	// LoadTimeout bounds one load (default 3s).
	LoadTimeout time.Duration
	// Now is the clock (default time.Now).
	Now func() time.Time
}

func (o *Options) defaults() {
	if o.TTL <= 0 {
		o.TTL = 30 * time.Second
	}
	if o.MaxStale < o.TTL {
		o.MaxStale = max(15*time.Minute, o.TTL)
	}
	if o.Size <= 0 {
		o.Size = 10000
	}
	if o.LoadTimeout <= 0 {
		o.LoadTimeout = 3 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

type entry struct {
	p  Placement
	at time.Time
}

// Directory is a Source over a Loader: a bounded process cache with stale-if-error and one load in flight per tenant.
type Directory struct {
	load   Loader
	list   Lister
	o      Options
	cache  *expirable.LRU[uuid.UUID, entry]
	flight singleflight.Group
}

// New returns a Directory over load. list may be nil when the process never iterates placements.
func New(load Loader, list Lister, o Options) *Directory {
	o.defaults()
	return &Directory{load: load, list: list, o: o, cache: expirable.NewLRU[uuid.UUID, entry](o.Size, nil, o.MaxStale)}
}

// Placement returns the tenant's placement: the cached answer while it is fresh, otherwise a new one. If the new one cannot be had, the last answer is returned
// as long as it is not older than MaxStale; beyond that, or without one, the error is returned. An unknown tenant is ErrNotFound and is not cached.
func (d *Directory) Placement(ctx context.Context, id uuid.UUID) (Placement, error) {
	cached, have := d.cache.Get(id)
	if have && d.o.Now().Sub(cached.at) < d.o.TTL {
		return cached.p, nil
	}
	res, err, _ := d.flight.Do(id.String(), func() (any, error) {
		// Detached from the first caller's cancellation: the answer is shared with every waiter.
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.o.LoadTimeout)
		defer cancel()
		p, err := d.load(lctx, id)
		if err != nil {
			return nil, err
		}
		p.TenantID = id
		d.cache.Add(id, entry{p: p, at: d.o.Now()})
		return p, nil
	})
	switch {
	case err == nil:
		return res.(Placement), nil
	case errors.Is(err, ErrNotFound):
		d.cache.Remove(id)
		return Placement{}, err
	case have && d.o.Now().Sub(cached.at) < d.o.MaxStale:
		return cached.p, nil
	}
	return Placement{}, fmt.Errorf("tenantdir: placement of %s: %w", id, err)
}

// Invalidate drops the cached answer for id.
func (d *Directory) Invalidate(id uuid.UUID) { d.cache.Remove(id) }

// Placed lists the tenants placed in cell ("" for every cell), straight from the owner: it is for jobs that iterate, which run on a timer and not per request.
func (d *Directory) Placed(ctx context.Context, cell string) ([]Placement, error) {
	if d.list == nil {
		return nil, errors.New("tenantdir: this directory cannot list placements")
	}
	return d.list(ctx, cell)
}

// Fixed is a Source over a fixed set of placements, for tests and tools that already know where everything is. A tenant not in the map is pooled.
type Fixed map[uuid.UUID]Placement

// Placement implements Source.
func (f Fixed) Placement(_ context.Context, id uuid.UUID) (Placement, error) {
	if p, ok := f[id]; ok {
		return p, nil
	}
	return Placement{TenantID: id, Status: StatusReady}, nil
}

// Invalidate implements Source; a fixed set has nothing cached.
func (f Fixed) Invalidate(uuid.UUID) {}
