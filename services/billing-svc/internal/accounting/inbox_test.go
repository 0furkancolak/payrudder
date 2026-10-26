package accounting

import (
	"strings"
	"testing"
)

func TestAttachmentTypeJudgesTheBytes(t *testing.T) {
	cases := map[string]string{"%PDF-1.7 x": "application/pdf", "\x89PNG\r\n\x1a\nxx": "image/png", "\xff\xd8\xff\xe0": "image/jpeg", "MZ\x90 exe": "", "<html>": "", "": ""}
	for in, want := range cases {
		if got := AttachmentType([]byte(in)); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestCleanNameKeepsTheLastElementOnly(t *testing.T) {
	for in, want := range map[string]string{"inv.pdf": "inv.pdf", "../../etc/passwd": "passwd", `C:\docs\fatura.pdf`: "fatura.pdf", "a\x00b\n.pdf": "ab.pdf", "": "x.pdf", "/": "x.pdf"} {
		if got := cleanName(in, "x.pdf"); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if n := len([]rune(cleanName(strings.Repeat("é", 500)+".pdf", "x.pdf"))); n > maxName {
		t.Errorf("a name of %d characters is kept", n)
	}
}

func TestNewTokenIsLongAndDifferent(t *testing.T) {
	a, b := newToken(), newToken()
	if len(a) < 16 || len(a) > 64 || a == b || a != strings.ToLower(a) {
		t.Errorf("tokens %q %q", a, b)
	}
}
