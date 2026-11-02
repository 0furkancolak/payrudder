//go:build integration

package itest

import (
	"bytes"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/secretbox/reseal"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// A rotation from key 1 to key 2 (docs/runbooks/secrets-key-rotation.md): the accounts a tenant of the pooled database and a tenant of a cell saved under key 1 are sealed
// again under key 2 by the job, with the associated data the store used, and open afterwards with a Box that holds key 2 only. A dry run changes nothing, a second run
// finds nothing, a box copied from another row is counted and left alone, and a row its owner saved again while the job ran keeps the owner's box.
func TestResealMovesEveryBoxToTheActiveKey(t *testing.T) {
	k1, k2 := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	old, _ := secretbox.New(k1)
	ring, _ := secretbox.NewRing(2, map[byte][]byte{1: k1, 2: k2})
	only2, _ := secretbox.NewRing(2, map[byte][]byte{2: k2})
	ctx := testkit.Ctx(t)

	// setup puts two payment accounts and two e-invoice accounts (one labelled) under key 1 for a tenant of the pooled database and one of the cell. The roles of a
	// service exist once per Postgres, so each scenario has its own.
	setup := func(t *testing.T) (w *world, tenants []uuid.UUID) {
		w = openWorld(t, testkit.StartPostgres(t), true, "c1")
		for _, cell := range []string{"", "c1"} {
			id := uuid.New()
			w.Dir.assign(id, cell)
			tenants = append(tenants, id)
			accounts, einvoices := store.Accounts{DB: w.DB, Box: old}, store.EInvoiceAccounts{DB: w.DB, Box: old}
			for _, key := range []string{"iyzico", "stripe~eu"} {
				if err := accounts.Put(ctx, id, key, "live", []byte(`{"key":"`+key+`"}`), key == "iyzico"); err != nil {
					t.Fatal(err)
				}
			}
			for _, key := range []string{"nilvera", "nilvera~tr"} {
				if err := einvoices.Put(ctx, id, "live", key, "production", []byte(`{"key":"`+key+`"}`), key == "nilvera", store.Audit{}); err != nil {
					t.Fatal(err)
				}
			}
		}
		return w, tenants
	}
	run := func(w *world, box *secretbox.Box, dry bool, cols []reseal.Column) map[string]reseal.Report {
		t.Helper()
		reports, err := reseal.Run(ctx, reseal.Input{Box: box, Columns: cols, Pooled: w.Pooled, Cells: w.pools, Batch: 1, DryRun: dry})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]reseal.Report{}
		for _, r := range reports {
			out[r.Column] = r
		}
		return out
	}
	// opensUnderKey2Only reads every account back with a Box that cannot open key 1.
	opensUnderKey2Only := func(t *testing.T, w *world, tenants []uuid.UUID) {
		t.Helper()
		accounts, einvoices := store.Accounts{DB: w.DB, Box: only2}, store.EInvoiceAccounts{DB: w.DB, Box: only2}
		for _, id := range tenants {
			for _, key := range []string{"iyzico", "stripe~eu"} {
				if got, _, err := accounts.Settings(ctx, id, key, "live"); err != nil || string(got) != `{"key":"`+key+`"}` {
					t.Errorf("payment account %s of %s = %q, %v", key, id, got, err)
				}
			}
			for _, key := range []string{"nilvera", "nilvera~tr"} {
				if got, err := einvoices.Get(ctx, id, "live", key); err != nil || string(got.Settings) != `{"key":"`+key+`"}` {
					t.Errorf("e-invoice account %s of %s = %q, %v", key, id, got.Settings, err)
				}
			}
		}
	}

	t.Run("seals again, once", func(t *testing.T) {
		w, tenants := setup(t)
		dry := run(w, ring, true, reseal.Payments)
		for _, col := range []string{"provider_accounts.settings_enc", "einvoice_accounts.settings_enc"} {
			if r := dry[col]; r != (reseal.Report{Column: col, Boxes: 4, Current: 0, Resealed: 4}) {
				t.Errorf("dry run %s = %+v", col, r)
			}
		}
		if _, _, err := (store.Accounts{DB: w.DB, Box: only2}).Settings(ctx, tenants[0], "iyzico", "live"); err == nil {
			t.Fatal("the dry run changed a box")
		}
		first := run(w, ring, false, reseal.Payments)
		for _, col := range []string{"provider_accounts.settings_enc", "einvoice_accounts.settings_enc"} {
			if r := first[col]; r != (reseal.Report{Column: col, Boxes: 4, Current: 4, Resealed: 4}) {
				t.Errorf("first run %s = %+v", col, r)
			}
		}
		opensUnderKey2Only(t, w, tenants)
		for col, r := range run(w, ring, false, reseal.Payments) {
			if r.Resealed != 0 || r.Boxes != 4 || r.Current != 4 {
				t.Errorf("second run %s = %+v: a finished job found work", col, r)
			}
		}
	})

	t.Run("a box that does not open is counted and left", func(t *testing.T) {
		w, tenants := setup(t)
		// What the associated data guards against: the box of one account in the row of another.
		stolen := tenants[0]
		if _, err := w.At(stolen).Exec(ctx, `UPDATE provider_accounts SET settings_enc = (SELECT settings_enc FROM provider_accounts WHERE provider = 'iyzico') WHERE provider = 'stripe~eu'`); err != nil {
			t.Fatal(err)
		}
		r := run(w, ring, false, reseal.Payments)["provider_accounts.settings_enc"]
		if r.Unreadable != 1 || r.Resealed != 3 || r.Boxes != 4 || r.Current != 3 {
			t.Errorf("report = %+v, want 3 sealed again and 1 left", r)
		}
		var first byte
		if err := w.At(stolen).QueryRow(ctx, `SELECT get_byte(settings_enc, 0) FROM provider_accounts WHERE provider = 'stripe~eu'`).Scan(&first); err != nil || first != 1 {
			t.Errorf("the box that did not open starts with %d, %v: it was changed", first, err)
		}
	})

	t.Run("a row its owner saved again meanwhile keeps the owner's box", func(t *testing.T) {
		w, tenants := setup(t)
		cols := slices.Clone(reseal.Payments[:1])
		aad, saved := cols[0].AAD, false
		cols[0].AAD = func(k []string) []byte {
			if !saved && k[1] == "iyzico" && k[0] == tenants[0].String() {
				saved = true // between the job's read and its update
				if err := (store.Accounts{DB: w.DB, Box: old}).Put(ctx, tenants[0], "iyzico", "live", []byte(`{"key":"saved meanwhile"}`), true); err != nil {
					t.Error(err)
				}
			}
			return aad(k)
		}
		r := run(w, ring, false, cols)["provider_accounts.settings_enc"]
		if r.Changed != 1 || r.Resealed != 3 {
			t.Errorf("report = %+v, want 1 changed and 3 sealed again", r)
		}
		got, _, err := (store.Accounts{DB: w.DB, Box: ring}).Settings(ctx, tenants[0], "iyzico", "live")
		if err != nil || string(got) != `{"key":"saved meanwhile"}` {
			t.Errorf("the account = %q, %v: the owner's save was lost", got, err)
		}
		if r := run(w, ring, false, cols)["provider_accounts.settings_enc"]; r.Resealed != 1 || r.Current != 4 {
			t.Errorf("the run after = %+v, want the saved row sealed again", r)
		}
	})
}
