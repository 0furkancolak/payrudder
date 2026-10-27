package pgxx

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/tenantdir"
)

func TestCellSettings(t *testing.T) {
	if (CellSettings{}).Enabled() || (CellSettings{Cells: "  "}).Enabled() {
		t.Fatal("empty settings are enabled")
	}
	if cells, err := (CellSettings{}).Parse(); err != nil || cells != nil {
		t.Fatalf("unset = %v %v, want no cells", cells, err)
	}
	on := CellSettings{Cells: `{"c1":{"app":"postgres://a","system":"postgres://s","migrator":"postgres://m"},"eu-2":{"app":"x","system":"y"}}`}
	cells, err := on.Parse()
	if err != nil || len(cells) != 2 || cells["c1"].Migrator != "postgres://m" || cells["eu-2"].App != "x" {
		t.Fatalf("parsed = %+v %v", cells, err)
	}
	for _, bad := range []string{
		`{`,                                     // not JSON
		`[]`,                                    // not an object
		`{"C1":{"app":"x","system":"y"}}`,       // upper case
		`{"-c":{"app":"x","system":"y"}}`,       // starts with a hyphen
		`{"c1":{"app":"x"}}`,                    // no system DSN
		`{"c1":{"system":"y"}}`,                 // no app DSN
		`{"c1":{"app":"x","system":"y","z":1}}`, // an unknown field is a typo
	} {
		if err := (CellSettings{Cells: bad}).CheckCells(); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestSearchPathIsTheTenantSchemaThenShared(t *testing.T) {
	id := uuid.MustParse("0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
	if got, want := SearchPath(tenantdir.Schema(id)), `"t_0190a1b2c3d47e5f8a9b0c1d2e3f4a5b", shared`; got != want {
		t.Fatalf("SearchPath = %s, want %s", got, want)
	}
}

func TestRouterNeedsAPooledPoolAndADirectoryForCells(t *testing.T) {
	if _, err := NewRouter(nil, nil, nil); err == nil {
		t.Fatal("a router without a pooled pool was accepted")
	}
}

func TestShapeNormalisationHidesTheSchemaName(t *testing.T) {
	schema := "t_0190a1b2c3d47e5f8a9b0c1d2e3f4a5b"
	for _, pair := range [][2]string{
		{`index outbox outbox_pkey CREATE UNIQUE INDEX outbox_pkey ON public.outbox USING btree (id)`, `index outbox outbox_pkey CREATE UNIQUE INDEX outbox_pkey ON ` + schema + `.outbox USING btree (id)`},
		{`function f() config=[search_path=public]`, `function f() config=[search_path="` + schema + `"]`},
		{`function f() config=[search_path=public]`, `function f() config=[search_path=` + schema + `]`},
		{`constraint t fk FOREIGN KEY (a) REFERENCES public.u(id)`, `constraint t fk FOREIGN KEY (a) REFERENCES "` + schema + `".u(id)`},
	} {
		if a, b := norm(pair[0], "public"), norm(pair[1], schema); a != b {
			t.Errorf("not equal after norm:\n %s\n %s", a, b)
		}
	}
}

func TestDiffListsWhatIsOnlyOnOneSide(t *testing.T) {
	a := []string{"a", "b", "c"}
	b := []string{"b", "c", "d"}
	if got := Diff(a, b); !slices.Equal(got, []string{"- a", "+ d"}) {
		t.Fatalf("Diff = %v", got)
	}
	if got := Diff(a, a); len(got) != 0 {
		t.Fatalf("equal shapes differ: %v", got)
	}
	if got := Diff(nil, b); len(got) != 3 || got[0] != "+ b" {
		t.Fatalf("Diff(nil, b) = %v", got)
	}
}

func TestMoveRefusesWhatItCannotDo(t *testing.T) {
	good := Endpoint{Cell: "c1", MigratorDSN: "postgres://x"}
	pooled := Endpoint{MigratorDSN: "postgres://y"}
	ts := TenantSchemas{Migrations: nil}
	id := uuid.New()
	for name, m := range map[string]Move{
		"no tenant":           {From: good, To: pooled, Schemas: ts},
		"pooled to pooled":    {TenantID: id, From: pooled, To: Endpoint{MigratorDSN: "postgres://z"}, Schemas: ts},
		"to where it is":      {TenantID: id, From: good, To: good, Schemas: ts},
		"no DSN":              {TenantID: id, From: good, To: Endpoint{Cell: "c2"}, Schemas: ts},
		"no tenant migration": {TenantID: id, From: good, To: Endpoint{Cell: "c2", MigratorDSN: "postgres://z"}, Schemas: ts},
	} {
		if err := m.check(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
