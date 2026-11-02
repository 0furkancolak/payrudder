package snapshotpdf

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
)

// pdfText is the text the PDF shows, one line per text operation, in drawing order. Pages are not compressed and every string is the UTF-16BE of the text (the embedded
// font is addressed by code point), so a few lines of parsing replace a PDF library.
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
				if rest[n] == 'r' {
					raw = append(raw, '\r')
				} else {
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

var accepted = time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)

func snapshot(locale, customer, body string) string {
	return domain.RenderSnapshot(domain.Snapshot{
		Seller: "Ada Labs Ltd", Customer: customer, Email: "ada@example.com", AcceptedAt: accepted, Locale: locale, Source: "hosted",
		Values: map[string]string{"seller": "Ada Labs Ltd"},
		Items: []domain.SnapshotItem{{Kind: domain.ContractDistanceSales, Number: 3, LocaleServed: locale, Body: body, BodySHA256: strings.Repeat("ab", 32), VersionSHA256: strings.Repeat("cd", 32),
			CheckboxText: "I have read and accept"}},
		ImmediateStart: "I want the service to start now", WaiverAck: "I know I lose the right of withdrawal then",
		Observed: map[string]bool{"ip": true, "user_agent": true},
	})
}

const text = "# Distance sales contract\n\nThe seller is **{{seller}}**. The buyer pays in advance.\n\n- Delivery is digital\n- Support is by mail\n\n1. First term\n2. Second term\n"

func TestThePageIsDrawnAsItWasStored(t *testing.T) {
	page := snapshot("en", "Ada Lovelace", text)
	pdf, err := Render(page, accepted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("not a PDF: %q", pdf[:10])
	}
	got := pdfText(t, pdf)
	for _, want := range []string{"Record of acceptance", "Customer: Ada Lovelace", "Accepted: 2026-11-02 09:30:00 UTC", "Distance sales contract", "The seller is Ada Labs Ltd. The buyer pays in advance.",
		"Delivery is digital", "Second term", "[x] I have read and accept", "[x] I want the service to start now", strings.Repeat("ab", 32)} {
		if !strings.Contains(got, want) {
			t.Errorf("the PDF does not show %q:\n%s", want, got)
		}
	}
	sum := sha256.Sum256([]byte(page))
	if !strings.Contains(got, hex.EncodeToString(sum[:])) {
		t.Error("the PDF does not name the hash of the page it was drawn from")
	}
	if strings.Contains(got, "<") || strings.Contains(got, "&amp;") {
		t.Errorf("markup leaked into the PDF:\n%s", got)
	}
	again, err := Render(page, accepted)
	if err != nil || !bytes.Equal(pdf, again) {
		t.Errorf("the same page did not give the same bytes (%v)", err)
	}
	if other, _ := Render(page+"<!-- changed -->", accepted); bytes.Equal(pdf, other) {
		t.Error("another page gave the same PDF: the hash line should differ")
	}
}

func TestTurkishLettersAreDrawn(t *testing.T) {
	pdf, err := Render(snapshot("tr", "Şükrü Çağlayan", "Ödeme peşin yapılır. Iğdır İstanbul."), accepted)
	if err != nil {
		t.Fatal(err)
	}
	got := pdfText(t, pdf)
	for _, want := range []string{"Müşteri: Şükrü Çağlayan", "Kabul kaydı", "Iğdır İstanbul"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestAPageTheFontCannotDrawIsNotDrawnWithQuestionMarks(t *testing.T) {
	for name, page := range map[string]string{
		"an Arabic snapshot":        snapshot("ar", "Ada", "نص العقد"),
		"a customer named in Hanzi": snapshot("en", "李雷", text),
		"text that is no snapshot":  "",
	} {
		if _, err := Render(page, accepted); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: %v, want ErrUnsupported", name, err)
		}
	}
}

func TestALongTextRunsOverPages(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 120; i++ {
		b.WriteString("Clause number " + strings.Repeat("word ", 30) + "\n\n")
	}
	pdf, err := Render(snapshot("en", "Ada", b.String()), accepted)
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(pdf, []byte("/Type /Page\n")); n < 3 {
		t.Errorf("only %d pages for 120 paragraphs", n)
	}
	if got := pdfText(t, pdf); strings.Contains(got, "{nb}") || !strings.Contains(got, "1 / ") {
		t.Errorf("the page count was not filled in:\n%s", got[len(got)-300:])
	}
}
