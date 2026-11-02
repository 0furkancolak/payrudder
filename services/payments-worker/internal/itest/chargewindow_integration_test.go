//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
)

// The window a payment that waits for the customer has starts when the newest charge key of the invoice was first tried (the moment handler.providerStatus judges by) and
// ends PAYMENTS_TRANSFER_WINDOW_DAYS later: a re-sent charge under the same key does not move it, a charge under a new key (a dunning retry) does, an invoice with no charge
// has none, and another tenant sees none of it.
func TestTheWindowOfAPaymentThatWaitsForTheCustomer(t *testing.T) {
	wd := openDB(t, testkit.StartPostgres(t))
	ctx := context.Background()
	tenant, other, invoice := uuid.New(), uuid.New(), uuid.New()
	attempts := store.Attempts{DB: wd.DB}
	first := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	try := func(tid uuid.UUID, inv uuid.UUID, key string, at time.Time) {
		t.Helper()
		id := uuid.New()
		if _, err := attempts.Start(ctx, id, tid, inv, "charge", "live", "stripe", key, map[string]any{}); err != nil {
			t.Fatal(err)
		}
		if _, err := wd.At(tid).Exec(ctx, `UPDATE payment_attempts SET created_at = $1 WHERE command_id = $2`, at, id); err != nil {
			t.Fatal(err)
		}
	}
	srv := &webhook.Server{Registry: payments.NewRegistry(), Attempts: attempts, TransferWindow: 14 * 24 * time.Hour, Token: "internal", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	api := httptest.NewServer(srv.Handler())
	defer api.Close()
	window := func(tid, inv uuid.UUID) (found bool, started, ends string) {
		t.Helper()
		req, _ := http.NewRequest("GET", api.URL+"/admin/tenants/"+tid.String()+"/invoices/"+inv.String()+"/charge-window", nil)
		req.Header.Set("X-Internal-Token", "internal")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			Found      bool   `json:"found"`
			Started    string `json:"started_time"`
			WindowEnds string `json:"window_ends_time"`
		}
		if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&out) != nil {
			t.Fatalf("status %d", resp.StatusCode)
		}
		return out.Found, out.Started, out.WindowEnds
	}

	if found, _, _ := window(tenant, invoice); found {
		t.Fatal("an invoice with no charge has a window")
	}
	try(tenant, invoice, invoice.String()+":charge:0", first)
	try(tenant, invoice, invoice.String()+":charge:0", first.Add(10*time.Minute)) // the same charge, sent again
	if found, started, ends := window(tenant, invoice); !found || started != "2026-11-01T09:00:00Z" || ends != "2026-11-15T09:00:00Z" {
		t.Fatalf("window = %v %s %s; want it to start at the first try (09:00) and end 14 days later", found, started, ends)
	}
	try(tenant, invoice, invoice.String()+":charge:1", first.Add(48*time.Hour)) // a dunning retry: a new key, a new window
	if found, started, ends := window(tenant, invoice); !found || started != "2026-11-03T09:00:00Z" || ends != "2026-11-17T09:00:00Z" {
		t.Fatalf("window after a retry = %v %s %s; want it to start at the new key's first try (11-03)", found, started, ends)
	}
	if found, _, _ := window(other, invoice); found {
		t.Error("another tenant reads the window of this tenant's invoice")
	}
	req, _ := http.NewRequest("GET", api.URL+"/admin/tenants/"+tenant.String()+"/invoices/"+invoice.String()+"/charge-window", nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("without the internal token: %v, %v; want 401", resp, err)
	}
}
