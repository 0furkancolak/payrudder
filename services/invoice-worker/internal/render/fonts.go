package render

import "github.com/0furkancolak/payrudder/pkg/pdffont"

// The font is the platform's one PDF font (pkg/pdffont): Noto Sans Regular and Bold, SIL Open Font License 1.1.
var (
	notoRegular = pdffont.Regular
	notoBold    = pdffont.Bold
)

// printable maps text to what the font can draw (control characters become a space, a rune without a glyph becomes '?').
func printable(s string) string { return pdffont.Printable(s) }
