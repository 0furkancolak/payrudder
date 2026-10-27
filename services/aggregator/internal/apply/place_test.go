package apply

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/pkg/usage"
)

// lazyPool is a pool that has not connected: place only asks the router where a tenant is.
func lazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func itemOf(tn uuid.UUID, id string) item {
	return item{ev: usage.Event{TenantID: tn, EventID: id}}
}

func ids(items []item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ev.EventID
	}
	return out
}

func TestPlaceSplitsABatchByPlacement(t *testing.T) {
	dir := testkit.NewDirectory()
	router, err := pgxx.NewRouter(lazyPool(t), map[string]*pgxpool.Pool{"c1": lazyPool(t)}, dir)
	if err != nil {
		t.Fatal(err)
	}
	pooled, a, b := uuid.New(), uuid.New(), uuid.New()
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	dir.Place(a, "c1")
	dir.Place(b, "c1")
	h := &Handler{Cells: router}

	items := []item{itemOf(b, "b1"), itemOf(pooled, "p1"), itemOf(a, "a1"), itemOf(b, "b2"), itemOf(pooled, "p2"), itemOf(a, "a2")}
	gotPooled, cells, err := h.place(context.Background(), items)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(gotPooled); len(got) != 2 || got[0] != "p1" || got[1] != "p2" {
		t.Errorf("pooled = %v, want p1 p2 in order", got)
	}
	if len(cells) != 2 || cells[0].tenant != a || cells[1].tenant != b {
		t.Fatalf("cells = %v, want a then b (ascending by tenant)", cells)
	}
	if got := ids(cells[0].items); len(got) != 2 || got[0] != "a1" || got[1] != "a2" {
		t.Errorf("a's items = %v", got)
	}
	if got := ids(cells[1].items); len(got) != 2 || got[0] != "b1" || got[1] != "b2" {
		t.Errorf("b's items = %v", got)
	}
	// Every item is in exactly one group, and no group holds two tenants.
	n := len(gotPooled)
	for _, c := range cells {
		n += len(c.items)
		for _, it := range c.items {
			if it.ev.TenantID != c.tenant {
				t.Errorf("cell group of %s holds an item of %s", c.tenant, it.ev.TenantID)
			}
		}
	}
	if n != len(items) {
		t.Errorf("%d of %d items were placed", n, len(items))
	}
}

func TestPlaceRefusesTheWholeBatchForATenantItCannotPlace(t *testing.T) {
	dir := testkit.NewDirectory()
	router, err := pgxx.NewRouter(lazyPool(t), map[string]*pgxpool.Pool{"c1": lazyPool(t)}, dir)
	if err != nil {
		t.Fatal(err)
	}
	moving, ok, ghost := uuid.New(), uuid.New(), uuid.New()
	dir.Set(tenantdir.Placement{TenantID: moving, Cell: "c1", Status: tenantdir.StatusMoving})
	dir.Place(ok, "c1")
	dir.Place(ghost, "nowhere") // a cell this process has no connection to
	h := &Handler{Cells: router}

	if _, _, err := h.place(context.Background(), []item{itemOf(ok, "1"), itemOf(moving, "2")}); !errors.Is(err, tenantdir.ErrNotServing) {
		t.Errorf("a moving tenant: %v, want ErrNotServing", err)
	}
	if _, _, err := h.place(context.Background(), []item{itemOf(ok, "1"), itemOf(ghost, "2")}); !errors.Is(err, pgxx.ErrUnknownCell) {
		t.Errorf("a tenant in an unknown cell: %v, want ErrUnknownCell", err)
	}
}

func TestPlaceWithoutCellsIsEverythingPooled(t *testing.T) {
	h := &Handler{}
	items := []item{itemOf(uuid.New(), "1"), itemOf(uuid.New(), "2")}
	pooled, cells, err := h.place(context.Background(), items)
	if err != nil || len(pooled) != 2 || len(cells) != 0 {
		t.Fatalf("place = %v, %v, %v", pooled, cells, err)
	}
}
