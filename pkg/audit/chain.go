package audit

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/idgen"
)

// The hash chain of the log (docs/audit-log-and-two-factor.md, "The hash chain"). The database writes it: a trigger of audit_events numbers an entry in its chain (seq),
// names the entry before it (prev_hash) and hashes its own columns with that name (row_hash, by audit_events_hash), under an advisory lock of the chain. A chain is the
// entries of one tenant, or the entries that belong to no tenant ("none"). Verify reads a table back and recomputes every hash with the same SQL function, so it cannot
// disagree with the writer about the canonical form; Prune is the retention command.

// nilChain is the tenant of the chain of entries that belong to no tenant.
const nilChain = "00000000-0000-0000-0000-000000000000"

// Queryer is what Verify and Prune need of a *pgxpool.Pool, a *pgx.Conn or a pgx.Tx.
type Queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// chainName is how a chain is called in a report: its tenant's id, or "none".
func chainName(c string) string {
	if c == nilChain {
		return "none"
	}
	return c
}

// Problem is one thing wrong with a chain.
type Problem struct {
	Chain string // the tenant id, or "none"
	Seq   int64
	ID    string
	What  string
}

func (p Problem) String() string {
	return fmt.Sprintf("chain %s, entry %d (%s): %s", p.Chain, p.Seq, p.ID, p.What)
}

// Head is the newest entry of a chain. Anyone who can rewrite a whole chain, its tail included, can make it hash correctly: keep the heads somewhere else (a ticket, a
// log) and compare them by hand with the next run: a chain that ends before its remembered head lost entries.
type Head struct {
	Chain   string
	Seq     int64
	RowHash string
}

// Report is what Verify found in one table.
type Report struct {
	Schema   string
	Chains   int
	Entries  int64
	Pruned   int64 // entries the chain starts after (retention), 0 when every chain starts at 1
	Problems []Problem
	Heads    []Head
}

// OK reports whether the table's chains are whole.
func (r Report) OK() bool { return len(r.Problems) == 0 }

