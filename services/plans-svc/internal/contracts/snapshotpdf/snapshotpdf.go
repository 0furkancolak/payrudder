// Package snapshotpdf draws the stored snapshot page of an accepted agreement (domain.RenderSnapshot) as a PDF: the copy a customer can keep and print. The page stays the
// evidence (it is what was hashed); the PDF is read from that exact page, text only, in the platform's one PDF font (pkg/pdffont, Noto Sans), and ends with the sha256 of
// the page it was drawn from. The same page and time give the same bytes.
//
// Noto Sans has no Arabic, Hebrew or CJK glyphs and the drawing is left to right, so a page with a character the font cannot draw (an Arabic snapshot, a customer named
// in Chinese) is not drawn with question marks: Render answers ErrUnsupported and the HTML page remains the only copy.
package snapshotpdf

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"golang.org/x/net/html"

	"github.com/0furkancolak/payrudder/pkg/pdffont"
)

// ErrUnsupported means the page has a character the font cannot draw (or is not a snapshot page at all).
var ErrUnsupported = errors.New("snapshotpdf: the page cannot be drawn in the embedded font")

const (
	family = "NotoSans"
	margin = 18.0
)

// block is one thing to draw, in page order.
type block struct {
	kind  string // "h1", "h2", "h3", "field", "para", "item", "ack", "small"
	label string // a field's name
	text  string
	mark  string // an item's bullet or number
}

// Render draws the page. created is the PDF's creation date (the time of the acceptance, so the bytes do not depend on the clock).
func Render(page string, created time.Time) ([]byte, error) {
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	var title string
	var blocks []block
	var walk func(n *html.Node, ctx string)
	walk = func(n *html.Node, ctx string) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.Data {
			case "style", "script":
			case "title":
				title = flat(c)
			case "h1":
				blocks = append(blocks, block{kind: "h1", text: flat(c)})
			case "h2":
				blocks = append(blocks, block{kind: "h2", text: flat(c)})
			case "h3", "h4", "h5", "h6":
				blocks = append(blocks, block{kind: "h3", text: flat(c)})
			case "dl":
				var label string
				for d := c.FirstChild; d != nil; d = d.NextSibling {
					switch {
					case d.Type == html.ElementNode && d.Data == "dt":
						label = flat(d)
					case d.Type == html.ElementNode && d.Data == "dd":
						blocks = append(blocks, block{kind: "field", label: label, text: flat(d)})
					}
				}
			case "ul", "ol":
				n := 0
				for li := c.FirstChild; li != nil; li = li.NextSibling {
					if li.Type == html.ElementNode && li.Data == "li" {
						n++
						mark := "-"
						if c.Data == "ol" {
							mark = fmt.Sprintf("%d.", n)
						}
						blocks = append(blocks, block{kind: "item", mark: mark, text: flat(li)})
					}
				}
			case "p":
				kind := "para"
				switch {
				case hasClass(c, "ack"):
					kind = "ack"
				case onlyChild(c, "code") || onlyChild(c, "small"):
					kind = "small"
				}
				blocks = append(blocks, block{kind: kind, text: flat(c)})
			default: // html, body, div
				walk(c, ctx)
			}
		}
	}
	walk(doc, "")
	if len(blocks) == 0 {
		return nil, ErrUnsupported
	}
	var all strings.Builder
	all.WriteString(title)
	for _, b := range blocks {
		all.WriteString(b.label + b.text + b.mark)
	}
	if !pdffont.Covers(all.String()) {
		return nil, ErrUnsupported
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(false)
	pdf.SetCatalogSort(true)
	pdf.SetCreator("Payrudder", true)
	pdf.SetCreationDate(created.UTC())
	pdf.SetModificationDate(created.UTC()) // fpdf stamps time.Now() otherwise: the same page must give the same bytes
	pdf.SetTitle(title, true)
	pdf.AddUTF8FontFromBytes(family, "", pdffont.Regular)
	pdf.AddUTF8FontFromBytes(family, "B", pdffont.Bold)
	pdf.SetMargins(margin, margin, margin)
	pdf.SetAutoPageBreak(true, margin+4)
	pdf.AliasNbPages("{nb}")
	pdf.SetFooterFunc(func() {
		pdf.SetY(-margin)
		pdf.SetFont(family, "", 8)
		pdf.SetTextColor(100, 100, 100)
		pdf.CellFormat(0, 5, fmt.Sprintf("%d / {nb}", pdf.PageNo()), "", 0, "C", false, 0, "")
	})
	pdf.AddPage()
	width := 210 - 2*margin
	set := func(style string, size float64, grey bool) {
		pdf.SetFont(family, style, size)
		if grey {
			pdf.SetTextColor(90, 90, 90)
		} else {
			pdf.SetTextColor(0, 0, 0)
		}
	}
	for _, b := range blocks {
		switch b.kind {
		case "h1":
			set("B", 18, false)
			pdf.MultiCell(width, 8, b.text, "", "L", false)
			pdf.Ln(2)
		case "h2":
			pdf.Ln(5)
			set("B", 13, false)
			pdf.MultiCell(width, 6.5, b.text, "", "L", false)
			pdf.Ln(1)
		case "h3":
			pdf.Ln(2)
			set("B", 11, false)
			pdf.MultiCell(width, 5.5, b.text, "", "L", false)
		case "field":
			set("B", 10.5, false)
			pdf.MultiCell(width, 5.5, b.label+": "+b.text, "", "L", false)
		case "para":
			set("", 10.5, false)
			pdf.MultiCell(width, 5.2, b.text, "", "L", false)
			pdf.Ln(1.5)
		case "ack":
			set("B", 10.5, false)
			pdf.MultiCell(width, 5.5, b.text, "", "L", false)
			pdf.Ln(1)
		case "item":
			set("", 10.5, false)
			x := pdf.GetX()
			y := pdf.GetY()
			pdf.CellFormat(7, 5.2, b.mark, "", 0, "R", false, 0, "")
			pdf.SetXY(x+9, y)
			pdf.MultiCell(width-9, 5.2, b.text, "", "L", false)
		case "small":
			set("", 8, true)
			pdf.MultiCell(width, 4, b.text, "", "L", false)
			pdf.Ln(1)
		}
	}
	sum := sha256.Sum256([]byte(page))
	pdf.Ln(4)
	set("", 8, true)
	pdf.MultiCell(width, 4, "sha256 of the stored page this PDF was drawn from: "+hex.EncodeToString(sum[:]), "", "L", false)
	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, fmt.Errorf("snapshotpdf: %w", err)
	}
	return out.Bytes(), nil
}

// flat is the text under a node, white space collapsed, the ballot box of the page written as [x] (the font has none).
func flat(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return strings.NewReplacer("☑", "[x]", " ", " ").Replace(strings.Join(strings.Fields(b.String()), " "))
}

func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

// onlyChild reports whether the element's one child element is tag (and nothing but white space is beside it).
func onlyChild(n *html.Node, tag string) bool {
	found := false
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == html.ElementNode && c.Data == tag:
			found = true
		case c.Type == html.TextNode && strings.TrimSpace(c.Data) == "":
		default:
			return false
		}
	}
	return found
}
