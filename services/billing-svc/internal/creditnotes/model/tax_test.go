package model

import "testing"

// A credit note gives back the tax of what it credits in proportion: its share of the invoice's total is its share of the invoice's tax, rounded half to even, and the
// notes of one invoice add up to the invoice's tax exactly, whatever the rounding (the note that takes what is left of the invoice takes what is left of the tax).
func TestTaxShare(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		tax, total, taxBefore, left, amount, w int64
	}{
		{"half of a taxed invoice", 20, 120, 0, 120, 60, 10},
		{"all of it", 20, 120, 0, 120, 120, 20},
		{"an untaxed invoice", 0, 120, 0, 120, 60, 0},
		{"a small note rounds down", 20, 120, 0, 120, 1, 0},
		{"half to even, down", 5, 100, 0, 100, 10, 0}, // 0.5
		{"half to even, up", 5, 100, 0, 100, 30, 2},   // 1.5
		{"the first of three", 10, 100, 0, 100, 33, 3},
		{"the second of three", 10, 100, 3, 67, 33, 3},
		{"the last of three takes what is left", 10, 100, 6, 34, 34, 4},
		{"a partial note never takes more than is left", 10, 100, 9, 20, 15, 1},
		{"older notes without tax leave the whole tax for the note that closes the invoice", 20, 120, 0, 80, 80, 20},
		{"never more than the note itself (a credit balance paid most of the invoice)", 20, 30, 0, 30, 5, 3},
		{"the tax is capped by the amount", 20, 30, 17, 3, 3, 3},
		{"nothing left to give back", 20, 120, 20, 60, 60, 0},
		{"a zero total", 20, 0, 0, 0, 0, 0},
	} {
		if got := TaxShare(tc.tax, tc.total, tc.taxBefore, tc.left, tc.amount); got != tc.w {
			t.Errorf("%s: TaxShare(tax %d, total %d, before %d, left %d, amount %d) = %d, want %d", tc.name, tc.tax, tc.total, tc.taxBefore, tc.left, tc.amount, got, tc.w)
		}
	}
}

// Whatever the sizes of the notes, the tax they give back never exceeds the invoice's, and notes that credit the invoice in full give all of it back.
func TestTaxSharesOfSeveralNotesAddUpToTheInvoicesTax(t *testing.T) {
	const total = 1000
	for _, tax := range []int64{1, 7, 20, 333, 999} {
		for _, parts := range [][]int64{{1000}, {500, 500}, {333, 333, 334}, {1, 998, 1}, {999, 1}, {1, 1, 998}} {
			var before, credited int64
			for _, p := range parts {
				share := TaxShare(tax, total, before, total-credited, p)
				if share < 0 || share > p {
					t.Fatalf("tax %d, parts %v: a note of %d gave back %d", tax, parts, p, share)
				}
				before += share
				credited += p
			}
			if before != tax {
				t.Errorf("tax %d, parts %v: gave back %d in all", tax, parts, before)
			}
		}
	}
}
