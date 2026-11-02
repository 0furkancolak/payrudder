// Package pdffont is the one font the platform's PDFs are drawn in: Noto Sans Regular and Bold (SIL Open Font License 1.1, fonts/OFL.txt), embedded in the binary, with
// the lookup of which characters the font can draw. The invoice worker draws invoices with it and plans-svc draws the snapshots of accepted agreements.
package pdffont

import (
	_ "embed" // the fonts below
	"encoding/binary"
	"strings"
	"sync"
)

// Noto Sans covers Latin with its extensions (the Turkish ğ, ş, ı and İ, the German ß, the French and Spanish accents), Greek and Cyrillic, not Arabic, Hebrew or CJK. Both
// weights are embedded in the binary (about 1.2 MiB); a PDF carries only the glyphs it uses.
//
//go:embed fonts/NotoSans-Regular.ttf
var Regular []byte

//go:embed fonts/NotoSans-Bold.ttf
var Bold []byte

// cmapFormat4 returns the (3,1) Unicode BMP subtable of a TrueType font's cmap, or nil.
func cmapFormat4(font []byte) []byte {
	if len(font) < 12 {
		return nil
	}
	be := binary.BigEndian
	for i, n := 0, int(be.Uint16(font[4:])); i < n && 12+16*i+16 <= len(font); i++ {
		rec := font[12+16*i:]
		if string(rec[:4]) != "cmap" {
			continue
		}
		off := int(be.Uint32(rec[8:]))
		if off+4 > len(font) {
			return nil
		}
		cmap := font[off:]
		for j, m := 0, int(be.Uint16(cmap[2:])); j < m && 4+8*j+8 <= len(cmap); j++ {
			sub := cmap[4+8*j:]
			if be.Uint16(sub) == 3 && be.Uint16(sub[2:]) == 1 {
				if so := int(be.Uint32(sub[4:])); so+14 <= len(cmap) && be.Uint16(cmap[so:]) == 4 {
					return cmap[so:]
				}
			}
		}
	}
	return nil
}

// hasGlyph reports whether the font draws r (BMP only): the lookup of the cmap format 4 subtable.
func hasGlyph(sub []byte, r rune) bool {
	if sub == nil || r < 0 || r > 0xFFFF {
		return false
	}
	be := binary.BigEndian
	segs := int(be.Uint16(sub[6:])) / 2
	end, start, delta, rangeOff := 14, 16+2*segs, 16+4*segs, 16+6*segs
	if rangeOff+2*segs > len(sub) {
		return false
	}
	for i := 0; i < segs; i++ {
		if int(be.Uint16(sub[end+2*i:])) < int(r) {
			continue
		}
		if int(be.Uint16(sub[start+2*i:])) > int(r) {
			return false
		}
		ro := int(be.Uint16(sub[rangeOff+2*i:]))
		if ro == 0 {
			return (int(r)+int(be.Uint16(sub[delta+2*i:])))&0xFFFF != 0
		}
		p := rangeOff + 2*i + ro + 2*(int(r)-int(be.Uint16(sub[start+2*i:])))
		return p+2 <= len(sub) && be.Uint16(sub[p:]) != 0
	}
	return false
}

var (
	coverOnce sync.Once
	coverSub  []byte
)

// Printable maps text to what the embedded font can draw: control characters become a space and a rune the font has no glyph for (CJK, Arabic, emoji) becomes '?',
// as the English-only renderer did for everything above U+00FF.
func Printable(s string) string {
	coverOnce.Do(func() { coverSub = cmapFormat4(Regular) })
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7F:
			b.WriteRune(' ')
		case hasGlyph(coverSub, r):
			b.WriteRune(r)
		default:
			b.WriteRune('?')
		}
	}
	return b.String()
}

// Covers reports whether the font can draw every character of s (control characters and white space aside): text for which it cannot is better not drawn at all than
// drawn with question marks.
func Covers(s string) bool {
	coverOnce.Do(func() { coverSub = cmapFormat4(Regular) })
	for _, r := range s {
		if r < 0x20 || r == 0x7F || r == ' ' {
			continue
		}
		if !hasGlyph(coverSub, r) {
			return false
		}
	}
	return true
}
