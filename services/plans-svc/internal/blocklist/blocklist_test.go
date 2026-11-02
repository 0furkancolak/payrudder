package blocklist

import "testing"

func TestNormalize(t *testing.T) {
	for _, tc := range []struct{ kind, in, want string }{
		{"email", "  Ada@Example.COM ", "ada@example.com"},
		{"tax_id", " tr 1234-5678 90 ", "TR1234567890"},
		{"external_id", " Cus_A ", "Cus_A"}, // an external id is exact
	} {
		if got, err := Normalize(tc.kind, tc.in); err != nil || got != tc.want {
			t.Errorf("Normalize(%q, %q) = %q, %v; want %q", tc.kind, tc.in, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ kind, in string }{
		{"email", "not an address"}, {"email", "a b@example.com"}, {"email", ""}, {"tax_id", " - "}, {"external_id", ""}, {"card", "4242"}, {"", "x"},
	} {
		if got, err := Normalize(tc.kind, tc.in); err == nil {
			t.Errorf("Normalize(%q, %q) = %q; want a refusal", tc.kind, tc.in, got)
		}
	}
	long := make([]rune, maxValue+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := Normalize("external_id", string(long)); err == nil {
		t.Error("a value over 320 characters must be refused")
	}
}

func TestSubjectNormalisesAsAnEntryDoes(t *testing.T) {
	email, tax, ext := Subject{Email: " Ada@Example.com", TaxID: "tr 12-34", ExternalID: " cus_1 "}.normalized()
	if email != "ada@example.com" || tax != "TR1234" || ext != "cus_1" {
		t.Errorf("got %q %q %q", email, tax, ext)
	}
}
