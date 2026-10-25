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
	}{
		{"the whole invoice", []*commandsv1.InvoiceLineSnapshot{line("BASE_FEE", 10000), tax}, 12000},
		{"a third of it", []*commandsv1.InvoiceLineSnapshot{line("BASE_FEE", 10000), line("USAGE", 3333), line("TAX", 2667)}, 5333},
		{"with a discount", []*commandsv1.InvoiceLineSnapshot{line("BASE_FEE", 10000), line("DISCOUNT", -1500), line("TAX", 1700)}, 4001},
		{"one minor unit", []*commandsv1.InvoiceLineSnapshot{line("BASE_FEE", 999), line("USAGE", 1), line("TAX", 200)}, 1},
		{"the credit balance is not part of it", []*commandsv1.InvoiceLineSnapshot{line("BASE_FEE", 10000), line("TAX", 2000), line("CREDIT", -3000)}, 9000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ProrateLines(tc.lines, tc.target)
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
	if got := ProrateLines([]*commandsv1.InvoiceLineSnapshot{line("CREDIT", -5)}, 5); got != nil {
		t.Errorf("nothing to prorate: %v", got)
	}
}
