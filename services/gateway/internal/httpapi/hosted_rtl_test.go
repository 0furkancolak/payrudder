package httpapi

import (
	"regexp"
	"strings"
	"testing"
)

var (
	physicalProp = regexp.MustCompile(`(?:^|[;{\s])((?:margin|padding|border)-(?:left|right)(?:-[a-z]+)?|border-(?:top|bottom)-(?:left|right)-radius|left|right)\s*:`)
	physicalVal  = regexp.MustCompile(`(?:text-align|float|clear)\s*:\s*(?:left|right)`)
	shorthand    = regexp.MustCompile(`(?:^|[;{\s])(margin|padding|border-width|border-radius|inset)\s*:\s*([^;}]+)`)
)

// splitTop splits a CSS value on spaces that are not inside parentheses (calc(var(--radius) - 6px) is one value).
func splitTop(v string) []string {
	var out []string
	depth, start := 0, 0
	for i, c := range v + " " {
		switch {
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ' ' && depth == 0:
			if i > start {
				out = append(out, v[start:i])
			}
			start = i + 1
		}
	}
	return out
}

// Arabic pages read right to left on the same stylesheet: it says start and end (logical properties), never left and right, and a four-value shorthand puts the same
// value on both sides. A page whose margin-right is wrong in Arabic would otherwise only be found by a person looking at it.
func TestHostedCSSHasNoPhysicalDirections(t *testing.T) {
	for _, m := range physicalProp.FindAllStringSubmatch(pageCSS, -1) {
		t.Errorf("the stylesheet sets %q: use the logical property (margin-inline-start, padding-inline-end, inset-inline-start…)", m[1])
	}
	for _, m := range physicalVal.FindAllString(pageCSS, -1) {
		t.Errorf("the stylesheet says %q: use start or end", m)
	}
	for _, m := range shorthand.FindAllStringSubmatch(pageCSS, -1) {
		if v := splitTop(strings.TrimSpace(m[2])); len(v) == 4 && v[1] != v[3] {
			t.Errorf("%s: %s gives the left and the right side different values: use margin-inline / padding-inline", m[1], m[2])
		}
	}
}
