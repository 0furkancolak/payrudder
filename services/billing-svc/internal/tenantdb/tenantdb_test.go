package tenantdb

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

var (
	tenantA = uuid.MustParse("0190a1b2-0000-7000-8000-00000000000a")
	tenantB = uuid.MustParse("0190a1b2-0000-7000-8000-00000000000b")
	tenantC = uuid.MustParse("0190a1b2-0000-7000-8000-00000000000c")
	tenantD = uuid.MustParse("0190a1b2-0000-7000-8000-00000000000d")
	tenantE = uuid.MustParse("0190a1b2-0000-7000-8000-00000000000e")
)

// world is a DB over a directory and pools that are never used: nothing here reaches a database, only the placement logic.
//
//	A  cell c1, serving
//	B  cell c1, moving
//	C  cell c1, serving
//	D  pooled, serving
//	E  pooled, moving (on its way to a cell)
func world(t *testing.T) *DB {
	t.Helper()
	dir := tenantdir.Fixed{
		tenantA: {TenantID: tenantA, Cell: "c1", Status: tenantdir.StatusReady},
		tenantB: {TenantID: tenantB, Cell: "c1", Status: tenantdir.StatusMoving},
		tenantC: {TenantID: tenantC, Cell: "c1", Status: tenantdir.StatusReady},
		tenantD: {TenantID: tenantD, Status: tenantdir.StatusReady},
		tenantE: {TenantID: tenantE, Status: tenantdir.StatusMoving},
	}
	r, err := pgxx.NewRouter(&pgxpool.Pool{}, map[string]*pgxpool.Pool{"c1": {}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	return New(&pgxpool.Pool{}, r)
}

func TestEachVisitsThePooledDatabaseAndEveryServingCellTenantOnce(t *testing.T) {
	var seen []uuid.UUID
	err := world(t).Each(context.Background(), func(_ context.Context, s Scope) error {
		seen = append(seen, s.Tenant())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []uuid.UUID{uuid.Nil, tenantA, tenantC} // the pooled database once, the serving cell tenants once each, the moving one not at all
	if len(seen) != len(want) {
		t.Fatalf("visited %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("visited %v, want %v", seen, want)
		}
	}
}

func TestEachWithoutCellsIsOnePassOverThePool(t *testing.T) {
	d := New(&pgxpool.Pool{}, nil)
	n := 0
	if err := d.Each(context.Background(), func(_ context.Context, s Scope) error {
		n++
		if s.Tenant() != uuid.Nil {
			t.Errorf("scope of %s, want the pooled database", s.Tenant())
		}
		return nil
	}); err != nil || n != 1 {
		t.Fatalf("passes = %d, %v; want one", n, err)
	}
	if d.Cells() {
		t.Error("cells reported on")
	}
}

func TestOnePlaceFailingDoesNotStopTheOthers(t *testing.T) {
	var seen []uuid.UUID
	boom := errors.New("boom")
	err := world(t).Each(context.Background(), func(_ context.Context, s Scope) error {
		seen = append(seen, s.Tenant())
		if s.Tenant() == tenantA {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), tenantA.String()) {
		t.Fatalf("err = %v, want boom naming tenant A", err)
	}
	if len(seen) != 3 {
		t.Fatalf("visited %v after A failed, want all three places", seen)
	}
}

func TestACellScopeRefusesAnotherTenant(t *testing.T) {
	ctx := context.Background()
	_ = world(t).Each(ctx, func(_ context.Context, s Scope) error {
		if s.Tenant() != tenantA {
			return nil
		}
		if err := s.Check(ctx, tenantC); !errors.Is(err, ErrWrongTenant) || Skippable(err) {
			t.Errorf("Check of another tenant = %v, want ErrWrongTenant (a bug of the job, not something to skip)", err)
		}
		err := s.InTx(ctx, tenantC, pgx.TxOptions{}, func(pgx.Tx) error {
			t.Error("a transaction of another tenant ran in the scope")
			return nil
		})
		if !errors.Is(err, ErrWrongTenant) {
			t.Errorf("InTx of another tenant = %v, want ErrWrongTenant (a bug of the job, not something to skip)", err)
		}
		_, err = s.Q(tenantC).GetBillingRun(ctx, sqlc.GetBillingRunParams{TenantID: tenantC, PeriodID: "2026-10"})
		if !errors.Is(err, ErrWrongTenant) {
			t.Errorf("a statement for another tenant = %v, want ErrWrongTenant (a bug of the job, not something to skip)", err)
		}
		return nil
	})
}

func TestThePooledScopeLeavesWhatItShouldNotTouch(t *testing.T) {
	ctx := context.Background()
	_ = world(t).Each(ctx, func(_ context.Context, s Scope) error {
		if s.Tenant() != uuid.Nil {
			return nil
		}
		if err := s.Check(ctx, tenantD); err != nil {
			t.Errorf("a pooled tenant that serves: %v", err)
		}
		if err := s.Check(ctx, uuid.New()); err != nil {
			t.Errorf("a tenant the directory does not know is pooled: %v", err)
		}
		// Rows of a tenant that is moving are left alone: the copy would miss what the job wrote, and the retire would delete it.
		if err := s.Check(ctx, tenantE); !Moving(err) || !Skippable(err) {
			t.Errorf("a pooled tenant that is moving = %v, want not serving", err)
		}
		// Rows left in the pooled database by a move that did not finish: the cell has the live data.
		if err := s.Check(ctx, tenantA); !errors.Is(err, ErrMisplaced) || !Skippable(err) {
			t.Errorf("a tenant the directory puts in a cell = %v, want ErrMisplaced", err)
		}
		return nil
	})
}

// unknown answers that no tenant exists, and cannot be asked for anything else.
type unknown struct{ tenantdir.Fixed }

func (unknown) Placement(_ context.Context, id uuid.UUID) (tenantdir.Placement, error) {
	return tenantdir.Placement{}, tenantdir.ErrNotFound
}

func TestATenantTheDirectoryDoesNotKnowIsPooled(t *testing.T) {
	r, err := pgxx.NewRouter(&pgxpool.Pool{}, map[string]*pgxpool.Pool{"c1": {}}, unknown{})
	if err != nil {
		t.Fatal(err)
	}
	d := New(&pgxpool.Pool{}, r)
	ctx := context.Background()
	// Leftover rows of a tenant plans-svc no longer has: they are pooled (a cell needs a directory row), and a scan must not stop at them.
	if err := d.Check(ctx, uuid.New()); err != nil {
		t.Errorf("Check of an unknown tenant = %v, want it served from the pooled database", err)
	}
	_ = d.Each(ctx, func(_ context.Context, s Scope) error {
		if s.Tenant() == uuid.Nil {
			if err := s.Check(ctx, uuid.New()); err != nil {
				t.Errorf("the pooled scope refused an unknown tenant: %v", err)
			}
		}
		return nil
	})
}

func TestFindIsNeverNotFoundWhileATenantCouldNotBeSearched(t *testing.T) {
	ctx := context.Background()
	calls := 0
	nothing := func(context.Context, *sqlc.Queries) (bool, error) { calls++; return false, nil }

	found, err := world(t).Find(ctx, nothing)
	if found || !Moving(err) {
		t.Fatalf("Find = %v, %v; want an error that says a tenant is moving (B is)", found, err)
	}
	if calls != 3 { // the pooled database, A and C; B is not searched
		t.Errorf("probe ran %d times, want 3", calls)
	}

	// With nobody moving the answer is a plain no.
	dir := tenantdir.Fixed{tenantA: {TenantID: tenantA, Cell: "c1", Status: tenantdir.StatusReady}}
	r, err := pgxx.NewRouter(&pgxpool.Pool{}, map[string]*pgxpool.Pool{"c1": {}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if found, err := New(&pgxpool.Pool{}, r).Find(ctx, nothing); found || err != nil {
		t.Fatalf("Find with nobody moving = %v, %v; want not found", found, err)
	}

	// A hit ends the search, and a hit in the cell counts although the tenants after it were not asked.
	calls = 0
	hit := func(context.Context, *sqlc.Queries) (bool, error) { calls++; return calls == 2, nil }
	if found, err := world(t).Find(ctx, hit); !found || err != nil || calls != 2 {
		t.Fatalf("Find = %v, %v after %d probes; want a hit on the second place", found, err, calls)
	}
}

func TestFindFailureOfOneTenantIsNotANo(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	probe := func(context.Context, *sqlc.Queries) (bool, error) {
		calls++
		if calls == 2 {
			return false, boom
		}
		return false, nil
	}
	found, err := world(t).Find(context.Background(), probe)
	if found || !errors.Is(err, boom) {
		t.Fatalf("Find = %v, %v; want the failure of the tenant that could not be searched", found, err)
	}
	if calls != 3 {
		t.Errorf("probe ran %d times, want every serving place asked", calls)
	}
}