// Schemas lists the schemas of the database that hold an audit_events table: public, and the schema of each tenant a cell holds.
func Schemas(ctx context.Context, q Queryer) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT table_schema FROM information_schema.tables WHERE table_name = 'audit_events' AND table_type = 'BASE TABLE'
		AND table_schema NOT IN ('pg_catalog', 'information_schema') ORDER BY table_schema`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Verify recomputes the hash of every entry of the schema's audit_events and checks the links: each entry's hash is what its columns say, each entry follows the one
// before it in its chain by one and names its hash, and a chain starts at 1 (or after the entries retention deleted: an audit.pruned entry of the chain names the last of them, by number and, in before_hash, by hash).
// It reports every problem it finds, in the order of the chains and their entries; an error is a failure to read.
func Verify(ctx context.Context, q Queryer, schema string) (Report, error) {
	rep := Report{Schema: schema}
	table := pgx.Identifier{schema, "audit_events"}.Sanitize()
	fn := pgx.Identifier{schema, "audit_events_hash"}.Sanitize()
	rows, err := q.Query(ctx, `SELECT e.id::text, coalesce(e.tenant_id::text, '`+nilChain+`'), e.seq, e.prev_hash, e.row_hash, `+fn+`(e), e.action,
			coalesce(e.detail->>'through_seq', ''), e.before_hash
		FROM `+table+` e ORDER BY coalesce(e.tenant_id::text, '`+nilChain+`'), e.seq`)
	if err != nil {
		return rep, err
	}
	defer rows.Close()

	type anchor struct {
		seq  int64
		hash string
	}
	var (
		chain              string // the chain being read
		have               bool
		last, first        Head
		firstPrev, firstID string
		anchors            []anchor // the audit.pruned entries of the chain
	)
	endChain := func() {
		if !have {
			return
		}
		rep.Heads = append(rep.Heads, last)
		if first.Seq == 1 && firstPrev == "" {
			return
		}
		// A chain that does not start at the beginning is whole only after retention: an audit.pruned entry names the last entry that was deleted.
		for _, a := range anchors {
			if a.seq == first.Seq-1 && a.hash == firstPrev {
				rep.Pruned += a.seq
				return
			}
		}
		rep.Problems = append(rep.Problems, Problem{Chain: chainName(chain), Seq: first.Seq, ID: firstID, What: "the chain does not start at its beginning and no audit.pruned entry says why: entries are missing"})
	}
	for rows.Next() {
		var id, tenant, prev, hash, computed, action, throughSeq, throughHash string
		var seq int64
		if err := rows.Scan(&id, &tenant, &seq, &prev, &hash, &computed, &action, &throughSeq, &throughHash); err != nil {
			return rep, err
		}
		rep.Entries++
		if !have || tenant != chain {
			endChain()
			chain, have, anchors = tenant, true, nil
			rep.Chains++
			first, firstPrev, firstID = Head{Chain: chainName(tenant), Seq: seq, RowHash: hash}, prev, id
			last = Head{}
		} else {
			switch {
			case seq != last.Seq+1:
				rep.Problems = append(rep.Problems, Problem{Chain: chainName(chain), Seq: seq, ID: id, What: "entry " + strconv.FormatInt(seq, 10) + " follows entry " + strconv.FormatInt(last.Seq, 10) + ": entries are missing, doubled or out of order"})
			case prev != last.RowHash:
				rep.Problems = append(rep.Problems, Problem{Chain: chainName(chain), Seq: seq, ID: id, What: "it does not name the hash of the entry before it: an entry was changed or replaced"})
			}
		}
		if computed != hash {
			rep.Problems = append(rep.Problems, Problem{Chain: chainName(chain), Seq: seq, ID: id, What: "its hash is not the hash of what it says: the entry was changed"})
		}
		if action == "audit.pruned" && throughSeq != "" {
			if n, err := strconv.ParseInt(throughSeq, 10, 64); err == nil {
				anchors = append(anchors, anchor{seq: n, hash: throughHash})
			}
		}
		last = Head{Chain: chainName(tenant), Seq: seq, RowHash: hash}
	}
	if err := rows.Err(); err != nil {
		return rep, err
	}
	endChain()
	return rep, nil
}

// PruneResult is what Prune did, or would do, to one chain.
type PruneResult struct {
	Chain   string
	Deleted int64
	// Through is the last entry deleted (zero when none is).
	Through Head
}

// Prune is the retention of the log: it deletes, in each chain of the schema, the entries from the beginning up to the first one that is not older than cutoff (a prefix:
// the chain stays a chain), and appends to the chain the entry audit.pruned, which names the last entry deleted and its hash, so that Verify accepts the chain starting
// after it and nothing else. It never deletes a whole chain: the newest entry stays. With dry it deletes nothing and says what it would. Run it with the system role
// of the database; each chain is one transaction under the chain's own lock. only names the chains to prune (a tenant id, or "none"); no name means every chain.
func Prune(ctx context.Context, begin func(ctx context.Context) (pgx.Tx, error), schema string, cutoff time.Time, dry bool, only ...string) ([]PruneResult, error) {
	table := pgx.Identifier{schema, "audit_events"}.Sanitize()
	var chains []string
	{
		tx, err := begin(ctx)
		if err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT coalesce(tenant_id::text, '`+nilChain+`') FROM `+table+` ORDER BY 1`)
		if err == nil {
			chains, err = pgx.CollectRows(rows, pgx.RowTo[string])
		}
		_ = tx.Rollback(ctx)
		if err != nil {
			return nil, err
		}
	}
	var out []PruneResult
	for _, chain := range chains {
		if len(only) > 0 && !slices.Contains(only, chain) && (chain != nilChain || !slices.Contains(only, "none")) {
			continue
		}
		res, err := pruneChain(ctx, begin, schema, chain, cutoff, dry)
		if err != nil {
			return out, fmt.Errorf("audit: chain %s: %w", chain, err)
		}
		if res.Deleted > 0 {
			out = append(out, res)
		}
	}
	return out, nil
}

