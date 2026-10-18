package chmigrate

import (
	"reflect"
	"testing"
)

func TestStatements(t *testing.T) {
	sql := `-- a comment
CREATE TABLE a
(
    x Int64
)
ENGINE = Memory;

-- another
CREATE MATERIALIZED VIEW v AS SELECT 1;
`
	got := Statements(sql)
	want := []string{"CREATE TABLE a\n(\n    x Int64\n)\nENGINE = Memory", "CREATE MATERIALIZED VIEW v AS SELECT 1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("statements = %q, want %q", got, want)
	}
}
