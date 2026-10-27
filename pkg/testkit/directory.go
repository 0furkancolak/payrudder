package testkit

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/tenantdir"
)

// Directory is a tenant directory a test can change while the code under test reads it (a tenantdir.Lister). A tenant it does not know is pooled and ready.
type Directory struct {
	mu sync.Mutex
	m  tenantdir.Fixed
}

// NewDirectory returns an empty Directory: every tenant is pooled.
func NewDirectory() *Directory { return &Directory{m: tenantdir.Fixed{}} }

// Set places a tenant (or moves it, or changes its status).
func (d *Directory) Set(p tenantdir.Placement) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.m[p.TenantID] = p
}

// Place puts a tenant in a cell, ready.
func (d *Directory) Place(id uuid.UUID, cell string) {
	d.Set(tenantdir.Placement{TenantID: id, Cell: cell, Status: tenantdir.StatusReady})
}

// Placement implements tenantdir.Source.
func (d *Directory) Placement(ctx context.Context, id uuid.UUID) (tenantdir.Placement, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.m.Placement(ctx, id)
}

// Invalidate implements tenantdir.Source.
func (d *Directory) Invalidate(uuid.UUID) {}

// Placed implements tenantdir.Lister.
func (d *Directory) Placed(ctx context.Context, cell string) ([]tenantdir.Placement, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.m.Placed(ctx, cell)
}
