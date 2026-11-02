package httpapi

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// WCAG 2.x AA on the colour tokens of the hosted pages, in both themes, computed from the CSS the pages ship (so a token that changes is checked as it changes).
// oklch is converted to sRGB with Björn Ottosson's matrices; a colour outside sRGB is clamped, as a browser without gamut mapping would.

type rgb struct{ r, g, b float64 } // linear sRGB, 0..1

var oklchRe = regexp.MustCompile(`oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*(?:/\s*([\d.]+)(%)?)?\)`)

func parseOKLCH(t *testing.T, s string) (c rgb, alpha float64) {
	t.Helper()
	m := oklchRe.FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("not an oklch colour: %q", s)
	}
	f := func(v string) float64 {
		x, err := strconv.ParseFloat(v, 64)
		if err != nil {
			t.Fatalf("bad number %q in %q", v, s)
		}
		return x
	}
	l, ch, h := f(m[1]), f(m[2]), f(m[3])*math.Pi/180
	alpha = 1
	if m[4] != "" {
		alpha = f(m[4])
		if m[5] == "%" {
			alpha /= 100
		}
	}
	a, b := ch*math.Cos(h), ch*math.Sin(h)
	lp, mp, sp := l+0.3963377774*a+0.2158037573*b, l-0.1055613458*a-0.0638541728*b, l-0.0894841775*a-1.2914855480*b
	l3, m3, s3 := lp*lp*lp, mp*mp*mp, sp*sp*sp
	clamp := func(v float64) float64 { return math.Min(1, math.Max(0, v)) }
	return rgb{clamp(4.0767416621*l3 - 3.3077115913*m3 + 0.2309699292*s3), clamp(-1.2684380046*l3 + 2.6097574011*m3 - 0.3413193965*s3), clamp(-0.0041960863*l3 - 0.7034186147*m3 + 1.7076147010*s3)}, alpha
}

func (c rgb) luminance() float64 { return 0.2126*c.r + 0.7152*c.g + 0.0722*c.b }

func toSRGB(v float64) float64 {
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

func fromSRGB(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// over is fg at opacity a on bg, mixed in sRGB-encoded space as browsers do.
func over(fg, bg rgb, a float64) rgb {
	mix := func(f, b float64) float64 { return fromSRGB(a*toSRGB(f) + (1-a)*toSRGB(b)) }
	return rgb{mix(fg.r, bg.r), mix(fg.g, bg.g), mix(fg.b, bg.b)}
}

func contrast(a, b rgb) float64 {
	la, lb := a.luminance(), b.luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// tokens reads the custom properties of a CSS declaration block ("--a:oklch(...);--b:...").
func tokens(block string) map[string]string {
	out := map[string]string{}
	for _, d := range regexp.MustCompile(`(--[\w-]+):([^;]+)`).FindAllStringSubmatch(block, -1) {
		out[d[1]] = strings.TrimSpace(d[2])
	}
	return out
}

// themeTokens returns the tokens of the light theme and the dark one (the light tokens with the dark ones over them).
func themeTokens(t *testing.T) (light, dark map[string]string) {
	t.Helper()
	root := regexp.MustCompile(`(?s):root\{([^}]*)\}`).FindAllStringSubmatch(hostedCSS, -1)
	if len(root) < 2 {
		t.Fatalf("expected a :root block for the light theme and one in the dark media query, found %d", len(root))
	}
	light = tokens(root[0][1])
	dark = map[string]string{}
	for k, v := range light {
		dark[k] = v
	}
	for k, v := range tokens(root[1][1]) {
		dark[k] = v
	}
	return light, dark
}

type textPair struct{ fg, bg string }

// pairs are the text colours the page prints on the backgrounds it prints them on: --background (the pane: its card has none), --card (inputs), --muted (stat, hint, code),
// and the soft backgrounds of the badges and the banner.
var textPairs = []textPair{
	{"--foreground", "--background"}, {"--foreground", "--card"}, {"--foreground", "--muted"}, {"--foreground", "--secondary"},
	{"--muted-foreground", "--background"}, {"--muted-foreground", "--card"}, {"--muted-foreground", "--muted"},
	{"--primary-foreground", "--primary"},
	{"--accent-foreground", "--background"}, {"--accent-foreground", "--card"},
	{"--destructive", "--background"}, {"--destructive", "--card"}, {"--destructive", "--destructive-soft"},
	{"--success", "--background"}, {"--success", "--card"}, {"--success", "--success-soft"},
	{"--warning", "--warning-soft"},
	{"--info", "--info-soft"},
}

func TestHostedTextContrastMeetsAA(t *testing.T) {
	light, dark := themeTokens(t)
	for name, th := range map[string]map[string]string{"light": light, "dark": dark} {
		for _, p := range textPairs {
			fg, _ := parseOKLCH(t, th[p.fg])
			bg, _ := parseOKLCH(t, th[p.bg])
			if r := contrast(fg, bg); r < 4.5 {
				t.Errorf("%s: %s on %s is %.2f:1, want at least 4.5:1", name, p.fg, p.bg, r)
			}
		}
		// The focus ring is a UI component: 3:1 against the page.
		ring, _ := parseOKLCH(t, th["--ring"])
		bg, _ := parseOKLCH(t, th["--background"])
		if r := contrast(ring, bg); r < 3 {
			t.Errorf("%s: --ring on --background is %.2f:1, want at least 3:1", name, r)
		}
	}
}

// The coloured side is white on a gradient in both themes; its two fainter lines are white at 78% and 70% opacity.
func TestHostedSideContrastMeetsAA(t *testing.T) {
	m := regexp.MustCompile(`\.side\{[^}]*linear-gradient\(\d+deg,(oklch\([^)]*\)),(oklch\([^)]*\))\)`).FindStringSubmatch(pageCSS)
	if m == nil {
		t.Fatal("the gradient of .side is not where the test looks for it")
	}
	white := rgb{1, 1, 1}
	for _, stop := range m[1:] {
		bg, _ := parseOKLCH(t, stop)
		for _, a := range []float64{1, 0.78, 0.7} {
			if r := contrast(over(white, bg, a), bg); r < 4.5 {
				t.Errorf("white at %.0f%% on %s is %.2f:1, want at least 4.5:1", a*100, stop, r)
			}
		}
	}
	for _, want := range []string{".sidenote{margin-top:6px;opacity:.78}", ".sidefoot{font-size:.82rem;opacity:.7}"} {
		if !strings.Contains(pageCSS, want) {
			t.Errorf("the opacities the test assumes changed (%s)", want)
		}
	}
}

// The tokens of the light theme are what a printed page uses, whatever the reader's colour scheme.
func TestHostedPrintUsesTheLightTokens(t *testing.T) {
	light, _ := themeTokens(t)
	i := strings.LastIndex(hostedCSS, "@media print{:root{")
	if i < 0 {
		t.Fatal("no print override of the tokens")
	}
	got := tokens(hostedCSS[i : strings.Index(hostedCSS[i:], "}}")+i])
	for k, v := range light {
		if got[k] != v {
			t.Errorf("print: %s = %q, want the light theme's %q", k, got[k], v)
		}
	}
}
