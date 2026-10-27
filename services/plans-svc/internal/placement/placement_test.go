package placement

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// db is a connection that is never used: the tests below are about which one a function hands to its callback.
type db struct{}

func (db) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (db) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("not used")
}
func (db) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

func TestNoCellsIsThePooledDatabase(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	for name, p := range map[string]*Places{"nil": nil, "no router": NewPlaces(nil, nil)} {
		t.Run(name, func(t *testing.T) {
			if p.CellsOn() {
				t.Fatal("cells are on")
			}
			if in, err := p.InCell(ctx, id); in || err != nil {
				t.Fatalf("InCell = %v, %v", in, err)
			}
			var got sqlc.DBTX
			mine := db{}
			if err := p.On(ctx, id, mine, func(d sqlc.DBTX) error { got = d; return nil }); err != nil || got != (sqlc.DBTX)(mine) {
				t.Fatalf("On handed %v (%v), want the caller's connection", got, err)
			}
			called := 0
			if err := p.EachCell(ctx, func(context.Context, pgxx.PlacedTenant) error { called++; return nil }); err != nil || called != 0 {
				t.Fatalf("EachCell visited %d tenants, %v", called, err)
			}
			if p.Router() != nil {
				t.Fatal("a router without cells")
			}
		})
	}
}

func TestOnTenantPassesTheCallersConnection(t *testing.T) {
	var q *sqlc.Queries
	mine := db{}
	if err := (*Places)(nil).OnTenant(context.Background(), uuid.New(), mine, func(got *sqlc.Queries) error { q = got; return nil }); err != nil || q == nil {
		t.Fatalf("OnTenant = %v, %v", q, err)
	}
	boom := errors.New("boom")
	if err := (*Places)(nil).OnTenant(context.Background(), uuid.New(), mine, func(*sqlc.Queries) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("the callback's error was lost: %v", err)
	}
}
