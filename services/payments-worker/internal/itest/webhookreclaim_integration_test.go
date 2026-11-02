//go:build integration

package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/iyzico"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
)

// The inbox answers a provider's repeat of an event it has recorded with "duplicate", which is right once the first delivery finished. A delivery that did not (the process
// died between the inbox write and the answer, or the "failed" mark and the clean-up both failed) left a row that turned every retry of the provider into a duplicate for
// good: the notification was never processed, and the provider stopped retrying on the 200. A row still received or failed after the window is taken over by the retry.
func TestAStuckInboxRowIsTakenOverByTheProvidersRetry(t *testing.T) {
	pg := testkit.StartPostgres(t)
	cdb := openDB(t, pg)
	pool := cdb.DB
	ctx := context.Background()
	fake := newFakeIyzico(t)

	box, _ := secretbox.New(bytes.Repeat([]byte{7}, 32))
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	accounts := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	attempts, ledger, events := store.Attempts{DB: pool}, store.Ledger{DB: pool}, store.Events{DB: pool}
	api := httptest.NewServer((&webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: events, Attempts: attempts, Ledger: ledger,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Token: "internal"}).Handler())
	defer api.Close()

	tenant := uuid.New()
	settings := `{"api_key":"api","secret_key":"s","base_url":"` + fake.srv.URL + `","buyer":{"name":"Ada","surname":"L","identity_number":"11111111111","address":"x","city":"Istanbul","country":"Turkey","zip_code":"34000","ip":"1.2.3.4","gsm_number":"+905550000000","email":"b@example.com"}}`
	if err := accounts.Put(ctx, tenant, "iyzico", "live", []byte(settings), true); err != nil {
		t.Fatal(err)
	}
	notify := func(event, key string) string {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"iyziEventType": "API_AUTH", "iyziReferenceCode": event, "paymentId": 1, "paymentConversationId": key, "status": "SUCCESS", "iyziEventTime": 1})
		resp, err := http.Post(api.URL+"/webhooks/iyzico", "application/json", strings.NewReader(string(b)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("webhook answered %d %s", resp.StatusCode, out)
		}
		return string(out)
	}
	// A charge of ours the provider settled later: an attempt, and no ledger row yet. That row is what a processed notification adds.
	charge := func(t *testing.T, payment string) (invoice uuid.UUID, key string) {
		t.Helper()
		invoice = uuid.New()
		key = invoice.String() + ":charge:0"
		fake.mu.Lock()
		fake.basket = invoice.String() // iyzico's payment detail names the basket the payment was made for
		fake.detail = `{"status":"success","paymentId":"` + payment + `","paymentStatus":"SUCCESS"}`
		fake.mu.Unlock()
		if _, err := attempts.Start(ctx, uuid.New(), tenant, invoice, "charge", "live", "iyzico", key, map[string]any{"amount_minor": 1116, "currency": "TRY"}); err != nil {
			t.Fatal(err)
		}
		return invoice, key
	}
	rows := func(invoice uuid.UUID) int {
		t.Helper()
		var n int
		if err := cdb.At(tenant).QueryRow(ctx, `SELECT count(*) FROM payment_ledger WHERE tenant_id = $1 AND invoice_id = $2 AND kind = 'charge' AND source = 'webhook'`, tenant, invoice).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	age := func(event string, minutes int) {
		t.Helper()
		if _, err := cdb.At(tenant).Exec(ctx, `UPDATE provider_events SET received_at = now() - make_interval(mins => $2) WHERE provider = 'iyzico' AND event_id = $1`, event, minutes); err != nil {
			t.Fatal(err)
		}
	}
	status := func(event string) string {
		t.Helper()
		var s string
		if err := cdb.At(tenant).QueryRow(ctx, `SELECT status FROM provider_events WHERE provider = 'iyzico' AND event_id = $1`, event).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	for _, stuck := range []string{"received", "failed"} {
		t.Run("a row left "+stuck, func(t *testing.T) {
			invoice, key := charge(t, "P-"+stuck)
			event := "ev-" + stuck
			// The delivery that died after the inbox write (received), or whose "failed" mark could not be followed by the clean-up (failed).
			if fresh, err := events.Receive(ctx, tenant, "iyzico", event, []byte(`{}`)); err != nil || !fresh {
				t.Fatalf("receive = %v, %v", fresh, err)
			}
			if stuck == "failed" {
				if err := events.Finish(ctx, "iyzico", event, tenant, "failed", "the provider could not be asked"); err != nil {
					t.Fatal(err)
				}
			}

			// The provider's retry soon after is still a duplicate: the first delivery may be running.
			age(event, 2)
			if got := notify(event, key); !strings.Contains(got, "duplicate") || rows(invoice) != 0 {
				t.Fatalf("a retry inside the window = %s, %d ledger rows; want a duplicate and none", got, rows(invoice))
			}

			// After the window the retry is processed.
			age(event, 11)
			if got := notify(event, key); !strings.Contains(got, "processed") {
				t.Fatalf("a retry after the window = %s; want it processed", got)
			}
			if n, s := rows(invoice), status(event); n != 1 || s != "processed" {
				t.Fatalf("after the retry: %d ledger rows, inbox %q; want 1 and processed", n, s)
			}

			// A processed row is final however old it is.
			age(event, 600)
			if got := notify(event, key); !strings.Contains(got, "duplicate") || rows(invoice) != 1 {
				t.Fatalf("a repeat of a processed event = %s, %d ledger rows; want a duplicate and still 1", got, rows(invoice))
			}
		})
	}
}
