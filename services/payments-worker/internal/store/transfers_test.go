package store

import (
	"errors"
	"testing"
)

func TestCheckReceipt(t *testing.T) {
	for name, c := range map[string]struct {
		received, amount, total int64
		paid                    bool
		over                    int64 // the outstanding amount reported when the receipt is refused; -1 when it is fine
	}{
		"the whole amount at once":  {0, 1000, 1000, true, -1},
		"a part":                    {0, 400, 1000, false, -1},
		"the rest after a part":     {400, 600, 1000, true, -1},
		"a part of the rest":        {400, 100, 1000, false, -1},
		"one unit too much":         {0, 1001, 1000, false, 1000},
		"too much after a part":     {400, 601, 1000, false, 600},
		"anything after it is paid": {1000, 1, 1000, false, 0},
	} {
		paid, err := CheckReceipt(c.received, c.amount, c.total)
		var over ErrOverpayment
		switch {
		case c.over < 0 && err != nil:
			t.Errorf("%s: %v", name, err)
		case c.over >= 0 && (!errors.As(err, &over) || over.Outstanding != c.over):
			t.Errorf("%s: err %v, want an overpayment with %d outstanding", name, err, c.over)
		case err == nil && paid != c.paid:
			t.Errorf("%s: paid = %v, want %v", name, paid, c.paid)
		}
	}
	for _, amount := range []int64{0, -5} {
		if _, err := CheckReceipt(0, amount, 1000); err == nil {
			t.Errorf("amount %d accepted", amount)
		}
	}
}
