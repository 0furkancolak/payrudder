package pdffont

import "testing"

func TestTheFontDrawsLatinAndNotArabicOrCJK(t *testing.T) {
	for _, s := range []string{"Plain ASCII", "Şükrü Çağlayan İstanbul Iğdır", "Müller ß, Façade, señor, Ελληνικά, Кириллица"} {
		if !Covers(s) || Printable(s) != s {
			t.Errorf("%q should be drawn as it is", s)
		}
	}
	for _, s := range []string{"نص عربي", "李雷", "emoji 😀"} {
		if Covers(s) {
			t.Errorf("%q has characters the font cannot draw", s)
		}
	}
	if got := Printable("a\x00b\nc 李"); got != "a b c ?" {
		t.Errorf("Printable = %q", got)
	}
	if !Covers("") || !Covers("  \n\t") {
		t.Error("nothing to draw is covered")
	}
}