func pruneChain(ctx context.Context, begin func(ctx context.Context) (pgx.Tx, error), schema, chain string, cutoff time.Time, dry bool) (PruneResult, error) {
	table := pgx.Identifier{schema, "audit_events"}.Sanitize()
	lock := pgx.Identifier{schema, "audit_events_chain_lock"}.Sanitize()
	res := PruneResult{Chain: chainName(chain)}
	tx, err := begin(ctx)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Record writes to the table its search path names (the schema of a tenant in a cell, then the shared one).
	if _, err := tx.Exec(ctx, `SELECT set_config('search_path', $1, true)`, pgx.Identifier{schema}.Sanitize()+", public"); err != nil {
		return res, err
	}
	var tenantArg any
	if chain != nilChain {
		tenantArg = chain
	}
	if _, err := tx.Exec(ctx, `SELECT `+lock+`($1::uuid)`, tenantArg); err != nil {
		return res, err
	}
	const inChain = `coalesce(tenant_id::text, '` + nilChain + `') = $1`
	// The prefix ends before the first entry that is not older than the cutoff (or before the newest entry, which always stays): created_at is the start of the writing
	// transaction, which can be earlier than the entry before it in the chain, so the first young entry decides, not each entry's own age.
	var through int64
	err = tx.QueryRow(ctx, `SELECT coalesce((SELECT min(seq) FROM `+table+` WHERE `+inChain+` AND created_at >= $2), (SELECT max(seq) FROM `+table+` WHERE `+inChain+`)) - 1`, chain, cutoff).Scan(&through)
	if err != nil {
		return res, err
	}
	var first int64
	if err := tx.QueryRow(ctx, `SELECT coalesce(min(seq), 0) FROM `+table+` WHERE `+inChain, chain).Scan(&first); err != nil {
		return res, err
	}
	if through < first || first == 0 {
		return res, nil
	}
	if err := tx.QueryRow(ctx, `SELECT count(*), coalesce(max(row_hash) FILTER (WHERE seq = $3), '') FROM `+table+` WHERE `+inChain+` AND seq <= $2`, chain, through, through).Scan(&res.Deleted, &res.Through.RowHash); err != nil {
		return res, err
	}
	res.Through.Chain, res.Through.Seq = chainName(chain), through
	if dry || res.Deleted == 0 {
		return res, nil
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('payrudder.audit_prune', 'on', true)`); err != nil {
		return res, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE `+inChain+` AND seq <= $2`, chain, through); err != nil {
		return res, err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('payrudder.audit_prune', '', true)`); err != nil {
		return res, err
	}
	// The entry that says it. It is written here and not by Record, which masks anything that looks like a key, and the hash must stay what it is: it goes in before_hash,
	// the state before the entry (the last one deleted). The trigger appends it to the chain like every other.
	summary := "deleted " + strconv.FormatInt(res.Deleted, 10) + " entries older than " + cutoff.UTC().Format(time.RFC3339) + " from the beginning of the chain"
	detail := `{"through_seq": ` + strconv.FormatInt(through, 10) + `, "deleted": ` + strconv.FormatInt(res.Deleted, 10) + `, "cutoff": "` + cutoff.UTC().Format(time.RFC3339) + `"}`
	if _, err := tx.Exec(ctx, `INSERT INTO `+table+` (id, tenant_id, actor_type, actor_id, action, target_type, summary, detail, before_hash)
		VALUES ($1, $2::uuid, 'system', 'payrudderctl', 'audit.pruned', 'audit_events', $3, $4::jsonb, $5)`, idgen.NewUUIDv7(), tenantArg, summary, detail, res.Through.RowHash); err != nil {
		return res, err
	}
	return res, tx.Commit(ctx)
}
