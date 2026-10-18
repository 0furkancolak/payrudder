package policy

import (
	"strings"
	"testing"

	assetsv1 "github.com/0furkancolak/quotify/gen/go/quotify/assets/v1"
)

func TestPartSizeRule(t *testing.T) {
	tests := []struct {
		size, wantPart int64
		wantCount      int32
	}{
		{1024, 8 * MiB, 1},
		{0, 8 * MiB, 1},
		{20 * MiB, 8 * MiB, 3},
		{200 * MiB, 8 * MiB, 25},
		{5 * GiB, 8 * MiB, 640},
		{100 * GiB, 11 * MiB, 9310}, // beyond v1's cap, but the rule keeps the part count under 10 000
	}
	for _, tc := range tests {
		ps := PartSize(tc.size)
		n := PartCount(tc.size, ps)
		if tc.size <= MaxFileSize && (ps != tc.wantPart || n != tc.wantCount) {
			t.Errorf("size %d: part size %d count %d, want %d and %d", tc.size, ps, n, tc.wantPart, tc.wantCount)
		}
		if n > MaxParts || ps < MinPartSize {
			t.Errorf("size %d breaks S3's limits: part size %d, %d parts", tc.size, ps, n)
		}
	}
}

func TestCheck(t *testing.T) {
	p := New(DefaultGeneralTypes)
	if ct, v := p.Check(assetsv1.Purpose_PURPOSE_INVOICE_LOGO, "IMAGE/PNG; charset=binary", 1*MiB); v != nil || ct != "image/png" {
		t.Fatalf("logo png: %q %v", ct, v)
	}
	for name, tc := range map[string]struct {
		purpose assetsv1.Purpose
		ct      string
		size    int64
		code    string
	}{
		"svg logo":        {assetsv1.Purpose_PURPOSE_INVOICE_LOGO, "image/svg+xml", 1, UnsupportedContentType},
		"big logo":        {assetsv1.Purpose_PURPOSE_INVOICE_LOGO, "image/png", 3 * MiB, FileTooLarge},
		"attachment type": {assetsv1.Purpose_PURPOSE_INVOICE_ATTACHMENT, "image/png", 1, UnsupportedContentType},
		"general exe":     {assetsv1.Purpose_PURPOSE_GENERAL, "application/x-msdownload", 1, UnsupportedContentType},
		"general huge":    {assetsv1.Purpose_PURPOSE_GENERAL, "application/zip", 6 * GiB, FileTooLarge},
		"no purpose":      {assetsv1.Purpose_PURPOSE_UNSPECIFIED, "image/png", 1, UnsupportedContentType},
	} {
		if _, v := p.Check(tc.purpose, tc.ct, tc.size); v == nil || v.Code != tc.code {
			t.Errorf("%s: %v, want %s", name, v, tc.code)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	good := map[string]string{"logo.png": "logo.png", "  my file.pdf ": "my file.pdf", "Café.pdf": "Café.pdf", "şükrü.csv": "şükrü.csv"}
	for in, want := range good {
		if got, err := SanitizeFilename(in); err != nil || got != want {
			t.Errorf("SanitizeFilename(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "..", ".", "a/b.png", "a\\b.png", "x\x00.png", "tab\there.png", "bell\a.png"} {
		if _, err := SanitizeFilename(bad); err == nil {
			t.Errorf("SanitizeFilename(%q) must fail", bad)
		}
	}
	long, err := SanitizeFilename(strings.Repeat("é", 200) + ".pdf")
	if err != nil || len(long) > 255 {
		t.Errorf("long name: %d bytes, %v", len(long), err)
	}
}

func TestSniff(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 16))
	pdf := []byte("%PDF-1.7\n" + strings.Repeat(" ", 16))
	if !Sniff("image/png", png) || Sniff("image/jpeg", png) || !Sniff("application/pdf", pdf) || Sniff("image/png", pdf) {
		t.Fatal("sniffing disagrees with the real content types")
	}
	if !Sniff("text/csv", []byte("a,b\n1,2\n")) || Sniff("text/csv", png) {
		t.Fatal("text formats")
	}
	if !Sniff("application/zip", []byte("PK\x03\x04"+strings.Repeat("\x00", 20))) {
		t.Fatal("zip")
	}
}
