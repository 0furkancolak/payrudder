package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/services/notifier/internal/store/sqlc"
)

// Why an address is on the suppression list.
const (
	ReasonBounce    = "bounce"
	ReasonComplaint = "complaint"
	ReasonManual    = "manual"
)

// Suppression is an address a tenant's mail is not sent to.
type Suppression struct {
	TenantID  uuid.UUID `json:"tenant_id"`
	Address   string    `json:"address"`
	Reason    string    `json:"reason"`
	Source    string    `json:"source"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// Suppressions is the per-tenant suppression list. Addresses compare case-insensitively (they are stored lower case).
type Suppressions struct{ DB *pgxpool.Pool }

func norm(address string) string { return strings.ToLower(strings.TrimSpace(address)) }

// IsSuppressed reports whether mail to the address is suppressed for the tenant.
func (s Suppressions) IsSuppressed(ctx context.Context, tenantID uuid.UUID, address string) (bool, error) {
	return sqlc.New(s.DB).IsSuppressed(ctx, sqlc.IsSuppressedParams{TenantID: tenantID, Address: norm(address)})
}

// Add puts the address on the list and reports whether it was new; one already there keeps its first reason.
func (s Suppressions) Add(ctx context.Context, x Suppression) (bool, error) {
	n, err := sqlc.New(s.DB).Suppress(ctx, sqlc.SuppressParams{TenantID: x.TenantID, Address: norm(x.Address), Reason: x.Reason, Source: x.Source, Detail: x.Detail})
	return n == 1, err
}

// List returns up to pageSize addresses of the tenant after the given one, in address order.
func (s Suppressions) List(ctx context.Context, tenantID uuid.UUID, after string, pageSize int32) ([]Suppression, error) {
	rows, err := sqlc.New(s.DB).ListSuppressions(ctx, sqlc.ListSuppressionsParams{TenantID: tenantID, After: norm(after), PageSize: pageSize})
	if err != nil {
		return nil, err
	}
	out := make([]Suppression, len(rows))
	for i, r := range rows {
		out[i] = Suppression{TenantID: r.TenantID, Address: r.Address, Reason: r.Reason, Source: r.Source, Detail: r.Detail, CreatedAt: r.CreatedAt}
	}
	return out, nil
}

// Remove lifts the suppression and reports whether there was one.
func (s Suppressions) Remove(ctx context.Context, tenantID uuid.UUID, address string) (bool, error) {
	n, err := sqlc.New(s.DB).Unsuppress(ctx, sqlc.UnsuppressParams{TenantID: tenantID, Address: norm(address)})
	return n == 1, err
}
