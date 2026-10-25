package ubl

import (
	"strings"

	"github.com/0furkancolak/payrudder/pkg/money"
)

// writer is a tiny deterministic XML writer: two-space indentation, attributes in the order given, text escaped.
type writer struct {
	b     strings.Builder
	depth int
}

func (w *writer) indent() {
	for i := 0; i < w.depth; i++ {
		w.b.WriteString("  ")
	}
}

func (w *writer) attrs(kv []string) {
	for i := 0; i+1 < len(kv); i += 2 {
		w.b.WriteByte(' ')
		w.b.WriteString(kv[i])
		w.b.WriteString(`="`)
		w.b.WriteString(escape(kv[i+1]))
		w.b.WriteByte('"')
	}
}

// open writes <tag attrs> and indents.
func (w *writer) open(tag string, kv ...string) {
	w.indent()
	w.b.WriteByte('<')
	w.b.WriteString(tag)
	w.attrs(kv)
	w.b.WriteString(">\n")
	w.depth++
}

func (w *writer) close(tag string) {
	w.depth--
	w.indent()
	w.b.WriteString("</" + tag + ">\n")
}

// el writes <tag attrs>text</tag> on one line.
func (w *writer) el(tag, text string, kv ...string) {
	w.indent()
	w.b.WriteByte('<')
	w.b.WriteString(tag)
	w.attrs(kv)
	w.b.WriteByte('>')
	w.b.WriteString(escape(text))
	w.b.WriteString("</" + tag + ">\n")
}

// empty writes <tag/>.
func (w *writer) empty(tag string) {
	w.indent()
	w.b.WriteString("<" + tag + "/>\n")
}

// escape escapes the five XML specials and drops characters XML 1.0 forbids. Non-ASCII text is kept as UTF-8.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\'':
			b.WriteString("&apos;")
		case r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r <= 0xD7FF) || (r >= 0xE000 && r <= 0xFFFD) || r >= 0x10000:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// formatBps renders basis points as a percentage with two decimals: 2000 is "20.00", 750 is "7.50".
func formatBps(bps int) string { return money.FormatBps(int64(bps), false) }
