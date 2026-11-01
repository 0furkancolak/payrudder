package render

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf16"
)

// pdfText is the text a PDF drawn by PDF() shows, one line per text operation, in drawing order. Pages are not compressed and every string is the UTF-16BE of the text
// (the embedded font is addressed by code point), so a few lines of parsing replace a PDF library; the same text comes out of `pdftotext` (checked by eye, see the PR).
func pdfText(t *testing.T, pdf []byte) string {
	t.Helper()
	var lines []string
	rest := pdf
	for {
		i := bytes.Index(rest, []byte(" Td ("))
		if i < 0 {
			break
		}
		rest = rest[i+len(" Td ("):]
		var raw []byte
		n := 0
		for ; n < len(rest) && rest[n] != ')'; n++ {
			if rest[n] == '\\' && n+1 < len(rest) {
				n++
				switch rest[n] {
				case 'r':
					raw = append(raw, '\r')
				default: // \\ \( \)
					raw = append(raw, rest[n])
				}
				continue
			}
			raw = append(raw, rest[n])
		}
		if n == len(rest) || len(raw)%2 != 0 {
			t.Fatalf("a text operation that is not UTF-16BE: %q", raw)
		}
		u := make([]uint16, len(raw)/2)
		for k := range u {
			u[k] = uint16(raw[2*k])<<8 | uint16(raw[2*k+1])
		}
		lines = append(lines, string(utf16.Decode(u)))
		rest = rest[n:]
	}
	return strings.Join(lines, "\n")
}

// info reads a string of the document information dictionary (/Title, /Keywords, /Author), written as UTF-16BE after a byte order mark.
func info(t *testing.T, pdf []byte, field string) string {
	t.Helper()
	i := bytes.Index(pdf, []byte("/"+field+" (\xfe\xff"))
	if i < 0 {
		t.Fatalf("no /%s in the PDF", field)
	}
	rest := pdf[i+len("/"+field+" (\xfe\xff"):]
	end := bytes.IndexByte(rest, ')')
	u := make([]uint16, end/2)
	for k := range u {
		u[k] = uint16(rest[2*k])<<8 | uint16(rest[2*k+1])
	}
	return string(utf16.Decode(u))
}
