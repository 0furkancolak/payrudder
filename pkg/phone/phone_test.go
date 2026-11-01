package phone

import "testing"

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"":                    "",
		"+905321234567":       "+905321234567",
		"+90 532 123 45 67":   "+905321234567",
		"+90 (532) 123-45-67": "+905321234567",
		"00905321234567":      "+905321234567",
		"00 49 30 123456":     "+4930123456",
		"+1.415.555.2671":     "+14155552671",
		"+44 7911 123456":     "+447911123456",
		"+1234567":            "+1234567",
		"+123456789012345":    "+123456789012345",
	} {
		if got, ok := Normalize(in); !ok || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestNormalizeRefuses(t *testing.T) {
	for _, in := range []string{
		"05321234567",       // national form: no region to read it in
		"5321234567",        // no prefix at all
		"+0905321234567",    // the country code does not start with 0
		"+12345",            // too short
		"+1234567890123456", // 16 digits
		"+90 532 abc 45 67", // letters
		"+90532123456x",     // letters
		"+",                 //
		"++905321234567",    // two plus signs
		"+90 532 123 45 67 ext 2",
	} {
		if got, ok := Normalize(in); ok {
			t.Errorf("Normalize(%q) = %q, want it refused", in, got)
		}
	}
}
