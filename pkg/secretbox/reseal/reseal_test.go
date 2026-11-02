package reseal

import "testing"

// The statements are built from table, column and key names (identifiers cannot be parameters), so the registry may hold nothing but plain names: the registries
// that ship are accepted, and a name that could carry SQL is refused before a statement is built.
func TestTheRegistryHoldsOnlyPlainIdentifiers(t *testing.T) {
	for name, cols := range map[string][]Column{"payments": Payments, "plans": Plans} {
		if err := checkColumns(cols); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	aad := func([]string) []byte { return nil }
	ok := Column{Table: "t", Column: "c_enc", Key: []KeyColumn{{"id", "uuid"}}, AAD: aad}
	for name, c := range map[string]Column{
		"a table with a statement in it":  {Table: "t; DROP TABLE t", Column: ok.Column, Key: ok.Key, AAD: aad},
		"a schema-qualified table":        {Table: "other.t", Column: ok.Column, Key: ok.Key, AAD: aad},
		"a column in capitals":            {Table: ok.Table, Column: "C", Key: ok.Key, AAD: aad},
		"a key name with a comma":         {Table: ok.Table, Column: ok.Column, Key: []KeyColumn{{"id, secret", "uuid"}}, AAD: aad},
		"a key type that is not a type":   {Table: ok.Table, Column: ok.Column, Key: []KeyColumn{{"id", "uuid) OR (1=1"}}, AAD: aad},
		"no key to find the row again by": {Table: ok.Table, Column: ok.Column, AAD: aad},
		"no associated data":              {Table: ok.Table, Column: ok.Column, Key: ok.Key},
	} {
		if err := checkColumns([]Column{c}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := checkColumns([]Column{ok}); err != nil {
		t.Errorf("a plain column was refused: %v", err)
	}
}
