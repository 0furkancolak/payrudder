//go:build integration

package itest

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/migrations"
)

// The audit log is not truncated (security review): the owner of the table is refused by a trigger, and the service's own roles hold no TRUNCATE privilege at all. A tenant
// move into a cell empties the destination schema with a TRUNCATE of every table, which the trigger lets through for that tenant's schema only (the move tests of this
// package run that path).
func TestTheAuditLogIsNotTruncated(t *testing.T) {
	ctx := testkit.Ctx(t)
	db := testkit.MigratedDB(t, testkit.StartPostgres(t), "payments", migrations.FS)
	owner, err := pgx.Connect(ctx, db.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close(ctx) }()
	_, err = owner.Exec(ctx, `TRUNCATE audit_events`)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != pgxx.CodeRaiseException || pe.Hint != "evidence_table_truncate" {
		t.Errorf("the owner truncated the audit log: %v", err)
	}
	// Also through a TRUNCATE that names the table among others, and through a transaction that claims a move of some tenant: the pooled database is never a tenant's schema.
	for _, stmt := range []string{`TRUNCATE audit_events, payment_attempts`, `SELECT set_config('payrudder.tenant_move', '0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e', false); TRUNCATE audit_events`} {
		if _, err := owner.Exec(ctx, stmt); err == nil {
			t.Errorf("%s was allowed", stmt)
		}
	}
	sys, err := pgx.Connect(ctx, db.System)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sys.Close(ctx) }()
	if _, err := sys.Exec(ctx, `TRUNCATE audit_events`); pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
		t.Errorf("the system role: want 42501, got %v", err)
	}
}
