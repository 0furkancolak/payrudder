package command

import (
	"testing"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
)

func line(kind string, amount int64) *commandsv1.InvoiceLineSnapshot {
	return &commandsv1.InvoiceLineSnapshot{Kind: kind, AmountMinor: amount, Description: kind, Quantity: "1.5", UnitLabel: "GB"}
}

func sum(ls []*commandsv1.InvoiceLineSnapshot) (total int64) {
	for _, l := range ls {
		total += l.GetAmountMinor()
	}
	return total
}

func TestProrateLinesAddsUpExactly(t *testing.T) {
	tax := line("TAX", 2000)
	for _, tc := range []struct {
		name   string
		lines  []*commandsv1.InvoiceLineSnapshot
		target int64
		tax    int64 // the part of target the TAX lines carry
	}{
		{"the whole invoice", []*commandsv1.InvoiceLineSnapshot{line("BASE_FEE", 10000), tax}, 12000, 2000},
		{"a third of it", []*commandsv1.InvoiceLineSnapshot{line("BASE_FEE", 10000), line("USAGE", 3333), line("TAX", 2667)}, 5333, 1067},
		{"with a discount", []*commandsv1.InvoiceLineSnapshot{line("BASE_FEE", 10000), line("DISCOUNT", -1500), line("TAX", 1700)}, 4001, 567},
		{"one minor unit", []*commandsv1.InvoiceLineSnapshot{line("BASE_FEE", 999), line("USAGE", 1), line("TAX", 200)}, 1, 0},
		{"the credit balance is not part of it", []*commandsv1.InvoiceLineSnapshot{line("BASE_FEE", 10000), line("TAX", 2000), line("CREDIT", -3000)}, 9000, 1500},
		{"an untaxed invoice", []*commandsv1.InvoiceLineSnapshot{line("BASE_FEE", 10000)}, 4000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ProrateLines(tc.lines, tc.target, tc.tax)
			if s := sum(got); s != tc.target {
				t.Fatalf("lines add up to %d, want %d: %v", s, tc.target, got)
			}
			for i, l := range got {
				if l.GetKind() == "CREDIT" || l.GetLineNo() != int32(i+1) || l.GetQuantity() != "" {
					t.Errorf("line %d = %v", i, l)
				}
			}
		})
	}
	if got := ProrateLines([]*commandsv1.InvoiceLineSnapshot{line("CREDIT", -5)}, 5, 0); got != nil {
		t.Errorf("nothing to prorate: %v", got)
	}
}

// The tax of a credit note's document is the tax the note gave back and nothing else: its TAX line carries exactly that, the other lines carry the rest, whatever the
// rounding of a proportional split would have made of it.
func TestProrateLinesPutsTheTaxTheNoteGaveBackOnTheTaxLine(t *testing.T) {
	lines := []*commandsv1.InvoiceLineSnapshot{line("BASE_FEE", 1000), line("USAGE", 100), line("TAX", 220), line("CREDIT", -50)}
	for _, tc := range []struct{ target, tax int64 }{{660, 110}, {400, 67}, {520, 86}, {1, 0}, {1320, 220}} {
		got := ProrateLines(lines, tc.target, tc.tax)
		var taxLines, others int64
		for _, l := range got {
			if l.GetKind() == "TAX" {
				taxLines += l.GetAmountMinor()
			} else {
				others += l.GetAmountMinor()
			}
		}
		if taxLines != tc.tax || others != tc.target-tc.tax {
			t.Errorf("note of %d with %d tax: the TAX lines carry %d and the others %d", tc.target, tc.tax, taxLines, others)
		}
	}
}
