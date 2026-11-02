//go:build integration

package itest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
)

var errRollBack = errors.New("roll the tamper back")

// auditPlace is where a tenant's entries are: the pooled database, or in cells mode the tenant's schema of the cell.
func (e *env) auditPlace(tenantID string) (*pgxpool.Pool, string) {
	if e.w.cells && e.w.dir.inCell(uuid.MustParse(tenantID)) {
		return e.w.cellSys, tenantdir.Schema(uuid.MustParse(tenantID))
	}
	return e.system, "public"
}

// chainRows reads the numbers and hashes of a tenant's chain, in order.
func (e *env) chainRows(t *testing.T, tenantID string) (seqs []int64, prev, hash []string) {
	t.Helper()
	err := e.tx(context.Background(), tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT seq, prev_hash, row_hash FROM audit_events WHERE tenant_id = $1 ORDER BY seq`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s int64
			var p, h string
			if err := rows.Scan(&s, &p, &h); err != nil {
				return err
			}
			seqs, prev, hash = append(seqs, s), append(prev, p), append(hash, h)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return seqs, prev, hash
}

// writeEntries appends n entries to a tenant's log the way a service does: in a transaction of the app role, one after another.
func (e *env) writeEntries(t *testing.T, tenantID string, n int, action string) {
	t.Helper()
	tid := uuid.MustParse(tenantID)
	for i := range n {
		err := pgxx.InTenantTx(context.Background(), e.appDB, tid, func(tx pgx.Tx) error {
			return audit.Record(context.Background(), tx, audit.Event{TenantID: tid, Action: action, TargetType: "test", TargetID: fmt.Sprint(i), Summary: fmt.Sprintf("entry %d", i)})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// problemsOf are the problems a report holds for one chain.
func problemsOf(rep audit.Report, chain string) []audit.Problem {
	var out []audit.Problem
	for _, p := range rep.Problems {
		if p.Chain == chain {
			out = append(out, p)
		}
	}
	return out
}

// testAuditChain: every entry of the log carries the hash of the one before it, the verifier finds an entry that was changed, put in, taken out of the middle or from
// the beginning, and retention (audit.Prune) is the one way to delete a prefix, which the chain says in an entry of its own.
func testAuditChain(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	live := e.account(t, "Audit chain")[plansv1.Environment_ENVIRONMENT_LIVE]
	tid := live.GetId()
	seller := audit.Actor{Type: audit.ActorUser, ID: uuid.NewString(), Email: "seller@chain.example", IP: "203.0.113.11", RequestID: "req-chain-1"}
	tctx := actedAs(as(tid), seller)

	// The services write entries as before (here a customer's block and its lifting), and the database numbers and links them.
	for _, id := range []string{"cus_chain_a", "cus_chain_b"} {
		if _, err := e.plans.CreateCustomer(tctx, &plansv1.CreateCustomerRequest{ExternalId: id, Name: id, Email: id + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.BlockCustomer(tctx, &plansv1.BlockCustomerRequest{ExternalId: id, Reason: "chargebacks"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.UnblockCustomer(tctx, &plansv1.UnblockCustomerRequest{ExternalId: id}); err != nil {
			t.Fatal(err)
		}
	}
	e.writeEntries(t, tid, 4, "test.chain")

	t.Run("entries are numbered and linked", func(t *testing.T) {
		seqs, prev, hash := e.chainRows(t, tid)
		if len(seqs) < 8 {
			t.Fatalf("the tenant's log holds %d entries", len(seqs))
		}
		for i := range seqs {
			if seqs[i] != int64(i+1) {
				t.Fatalf("entry %d is numbered %d", i+1, seqs[i])
			}
			want := ""
			if i > 0 {
				want = hash[i-1]
			}
			if prev[i] != want || len(hash[i]) != 64 {
				t.Fatalf("entry %d: prev_hash %q, row_hash %q; the entry before it has %q", seqs[i], prev[i], hash[i], want)
			}
		}
	})

	t.Run("writers of one chain do not race", func(t *testing.T) {
		const writers = 12
		var wg sync.WaitGroup
		errs := make(chan error, writers)
		for i := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				id := uuid.MustParse(tid)
				errs <- pgxx.InTenantTx(ctx, e.appDB, id, func(tx pgx.Tx) error {
					return audit.Record(ctx, tx, audit.Event{TenantID: id, Action: "test.race", Summary: fmt.Sprint(i)})
				})
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		pool, schema := e.auditPlace(tid)
		rep, err := audit.Verify(ctx, pool, schema)
		if err != nil {
			t.Fatal(err)
		}
		if p := problemsOf(rep, tid); len(p) != 0 {
			t.Fatalf("the chain broke under %d writers: %v", writers, p)
		}
	})

	t.Run("the whole log of the database verifies", func(t *testing.T) {
		// Every entry every other test of the suite wrote went through the same trigger: the pooled tables, and in cells mode the schemas of the cell.
		pools := []*pgxpool.Pool{e.system}
		if e.w.cells {
			pools = append(pools, e.w.cellSys)
		}
		var chains int
		for _, pool := range pools {
			schemas, err := audit.Schemas(ctx, pool)
			if err != nil {
				t.Fatal(err)
			}
			for _, schema := range schemas {
				rep, err := audit.Verify(ctx, pool, schema)
				if err != nil {
					t.Fatal(err)
				}
				if !rep.OK() {
					t.Errorf("schema %s: %v", schema, rep.Problems)
				}
				chains += rep.Chains
			}
		}
		if chains == 0 {
			t.Error("no chain was found to verify")
		}
	})

	// tamper changes the tenant's rows as their owner would (the trigger that makes the table append-only switched off) and verifies inside the same transaction, which
	// is then rolled back, so the log stays whole for the tests after this one.
	tamper := func(t *testing.T, tid string, changes ...string) []audit.Problem {
		t.Helper()
		_, schema := e.auditPlace(tid)
		var found []audit.Problem
		err := pgxx.InTenantTx(ctx, e.ownerDB(t), uuid.MustParse(tid), func(tx pgx.Tx) error {
			if before, err := audit.Verify(ctx, tx, schema); err != nil || len(problemsOf(before, tid)) != 0 {
				return fmt.Errorf("the chain was not whole before the change: %v %v", err, problemsOf(before, tid))
			}
			if _, err := tx.Exec(ctx, `ALTER TABLE audit_events DISABLE TRIGGER audit_events_append_only`); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `SELECT set_config('payrudder.tenant_move', $1, true)`, tid); err != nil { // the chain trigger leaves rows alone while a tenant moves
				return err
			}
			for _, change := range changes {
				if _, err := tx.Exec(ctx, strings.ReplaceAll(change, "$1", "'"+tid+"'")); err != nil { // the id is a uuid this test made
					return err
				}
			}
			rep, err := audit.Verify(ctx, tx, schema)
			found = problemsOf(rep, tid)
			if err != nil {
				return err
			}
			return errRollBack
		})
		if !errors.Is(err, errRollBack) {
			t.Fatal(err)
		}
		return found
	}
	first := func(t *testing.T, ps []audit.Problem, seq int64, what string) {
		t.Helper()
		if len(ps) == 0 || ps[0].Seq != seq || !strings.Contains(ps[0].What, what) {
			t.Fatalf("problems = %v, want the first at entry %d about %q", ps, seq, what)
		}
	}

	t.Run("an entry that was changed is found", func(t *testing.T) {
		ps := tamper(t, tid, `UPDATE audit_events SET summary = 'nothing happened here' WHERE tenant_id = $1 AND seq = 3`)
		first(t, ps, 3, "was changed")
		if len(ps) != 1 {
			t.Errorf("one entry was changed: %v", ps)
		}
	})
	t.Run("an entry that was changed and given the hash of its new words is found at the next one", func(t *testing.T) {
		ps := tamper(t, tid, `UPDATE audit_events SET summary = 'nothing happened here' WHERE tenant_id = $1 AND seq = 3`,
			`UPDATE audit_events a SET row_hash = audit_events_hash(a) WHERE tenant_id = $1 AND seq = 3`)
		first(t, ps, 4, "does not name the hash")
	})
	t.Run("an entry taken out of the middle is found", func(t *testing.T) {
		ps := tamper(t, tid, `DELETE FROM audit_events WHERE tenant_id = $1 AND seq = 3`)
		first(t, ps, 4, "entries are missing")
	})
	t.Run("the beginning taken away without a word is found", func(t *testing.T) {
		ps := tamper(t, tid, `DELETE FROM audit_events WHERE tenant_id = $1 AND seq <= 2`)
		first(t, ps, 3, "does not start at its beginning")
	})
	t.Run("two entries put in the other order are found", func(t *testing.T) {
		ps := tamper(t, tid, `UPDATE audit_events SET seq = 1000003 WHERE tenant_id = $1 AND seq = 3`, `UPDATE audit_events SET seq = 3 WHERE tenant_id = $1 AND seq = 4`,
			`UPDATE audit_events SET seq = 4 WHERE tenant_id = $1 AND seq = 1000003`)
		if len(ps) == 0 {
			t.Fatal("two entries swapped places and the verifier saw nothing")
		}
	})
	t.Run("an entry written around the trigger with a made-up hash is found", func(t *testing.T) {
		ps := tamper(t, tid, `INSERT INTO audit_events (id, tenant_id, actor_type, action, summary, seq, prev_hash, row_hash)
			SELECT gen_random_uuid(), $1::uuid, 'system', 'test.forged', 'forged', max(seq) + 1, (SELECT row_hash FROM audit_events WHERE tenant_id = $1 ORDER BY seq DESC LIMIT 1), repeat('a', 64)
			FROM audit_events WHERE tenant_id = $1`)
		if len(ps) != 1 || !strings.Contains(ps[0].What, "was changed") {
			t.Fatalf("problems = %v", ps)
		}
	})

	t.Run("the app role cannot delete, flag or no flag", func(t *testing.T) {
		err := pgxx.InTenantTx(ctx, e.appDB, uuid.MustParse(tid), func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT set_config('payrudder.audit_prune', 'on', true)`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `DELETE FROM audit_events`)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("the app role deleted from the log, or failed for another reason: %v", err)
		}
	})

	t.Run("retention deletes a prefix and says so in the chain", func(t *testing.T) {
		pr := e.account(t, "Audit prune")[plansv1.Environment_ENVIRONMENT_LIVE].GetId()
		pool, schema := e.auditPlace(pr)
		e.writeEntries(t, pr, 6, "test.prune")
		seqs, _, hashes := e.chainRows(t, pr)
		var cutSeq int64
		var cutAt time.Time
		// The third of the six entries written here is where the cutoff goes: everything before it is older.
		if err := e.scan(ctx, pr, `SELECT seq, created_at FROM audit_events WHERE tenant_id = $1 AND action = 'test.prune' ORDER BY seq OFFSET 2 LIMIT 1`, []any{pr}, &cutSeq, &cutAt); err != nil {
			t.Fatal(err)
		}
		count := func() (n int) {
			t.Helper()
			if err := e.scan(ctx, pr, `SELECT count(*) FROM audit_events WHERE tenant_id = $1`, []any{pr}, &n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		whole := func(wantPruned int64) {
			t.Helper()
			rep, err := audit.Verify(ctx, pool, schema)
			if err != nil {
				t.Fatal(err)
			}
			if p := problemsOf(rep, pr); len(p) != 0 {
				t.Fatalf("the chain does not verify after retention: %v", p)
			}
			if rep.Pruned < wantPruned {
				t.Errorf("the report counts %d pruned entries, want at least %d", rep.Pruned, wantPruned)
			}
		}

		// A dry run says what it would delete and changes nothing.
		res, err := audit.Prune(ctx, pool.Begin, schema, cutAt, true, pr)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 1 || res[0].Deleted != cutSeq-1 || res[0].Through.Seq != cutSeq-1 || res[0].Through.RowHash != hashes[cutSeq-2] {
			t.Fatalf("dry run = %+v, want %d entries through entry %d (%s)", res, cutSeq-1, cutSeq-1, hashes[cutSeq-2])
		}
		if n := count(); n != len(seqs) {
			t.Fatalf("a dry run changed the log: %d entries, had %d", n, len(seqs))
		}

		// The real run deletes the prefix, appends the entry that says so, and the chain verifies from where it starts now.
		if res, err = audit.Prune(ctx, pool.Begin, schema, cutAt, false, pr); err != nil || len(res) != 1 || res[0].Deleted != cutSeq-1 {
			t.Fatalf("prune = %+v, %v", res, err)
		}
		if n := count(); n != len(seqs)-int(cutSeq-1)+1 {
			t.Fatalf("%d entries left, want %d and the entry that says why", n, len(seqs)-int(cutSeq-1))
		}
		var through, throughHash, actor string
		if err := e.scan(ctx, pr, `SELECT detail->>'through_seq', before_hash, actor_id FROM audit_events WHERE tenant_id = $1 AND action = 'audit.pruned'`, []any{pr}, &through, &throughHash, &actor); err != nil {
			t.Fatal(err)
		}
		if through != fmt.Sprint(cutSeq-1) || throughHash != hashes[cutSeq-2] || actor != "payrudderctl" {
			t.Errorf("audit.pruned says through %q (%q) by %q", through, throughHash, actor)
		}
		whole(cutSeq - 1)
		// Run again, nothing is older.
		if res, err = audit.Prune(ctx, pool.Begin, schema, cutAt, false, pr); err != nil || len(res) != 0 {
			t.Fatalf("a second run = %+v, %v", res, err)
		}

		// The words of the entry that says why are part of the chain: change them and the verifier does not accept the missing beginning.
		ps := tamper(t, pr, `UPDATE audit_events SET before_hash = repeat('0', 64) WHERE tenant_id = $1 AND action = 'audit.pruned'`)
		var unexplained bool
		for _, p := range ps {
			unexplained = unexplained || strings.Contains(p.What, "does not start at its beginning")
		}
		if !unexplained {
			t.Errorf("a changed anchor: %v", ps)
		}

		// The newest entry stays, however old: a cutoff after everything leaves it and the entry that says why.
		var head int64
		if err := e.scan(ctx, pr, `SELECT max(seq) FROM audit_events WHERE tenant_id = $1 AND action <> 'audit.pruned'`, []any{pr}, &head); err != nil {
			t.Fatal(err)
		}
		if res, err = audit.Prune(ctx, pool.Begin, schema, time.Now().Add(time.Hour), false, pr); err != nil || len(res) != 1 {
			t.Fatalf("prune everything = %+v, %v", res, err)
		}
		if n := count(); n != 2 {
			t.Fatalf("%d entries left, want the newest and the entry that says why", n)
		}
		whole(head - 1)
	})

	// Retention anchors on the stored hash of the last entry it deletes, so a change inside the prefix that it did not look for would vanish with the prefix and the
	// verifier would never find it. It verifies the chain first, under the chain's lock, and deletes nothing from a chain that has any problem.
	t.Run("retention refuses a chain that does not verify and deletes nothing", func(t *testing.T) {
		pr := e.account(t, "Audit prune refused")[plansv1.Environment_ENVIRONMENT_LIVE].GetId()
		_, schema := e.auditPlace(pr)
		e.writeEntries(t, pr, 6, "test.prune")
		for name, change := range map[string]string{
			"a changed entry inside the prefix":     `UPDATE audit_events SET summary = 'nothing happened here' WHERE tenant_id = $1 AND seq = 2`,
			"an entry taken out of the prefix":      `DELETE FROM audit_events WHERE tenant_id = $1 AND seq = 2`,
			"a changed entry that stays":            `UPDATE audit_events SET summary = 'nothing happened here' WHERE tenant_id = $1 AND seq = (SELECT max(seq) FROM audit_events WHERE tenant_id = $1)`,
			"the beginning taken away without word": `DELETE FROM audit_events WHERE tenant_id = $1 AND seq = 1`,
		} {
			err := pgxx.InTenantTx(ctx, e.ownerDB(t), uuid.MustParse(pr), func(tx pgx.Tx) error {
				for _, q := range []string{`ALTER TABLE audit_events DISABLE TRIGGER audit_events_append_only`, `SELECT set_config('payrudder.tenant_move', '` + pr + `', true)`, strings.ReplaceAll(change, "$1", "'"+pr+"'")} {
					if _, err := tx.Exec(ctx, q); err != nil {
						return err
					}
				}
				var before, after int
				count := func(into *int) error {
					return tx.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id = $1`, pr).Scan(into)
				}
				if err := count(&before); err != nil {
					return err
				}
				res, err := audit.Prune(ctx, tx.Begin, schema, time.Now().Add(time.Hour), false, pr)
				if err == nil || !strings.Contains(err.Error(), "does not verify") || len(res) != 0 {
					t.Errorf("%s: prune = %+v, %v; want a refusal that says the chain does not verify", name, res, err)
				}
				if err := count(&after); err != nil {
					return err
				}
				if after != before {
					t.Errorf("%s: %d entries before the refused prune, %d after", name, before, after)
				}
				return errRollBack
			})
			if !errors.Is(err, errRollBack) {
				t.Fatalf("%s: %v", name, err)
			}
		}
	})
}
