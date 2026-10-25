package env

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
)

// Lookup reads the environment of a tenant from its system of record (plans-svc).
type Lookup func(ctx context.Context, tenant uuid.UUID) (Env, error)

// Directory answers "which environment is this tenant's?" for a worker that must refuse a command or an event whose environment disagrees. The environment of a
// tenant is immutable (a trigger in plans_db refuses a change), so an answer is kept for good; the map is bounded and starts again when it is full, so a
// worker serving many tenants stays small. A failed lookup is an error, never a guess: the caller retries, and nothing is let through on a missing answer.
type Directory struct {
	lookup Lookup
	max    int
	mu     sync.Mutex
	seen   map[uuid.UUID]Env
}

// NewDirectory returns a Directory that asks lookup and keeps at most max answers (10 000 when max is not positive).
func NewDirectory(lookup Lookup, max int) *Directory {
	if max <= 0 {
		max = 10_000
	}
	return &Directory{lookup: lookup, max: max, seen: map[uuid.UUID]Env{}}
}

// Of returns the environment of the tenant.
func (d *Directory) Of(ctx context.Context, tenant uuid.UUID) (Env, error) {
	d.mu.Lock()
	e, ok := d.seen[tenant]
	d.mu.Unlock()
	if ok {
		return e, nil
	}
	e, err := d.lookup(ctx, tenant)
	if err != nil {
		return "", fmt.Errorf("environment of tenant %s: %w", tenant, err)
	}
	if _, err := Require(e); err != nil {
		return "", fmt.Errorf("environment of tenant %s: %w", tenant, err)
	}
	d.mu.Lock()
	if len(d.seen) >= d.max {
		clear(d.seen)
	}
	d.seen[tenant] = e
	d.mu.Unlock()
	return e, nil
}

// Check fails closed unless the environment a command or event claims is a real one and the tenant's own. A lookup that fails is returned as it is (it is not
// a refusal: the caller retries). what names the claim for the message ("charge_payment command").
func (d *Directory) Check(ctx context.Context, tenant uuid.UUID, claimed plansv1.Environment, what string) error {
	got, err := FromProto(claimed)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	want, err := d.Of(ctx, tenant)
	if err != nil {
		return err
	}
	return Same(got, want, what)
}
