package testkit

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
)

// AuditChain checks, on the migrations of a service, that the audit log is a hash chain: entries written through audit.Record are numbered and linked per tenant (and
// for the entries of no tenant), the verifier finds them whole, and an entry changed by the table's owner, the append-only trigger switched off, is found. svc is the
// short name of the service (plans, billing, payments): the test starts a Postgres of its own with that service's roles.
func AuditChain(t testing.TB, svc string, migrations fs.FS) {
	t.Helper()
	db := MigratedDB(t, StartPostgres(t), svc, migrations)
	ctx := startCtx(t)
	pool, err := pgxx.NewPool(ctx, db.System)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tenant := uuid.New()
	for i := range 3 {
		for _, id := range []uuid.UUID{tenant, uuid.Nil} {
			if err := audit.Record(ctx, pool, audit.Event{TenantID: id, Action: "test.chain", TargetType: "test", TargetID: string(rune('a' + i)), Summary: "entry"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	rep, err := audit.Verify(ctx, pool, "public")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() || rep.Chains != 2 || rep.Entries != 6 {
		t.Fatalf("testkit: %s: the log of a tenant and the log of no tenant should be two whole chains of three: %+v", svc, rep)
	}
	// Changed by the owner, whose rights the trigger and row-level security would otherwise stop: the verifier says which entry.
	conn, err := pgx.Connect(ctx, db.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, stmt := range []string{`ALTER TABLE audit_events DISABLE TRIGGER audit_events_append_only`, `ALTER TABLE audit_events NO FORCE ROW LEVEL SECURITY`,
		`UPDATE audit_events SET summary = 'changed' WHERE tenant_id = '` + tenant.String() + `' AND seq = 2`} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	rep, err = audit.Verify(ctx, tx, "public")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Problems) != 1 || rep.Problems[0].Seq != 2 || rep.Problems[0].Chain != tenant.String() || !strings.Contains(rep.Problems[0].What, "was changed") {
		t.Fatalf("testkit: %s: one changed entry of the tenant's chain should be the one problem: %+v", svc, rep.Problems)
	}
}
