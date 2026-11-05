//go:build integration

package itest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/stripe"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/secretbox/reseal"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
	"github.com/0furkancolak/payrudder/services/payments-worker/migrations"
)

// The rehearsal of steps 1 to 3 of the account_id migration (docs/adr/0026-integration-accounts.md, docs/runbooks/account-id-migration-rehearsal.md) on SYNTHETIC data: it
// builds the database as the previous release left it, fills it, records the truth, applies the new migrations, runs the backfill and the verify mode, reads through
// the shadow comparison, and asserts every check the runbook table names for those steps. Steps 4 and 5 are not here.
//
// The scale is set by ACCOUNTID_REHEARSAL_TENANTS (default 20) and ACCOUNTID_REHEARSAL_LEDGER (default 100000 rows of payment_ledger):
//
//	ACCOUNTID_REHEARSAL_LEDGER=1000000 go test -tags integration -run TestAccountIDRehearsal -v ./internal/itest
//
// What it measures is correctness. A Docker VM on a laptop says nothing of how long a step takes on production hardware.
func TestAccountIDRehearsal(t *testing.T) {
	tenants, ledgerRows := envInt("ACCOUNTID_REHEARSAL_TENANTS", 20), envInt("ACCOUNTID_REHEARSAL_LEDGER", 100000)
	k1 := bytes.Repeat([]byte{1}, 32)
	box, _ := secretbox.New(k1)
	ctx := testkit.Ctx(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("pooled database at scale", func(t *testing.T) {
		pg := testkit.StartPostgres(t)
		db := testkit.CreateServiceDB(t, pg, "payments")
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		// The database as the previous release left it: every migration but the ones of this change.
		previous := fstest.MapFS{}
		entries, err := fs.ReadDir(migrations.FS, ".")
		must(err)
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".sql") || strings.HasPrefix(e.Name(), "00017_") {
				continue
			}
			b, err := fs.ReadFile(migrations.FS, e.Name())
			must(err)
			previous[e.Name()] = &fstest.MapFile{Data: b}
		}
		must(pgxx.Migrate(ctx, db.Migrator, previous, nil))
		pool, err := pgxx.NewPool(ctx, db.System)
		must(err)
		t.Cleanup(pool.Close)
		accounts, einvoices := store.Accounts{DB: pool, Box: box}, store.EInvoiceAccounts{DB: pool, Box: box}

		// Synthetic data: per tenant three live payment accounts at two providers (stripe, the labelled stripe~eu and a bank transfer), a sandbox stripe account for every
		// second tenant, two e-invoice accounts (nilvera, nilvera~tr), and the ledger rows spread over the tenants.
		var ids []uuid.UUID
		olds := 0
		for i := 0; i < tenants; i++ {
			id := uuid.New()
			ids = append(ids, id)
			stripeSettings := func(prefix, extra string) []byte {
				return []byte(fmt.Sprintf(`{"api_key":"%s_%d%s","webhook_secret":"whsec_%d","base_url":"http://127.0.0.1:1"}`, prefix, i, extra, i))
			}
			must(accounts.Put(ctx, id, "stripe", "live", stripeSettings("sk_live", ""), true))
			must(accounts.Put(ctx, id, "stripe~eu", "live", stripeSettings("sk_live", "eu"), false))
			must(accounts.Put(ctx, id, "banktransfer", "live", []byte(fmt.Sprintf(`{"iban":"TR00 0000 %04d","holder":"Tenant %d"}`, i, i)), false))
			olds += 3
			if i%2 == 0 {
				must(accounts.Put(ctx, id, "stripe", "sandbox", stripeSettings("sk_test", ""), true))
				olds++
			}
			must(einvoices.Put(ctx, id, "live", "nilvera", "production", []byte(fmt.Sprintf(`{"api_key":"nv_%d"}`, i)), true, store.Audit{}))
			must(einvoices.Put(ctx, id, "live", "nilvera~tr", "production", []byte(fmt.Sprintf(`{"api_key":"nv_%dtr"}`, i)), false, store.Audit{}))
			olds += 2
		}
		start := time.Now()
		_, err = pool.Exec(ctx, `INSERT INTO payment_ledger (tenant_id, invoice_id, provider, environment, kind, status, amount_minor, currency, provider_ref, idempotency_key, source, occurred_at)
			SELECT ($1::uuid[])[1 + g % $3], gen_random_uuid(), (ARRAY['stripe','stripe~eu','banktransfer'])[1 + g % 3], 'live', 'charge', 'succeeded', 1000 + g % 5000, 'EUR',
			       'pi_' || g, 'k' || g, 'command', now() - make_interval(secs => g)
			FROM generate_series(1, $2) g`, ids, ledgerRows, tenants)
		must(err)
		t.Logf("REHEARSAL synthetic data: %d tenants, %d old accounts, %d payment_ledger rows (inserted in %s)", tenants, olds, ledgerRows, time.Since(start).Round(time.Millisecond))

		// Record the truth (runbook, "Before the first step").
		oldTables := []string{"payment_ledger", "provider_objects", "payment_attempts", "provider_subscriptions", "reconciliation_discrepancies", "settlement_lines", "fee_rules",
			"provider_account_health", "provider_accounts", "einvoice_accounts"}
		before := truthOf(ctx, t, pool, oldTables)
		for _, tb := range oldTables {
			t.Logf("REHEARSAL truth before: %-30s rows %-8d checksum %s", tb, before.rows[tb], before.sum[tb])
		}
		if before.rows["payment_ledger"] != int64(ledgerRows) || before.rows["provider_accounts"]+before.rows["einvoice_accounts"] != int64(olds) {
			t.Fatalf("the synthetic data is not what was asked for: %v", before.rows)
		}

		// Step 1: apply the migrations.
		t.Run("step 1 new tables beside the old", func(t *testing.T) {
			start := time.Now()
			must(pgxx.Migrate(ctx, db.Migrator, migrations.FS, nil))
			t.Logf("REHEARSAL step 1: migration 00017 applied in %s", time.Since(start).Round(time.Millisecond))
			if after := truthOf(ctx, t, pool, oldTables); !after.equal(before) {
				t.Errorf("an old table changed in step 1:\n%s", after.diff(before))
			}
			testkit.ForcedRLS(t, pool, migrations.RLSExempt)
			// The old release still works against the new schema: it reads and writes its tables as before.
			if got, _, err := accounts.Settings(ctx, ids[0], "stripe~eu", "live"); err != nil || !strings.Contains(string(got), "sk_live_0eu") {
				t.Errorf("old read after step 1 = %q, %v", got, err)
			}
			var accountsN, bindingsN int
			must(pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM integration_accounts), (SELECT count(*) FROM integration_bindings)`).Scan(&accountsN, &bindingsN))
			if accountsN != 0 || bindingsN != 0 {
				t.Errorf("the new tables hold %d accounts and %d bindings before the backfill", accountsN, bindingsN)
			}
		})

		in := reseal.Input{Box: box, Columns: reseal.Payments, Pooled: pool, Log: log}
		// Step 2: backfill, owner = tenant, and the verify mode.
		t.Run("step 2 backfill and verify", func(t *testing.T) {
			dry := in
			dry.DryRun = true
			r, err := reseal.BackfillAccounts(ctx, dry)
			must(err)
			if r.Old != olds || r.Created != olds || r.Bindings != 0 {
				t.Errorf("dry run = %+v, want %d created and nothing written", r, olds)
			}
			if n := count(ctx, t, pool, `SELECT count(*) FROM integration_accounts`); n != 0 {
				t.Errorf("a dry run wrote %d accounts", n)
			}

			start := time.Now()
			r, err = reseal.BackfillAccounts(ctx, in)
			must(err)
			t.Logf("REHEARSAL step 2: backfill %+v in %s", r, time.Since(start).Round(time.Millisecond))
			if r.Old != olds || r.Created != olds || r.Bindings != olds || r.Unreadable != 0 || r.Orphans != 0 {
				t.Errorf("backfill = %+v, want %d created, %d bindings, nothing unreadable", r, olds, olds)
			}
			// Every old account has exactly one new account and one binding with the same label and default flag.
			for _, c := range []struct{ what, q string }{
				{"payment accounts without exactly one account and binding of the same label and default flag", `
					SELECT count(*) FROM provider_accounts o WHERE (SELECT count(*) FROM integration_accounts a JOIN integration_bindings b ON b.account_id = a.id
					 WHERE a.owner_kind = 'tenant' AND a.owner_id = o.tenant_id AND a.kind = 'payment' AND a.environment = o.environment AND a.provider = split_part(o.provider, '~', 1)
					   AND a.label = coalesce(nullif(split_part(o.provider, '~', 2), ''), '') AND b.tenant_id = o.tenant_id AND b.environment = o.environment AND b.kind = 'payment'
					   AND b.provider = a.provider AND b.label = a.label AND b.is_default = o.is_default) <> 1`},
				{"e-invoice accounts without exactly one account and binding of the same label, default flag and mode", `
					SELECT count(*) FROM einvoice_accounts o WHERE (SELECT count(*) FROM integration_accounts a JOIN integration_bindings b ON b.account_id = a.id
					 WHERE a.owner_kind = 'tenant' AND a.owner_id = o.tenant_id AND a.kind = 'einvoice' AND a.environment = o.environment AND a.provider = split_part(o.provider, '~', 1)
					   AND a.label = coalesce(nullif(split_part(o.provider, '~', 2), ''), '') AND a.mode = o.mode AND b.tenant_id = o.tenant_id AND b.environment = o.environment
					   AND b.kind = 'einvoice' AND b.provider = a.provider AND b.label = a.label AND b.is_default = o.is_default) <> 1`},
				{"new accounts that are not owned by their tenant", `SELECT count(*) FROM integration_accounts WHERE owner_kind <> 'tenant' OR owner_id NOT IN (SELECT unnest($1::uuid[]))`},
			} {
				args := []any{}
				if strings.Contains(c.q, "$1") {
					args = append(args, ids)
				}
				if n := count(ctx, t, pool, c.q, args...); n != 0 {
					t.Errorf("%d %s", n, c.what)
				}
			}
			if a, b := count(ctx, t, pool, `SELECT count(*) FROM integration_accounts`), count(ctx, t, pool, `SELECT count(*) FROM integration_bindings`); a != olds || b != olds {
				t.Errorf("%d accounts and %d bindings for %d old accounts", a, b, olds)
			}
			// Every sealed box opens under the new AAD: the verify mode, which opens every box of every column and changes nothing.
			ver := in
			ver.Verify = true
			start = time.Now()
			reports, err := reseal.Run(ctx, ver)
			must(err)
			for _, rp := range reports {
				if rp.Unreadable != 0 {
					t.Errorf("verify: %+v", rp)
				}
				t.Logf("REHEARSAL step 2 verify: %-34s boxes %d, do not open %d", rp.Column, rp.Boxes, rp.Unreadable)
				if rp.Column == "integration_accounts.settings_enc" && rp.Boxes != olds {
					t.Errorf("verify opened %d boxes of the new accounts, want %d", rp.Boxes, olds)
				}
			}
			t.Logf("REHEARSAL step 2 verify took %s", time.Since(start).Round(time.Millisecond))
			// The old rows are unchanged.
			if after := truthOf(ctx, t, pool, oldTables); !after.equal(before) {
				t.Errorf("an old table changed in step 2:\n%s", after.diff(before))
			}
			// A second run finds nothing to do.
			r, err = reseal.BackfillAccounts(ctx, in)
			must(err)
			if r.Created != 0 || r.Refreshed != 0 || r.Bindings != 0 || r.Unchanged != olds {
				t.Errorf("second run = %+v: a finished backfill found work", r)
			}
			// What verify is for: a box copied into another row does not open, is counted, and a run of the backfill puts it right.
			if _, err := pool.Exec(ctx, `UPDATE integration_accounts SET settings_enc = (SELECT settings_enc FROM integration_accounts ORDER BY id LIMIT 1) WHERE id = (SELECT id FROM integration_accounts ORDER BY id DESC LIMIT 1)`); err != nil {
				t.Fatal(err)
			}
			reports, err = reseal.Run(ctx, ver)
			must(err)
			if bad := columnReport(reports, "integration_accounts.settings_enc"); bad.Unreadable != 1 {
				t.Errorf("verify missed a box sealed to another account: %+v", bad)
			}
			if r, err = reseal.BackfillAccounts(ctx, in); err != nil || r.Refreshed != 1 {
				t.Errorf("the backfill did not mend the box: %+v, %v", r, err)
			}
			if reports, err = reseal.Run(ctx, ver); err != nil || columnReport(reports, "integration_accounts.settings_enc").Unreadable != 0 {
				t.Errorf("verify after the mend: %+v, %v", reports, err)
			}
		})

		// Step 3: the shadow comparison.
		t.Run("step 3 shadow comparison", func(t *testing.T) {
			registry := payments.NewRegistry()
			stripe.Register(registry)
			shadow := &store.Shadow{Accounts: accounts, Log: log}
			plain := &handler.Resolver{Registry: registry, Accounts: accounts}
			shadowed := &handler.Resolver{Registry: registry, Accounts: accounts, Shadow: shadow}
			result := func(r string) int64 { return counted(t, "account_shadow_reads", attribute.String("result", r)) }
			equalBefore, otherBefore := result("equal"), result("differs")+result("missing")+result("error")

			// Results equal to the old release's on the same input: the same accounts resolve, to the same adapter and the same key, with the shadow on and off.
			named := 0
			for i, id := range ids {
				for _, key := range []string{"stripe", "stripe~eu"} {
					p1, err1 := plain.Named(ctx, id, key, "live")
					p2, err2 := shadowed.Named(ctx, id, key, "live")
					if err1 != nil || err2 != nil {
						t.Fatalf("tenant %d %s: old %v, shadowed %v", i, key, err1, err2)
					}
					r1, _ := plain.Account(p1)
					r2, _ := shadowed.Account(p2)
					if r1 != r2 || fmt.Sprintf("%T", p1) != fmt.Sprintf("%T", p2) {
						t.Errorf("tenant %d %s: %+v against %+v", i, key, r1, r2)
					}
					named++
				}
				p1, ok1, _ := plain.Default(ctx, id, "live")
				p2, ok2, _ := shadowed.Default(ctx, id, "live")
				r1, _ := plain.Account(p1)
				r2, _ := shadowed.Account(p2)
				if !ok1 || ok1 != ok2 || r1 != r2 {
					t.Errorf("tenant %d default: %+v/%v against %+v/%v", i, r1, ok1, r2, ok2)
				}
				named++
				for _, key := range []string{"nilvera", "nilvera~tr"} {
					a, _ := store.EInvoiceAccounts{DB: pool, Box: box}.Get(ctx, id, "live", key)
					b, err := store.EInvoiceAccounts{DB: pool, Box: box, Shadow: shadow}.Get(ctx, id, "live", key)
					if err != nil || a.Key != b.Key || a.Mode != b.Mode || !bytes.Equal(a.Settings, b.Settings) || a.IsDefault != b.IsDefault {
						t.Errorf("e-invoice %s of %d: %+v against %+v, %v", key, i, a, b, err)
					}
					named++
				}
				// A bank transfer account is bound like any other.
				if got, found, err := accounts.BoundSettings(ctx, id, store.KindPayment, "banktransfer", "live"); err != nil || !found || !strings.Contains(string(got), "TR00") {
					t.Errorf("bank transfer account of %d in the new tables = %q, %v, %v", i, got, found, err)
				}
			}
			// stripe + stripe~eu + Default(stripe) for the shadowed resolver is 3 per tenant, e-invoice 2: every read of the shadowed side was compared and was equal.
			if got, want := result("equal")-equalBefore, int64(tenants*5); got != want {
				t.Errorf("%d equal comparisons, want %d (%d reads made)", got, want, named)
			}
			if got := result("differs") + result("missing") + result("error") - otherBefore; got != 0 {
				t.Errorf("%d comparisons found a difference", got)
			}

			// An account replaced on the old side after the backfill, and one connected after it, are seen and counted; what is used stays the old row.
			must(accounts.Put(ctx, ids[0], "stripe~eu", "live", []byte(`{"api_key":"sk_live_replaced","webhook_secret":"whsec_0","base_url":"http://127.0.0.1:1"}`), false))
			must(accounts.Put(ctx, ids[0], "stripe~uk", "live", []byte(`{"api_key":"sk_live_new","webhook_secret":"whsec_0","base_url":"http://127.0.0.1:1"}`), false))
			differs, missing := result("differs"), result("missing")
			for _, key := range []string{"stripe~eu", "stripe~uk"} {
				if _, err := shadowed.Named(ctx, ids[0], key, "live"); err != nil {
					t.Fatalf("%s: %v (the shadow must not change what is returned)", key, err)
				}
			}
			if result("differs")-differs != 1 || result("missing")-missing != 1 {
				t.Errorf("a replaced and a new account were not both seen: differs +%d, missing +%d", result("differs")-differs, result("missing")-missing)
			}
			if r, err := reseal.BackfillAccounts(ctx, in); err != nil || r.Created != 1 || r.Refreshed != 1 || r.Unchanged != olds-1 {
				t.Errorf("the backfill after the drift = %+v, %v", r, err)
			}
			equal := result("equal")
			for _, key := range []string{"stripe~eu", "stripe~uk"} {
				must(func() error { _, err := shadowed.Named(ctx, ids[0], key, "live"); return err }())
			}
			if result("equal")-equal != 2 {
				t.Errorf("the two accounts are still not equal after the backfill")
			}

			// The old webhook URL is still accepted, and an unknown account is not: a notification signed with the account's secret is taken, a wrong signature is refused.
			// (A notification on /webhooks/a/{account_id} is not built: the route comes with the step that mirrors the writes.)
			api := httptest.NewServer((&webhook.Server{Registry: registry, Resolver: shadowed, Accounts: accounts, Events: store.Events{DB: pool}, Attempts: store.Attempts{DB: pool},
				Ledger: store.Ledger{DB: pool}, Subscriptions: store.Subscriptions{DB: pool}, Log: log, Token: "internal"}).Handler())
			defer api.Close()
			post := func(tenant uuid.UUID, secret string) int {
				body := `{"id":"evt_` + uuid.NewString() + `","type":"customer.created","created":1790000000,"data":{"object":{"id":"cus_1"}}}`
				req, _ := http.NewRequest("POST", api.URL+"/webhooks/stripe/"+tenant.String()+"/live", strings.NewReader(body))
				req.Header.Set("Stripe-Signature", stripe.SignatureHeader(secret, time.Now(), []byte(body)))
				resp, err := http.DefaultClient.Do(req)
				must(err)
				resp.Body.Close()
				return resp.StatusCode
			}
			if code := post(ids[1], "whsec_1"); code >= 300 {
				t.Errorf("a signed notification on the old URL = %d", code)
			}
			if code := post(ids[1], "whsec_wrong"); code < 400 {
				t.Errorf("a notification with the wrong secret = %d, want a refusal", code)
			}
			if code := post(uuid.New(), "whsec_1"); code < 400 {
				t.Errorf("a notification for an account that does not exist = %d, want a refusal", code)
			}
			if after := truthOf(ctx, t, pool, []string{"payment_ledger", "payment_attempts", "provider_objects", "provider_subscriptions"}); after.sum["payment_ledger"] != before.sum["payment_ledger"] {
				t.Errorf("the ledger changed during step 3")
			}
		})
		// The ledger's totals per tenant, currency and kind are what they were.
		if got := ledgerTotals(ctx, t, pool); got != before.totals {
			t.Errorf("ledger totals changed: %s against %s", got, before.totals)
		}
	})

	t.Run("tenants in a cell", func(t *testing.T) {
		w := openWorld(t, testkit.StartPostgres(t), true, "c1")
		accounts := store.Accounts{DB: w.DB, Box: box}
		einvoices := store.EInvoiceAccounts{DB: w.DB, Box: box}
		var ids []uuid.UUID
		for i, cell := range []string{"", "c1", "c1"} {
			id := uuid.New()
			w.Dir.assign(id, cell)
			ids = append(ids, id)
			for _, key := range []string{"stripe", "stripe~eu", "banktransfer"} {
				if err := accounts.Put(ctx, id, key, "live", []byte(fmt.Sprintf(`{"key":"%s%d"}`, key, i)), key == "stripe"); err != nil {
					t.Fatal(err)
				}
			}
			if err := einvoices.Put(ctx, id, "live", "nilvera~tr", "production", []byte(`{"k":"nv"}`), true, store.Audit{}); err != nil {
				t.Fatal(err)
			}
		}
		in := reseal.Input{Box: box, Columns: reseal.Payments, Pooled: w.Pooled, Cells: w.pools, Log: log}
		r, err := reseal.BackfillAccounts(ctx, in)
		if err != nil || r.Old != 12 || r.Created != 12 || r.Bindings != 12 {
			t.Fatalf("backfill over the pooled database and a cell = %+v, %v", r, err)
		}
		ver := in
		ver.Verify = true
		reports, err := reseal.Run(ctx, ver)
		if err != nil {
			t.Fatal(err)
		}
		if rp := columnReport(reports, "integration_accounts.settings_enc"); rp.Boxes != 12 || rp.Unreadable != 0 {
			t.Errorf("verify over the pooled database and a cell = %+v", rp)
		}
		shadow := &store.Shadow{Accounts: accounts, Log: log}
		for _, id := range ids {
			for _, key := range []string{"stripe", "stripe~eu", "banktransfer"} {
				old, _, err := accounts.Settings(ctx, id, key, "live")
				if err != nil {
					t.Fatal(err)
				}
				if got := shadow.Compare(ctx, id, store.KindPayment, key, "live", old); got != "equal" {
					t.Errorf("tenant %s %s: %s", id, key, got)
				}
			}
			if n, err := w.CountAll(ctx, `SELECT count(*) FROM integration_bindings WHERE tenant_id = $1`, id); err != nil || n != 4 {
				t.Errorf("tenant %s has %d bindings, %v", id, n, err)
			}
		}
	})
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

func count(ctx context.Context, t *testing.T, p *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := p.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func columnReport(reports []reseal.Report, column string) reseal.Report {
	for _, r := range reports {
		if r.Column == column {
			return r
		}
	}
	return reseal.Report{}
}

// truth is "record the truth" of the runbook: the row count and a checksum of every row (sorted by its own hash, so the order does not matter) of each table, the
// shape of the tables (columns and indexes), and the ledger's totals.
type truth struct {
	rows   map[string]int64
	sum    map[string]string
	shape  string
	totals string
}

func truthOf(ctx context.Context, t *testing.T, p *pgxpool.Pool, tables []string) truth {
	t.Helper()
	tr := truth{rows: map[string]int64{}, sum: map[string]string{}}
	for _, tb := range tables {
		var n int64
		var s string
		//nolint:gosec // tb comes from the list above
		if err := p.QueryRow(ctx, `SELECT count(*), coalesce(md5(string_agg(h, '' ORDER BY h)), '') FROM (SELECT md5(x::text) AS h FROM `+tb+` x) s`).Scan(&n, &s); err != nil {
			t.Fatalf("truth of %s: %v", tb, err)
		}
		tr.rows[tb], tr.sum[tb] = n, s
	}
	if err := p.QueryRow(ctx, `SELECT (SELECT md5(string_agg(column_name || data_type || is_nullable || coalesce(column_default, ''), ',' ORDER BY table_name, ordinal_position))
			FROM information_schema.columns WHERE table_schema = 'public' AND table_name = ANY($1))
		 || (SELECT md5(string_agg(indexdef, ',' ORDER BY indexname)) FROM pg_indexes WHERE schemaname = 'public' AND tablename = ANY($1))`, tables).Scan(&tr.shape); err != nil {
		t.Fatal(err)
	}
	tr.totals = ledgerTotals(ctx, t, p)
	return tr
}

func ledgerTotals(ctx context.Context, t *testing.T, p *pgxpool.Pool) string {
	t.Helper()
	var s string
	if err := p.QueryRow(ctx, `SELECT coalesce(md5(string_agg(format('%s|%s|%s|%s|%s', tenant_id, currency, kind, total, n), ',' ORDER BY tenant_id, currency, kind)), '')
		FROM (SELECT tenant_id, currency, kind, sum(amount_minor) AS total, count(*) AS n FROM payment_ledger GROUP BY 1, 2, 3) s`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (a truth) equal(b truth) bool {
	if a.shape != b.shape || a.totals != b.totals {
		return false
	}
	for k, v := range b.rows {
		if a.rows[k] != v || a.sum[k] != b.sum[k] {
			return false
		}
	}
	return true
}

func (a truth) diff(b truth) string {
	var out []string
	if a.shape != b.shape {
		out = append(out, "the columns or indexes of an old table")
	}
	if a.totals != b.totals {
		out = append(out, "the ledger totals")
	}
	for k, v := range b.rows {
		if a.rows[k] != v || a.sum[k] != b.sum[k] {
			out = append(out, fmt.Sprintf("%s: %d rows %s against %d rows %s", k, a.rows[k], a.sum[k], v, b.sum[k]))
		}
	}
	return strings.Join(out, "\n")
}
