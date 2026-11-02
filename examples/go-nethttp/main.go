// A net/http server that counts usage with Payrudder and verifies its webhooks. Standard library plus the SDK.
package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

func main() {
	// PAYRUDDER_API_KEY and PAYRUDDER_BASE_URL are read by the SDK; the webhook secret (whsec_...) is the one the endpoint was created with.
	client, err := payrudder.New()
	if err != nil {
		log.Fatal(err)
	}
	secret := os.Getenv("PAYRUDDER_WEBHOOK_SECRET")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhooks/payrudder", webhook(secret))
	mux.HandleFunc("POST /usage", usage(client))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })

	addr := ":" + envOr("PORT", "3000")
	log.Printf("listening on %s", addr)
	log.Fatal((&http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}).ListenAndServe())
}

// webhook verifies the signature over the exact bytes Payrudder sent, so the body is read raw and parsed only after it is verified.
func webhook(secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "unreadable body", http.StatusBadRequest)
			return
		}
		ev, err := payrudder.ConstructEvent(body, r.Header.Get(payrudder.WebhookSignatureHeader), secret, 0)
		if err != nil {
			http.Error(w, "bad signature", http.StatusBadRequest)
			return
		}
		log.Printf("webhook %q %q", ev.ID, ev.Type) //nolint:gosec // the signature is verified, so id and type were written by Payrudder
		// Do the work after answering, or put it on a queue: Payrudder retries a delivery that is not answered with a 2xx.
		w.WriteHeader(http.StatusOK)
	}
}

// usage counts one event: POST /usage {"customer": "cus_42", "meter": "api_calls", "quantity": "1"}.
func usage(client *payrudder.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Customer string `json:"customer"`
			Meter    string `json:"meter"`
			Quantity string `json:"quantity"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil || in.Customer == "" || in.Meter == "" {
			http.Error(w, "customer and meter are required", http.StatusBadRequest)
			return
		}
		if in.Quantity == "" {
			in.Quantity = "1"
		}
		// A key per request makes a retry of this call count once. A real app derives it from what happened (an order id), not from the clock.
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			key = "usage-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		results, err := client.Events.Ingest(r.Context(), []payrudder.IncomingEvent{{
			CustomerExternalID: in.Customer, MeterCode: in.Meter, Quantity: payrudder.Decimal(in.Quantity), EventTime: time.Now(), IdempotencyKey: key,
		}})
		if err != nil {
			var apiErr *payrudder.Error
			if errors.As(err, &apiErr) {
				http.Error(w, apiErr.Key, http.StatusBadGateway)
				return
			}
			http.Error(w, "payrudder is unreachable", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(results[0])
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
