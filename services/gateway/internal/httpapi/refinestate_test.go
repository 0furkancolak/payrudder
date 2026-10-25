package httpapi

import (
	"encoding/json"
	"testing"
	"time"
)

func charge(id, status string, at time.Time) ledgerRow {
	return ledgerRow{ID: json.Number(id), InvoiceID: "inv", Kind: "charge", Status: status, OccurredAt: at}
}

// A pending charge is a checkout or a transfer instruction that was handed out; many providers send nothing when the customer walks away. The page
// must not stay "processing" forever and hide the pay buttons.
func TestRefineStatePendingChargeExpires(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		state string
		rows  []ledgerRow
		want  string
	}{
		{"fresh pending is processing", "open", []ledgerRow{charge("1", "pending", now.Add(-5*time.Minute))}, "processing"},
		{"abandoned checkout is payable again", "open", []ledgerRow{charge("1", "pending", now.Add(-2*time.Hour))}, "open"},
		{"abandoned pending after a decline shows the decline", "open", []ledgerRow{charge("1", "declined", now.Add(-3*time.Hour)), charge("2", "pending", now.Add(-2*time.Hour))}, "failed"},
		{"a decline is still failed", "open", []ledgerRow{charge("1", "declined", now.Add(-time.Minute))}, "failed"},
		{"settled invoices are untouched", "paid", []ledgerRow{charge("1", "pending", now)}, "paid"},
	}
	for _, c := range cases {
		if got := refineState(c.state, c.rows, "inv", now); got != c.want {
			t.Errorf("%s: refineState = %q, want %q", c.name, got, c.want)
		}
	}
}
