package domain

import "testing"

func TestNoticeFloorIsTheCountrysRowThenAnyThenTheCodesOwn(t *testing.T) {
	r := NoticeRules{Default: 30, Floors: map[string]int{"DE": 60, NoticeAnyCountry: 21}}
	for country, want := range map[string]int{"DE": 60, "de": 60, " DE ": 60, "TR": 21, "": 21} {
		if got := r.Floor(country); got != want {
			t.Errorf("Floor(%q) = %d, want %d", country, got, want)
		}
	}
	// No row for any country, or a row below the code's floor, still gives 14: a table cannot lower it.
	if got := (NoticeRules{}).Floor("DE"); got != MinNoticeDays {
		t.Errorf("an empty table: %d", got)
	}
	if got := (NoticeRules{Floors: map[string]int{"DE": 3}}).Floor("DE"); got != MinNoticeDays {
		t.Errorf("a row of 3 days: %d", got)
	}
}

func TestValidateNoticeRules(t *testing.T) {
	ok := map[string]int{"TR": 30, "*": 14}
	if err := ValidateNoticeRules(30, ok, nil); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		def   int
		rules map[string]int
		notes map[string]string
	}{
		"default below 14":  {13, ok, nil},
		"default above 365": {366, ok, nil},
		"row below 14":      {30, map[string]int{"TR": 13}, nil},
		"row above 365":     {30, map[string]int{"TR": 366}, nil},
		"lower case":        {30, map[string]int{"tr": 30}, nil},
		"three letters":     {30, map[string]int{"TUR": 30}, nil},
		"long note":         {30, ok, map[string]string{"TR": string(make([]rune, 201))}},
	} {
		if ValidateNoticeRules(c.def, c.rules, c.notes) == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
