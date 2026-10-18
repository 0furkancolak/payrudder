package query

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNoTenantNoBuilder(t *testing.T) {
	if _, err := ForTenant(uuid.Nil); !errors.Is(err, ErrNoTenant) {
		t.Fatalf("ForTenant(nil) = %v, want ErrNoTenant", err)
	}
	if _, err := AllTenants(false); err == nil {
		t.Fatal("a non-operator must not get the cross-tenant builder")
	}
	// A zero Builder (never constructed) can't be used to read tenant data either: every query would filter on the nil tenant.
	var zero Builder
	q, err := zero.TopCustomers("m", "2026-10", 5)
	if err != nil || q.Args[0] != uuid.Nil {
		t.Fatalf("zero builder: %+v, %v (must filter on the nil tenant, matching no rows)", q, err)
	}
}

// Every tenant query starts its WHERE clause with the tenant and binds the tenant as the first argument.
func TestEveryQueryIsTenantScoped(t *testing.T) {
	tenant := uuid.New()
	b, _ := ForTenant(tenant)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	from, to := now.Add(-6*time.Hour), now
	var qs []Query
	for _, exact := range []bool{false, true} {
		for _, g := range []Granularity{Hour, Day} {
			for _, cust := range []uuid.UUID{uuid.Nil, uuid.New()} {
				q, err := b.Usage(UsageFilter{Meter: "api", Customer: cust, From: from, To: to, Granularity: g, Exact: exact, Timezone: "Europe/Istanbul"})
				if err != nil {
					t.Fatal(err)
				}
				qs = append(qs, q)
			}
		}
	}
	top, _ := b.TopCustomers("api", "2026-10", 10)
	stats, _ := b.IngestStats(5*time.Minute, now)
	qs = append(qs, top, stats)
	for _, q := range qs {
		i := strings.Index(q.SQL, "WHERE")
		if i < 0 || !strings.HasPrefix(strings.TrimSpace(q.SQL[i+len("WHERE"):]), "tenant_id = ?") {
			t.Errorf("WHERE does not start with the tenant predicate:\n%s", q.SQL)
		}
		if q.Args[0] != tenant {
			t.Errorf("first argument = %v, want the tenant", q.Args[0])
		}
	}
}

func TestOperatorStatsSpanTenants(t *testing.T) {
	b, err := AllTenants(true)
	if err != nil {
		t.Fatal(err)
	}
	q, err := b.IngestStats(time.Minute, time.Now())
	if err != nil || strings.Contains(q.SQL, "tenant_id = ?") || !strings.Contains(q.SQL, "GROUP BY tenant_id") {
		t.Fatalf("operator stats = %+v, %v", q, err)
	}
	// The operator builder can't read a tenant's series.
	if _, err := b.Usage(UsageFilter{Meter: "m", From: time.Now().Add(-time.Hour), To: time.Now()}); err == nil {
		t.Fatal("the operator builder must not build series queries")
	}
}

func TestValidation(t *testing.T) {
	b, _ := ForTenant(uuid.New())
	now := time.Now()
	for name, f := range map[string]UsageFilter{
		"no meter":        {From: now.Add(-time.Hour), To: now},
		"reversed range":  {Meter: "m", From: now, To: now.Add(-time.Hour)},
		"exact too long":  {Meter: "m", From: now.Add(-48 * time.Hour), To: now, Exact: true},
		"bad granularity": {Meter: "m", From: now.Add(-time.Hour), To: now, Granularity: 9},
	} {
		if _, err := b.Usage(f); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := b.TopCustomers("m", "2026-10", 101); err == nil {
		t.Error("limit 101 must be refused")
	}
	if got := sanitizeTZ("Europe/Istanbul'; DROP TABLE x; --"); got != "UTC" {
		t.Errorf("a hostile time zone name was inlined: %q", got)
	}
}
