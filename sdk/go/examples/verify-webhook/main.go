// Receive webhook deliveries and check their signature. Set PAYRUDDER_WEBHOOK_SECRET to the secret of the endpoint that posts here.
package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"time"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

func main() {
	secret := os.Getenv("PAYRUDDER_WEBHOOK_SECRET")
	if secret == "" {
		log.Fatal("set PAYRUDDER_WEBHOOK_SECRET to the secret of your webhook endpoint")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhooks/payrudder", func(w http.ResponseWriter, r *http.Request) {
		// The signature is over the raw bytes: read the body as it arrived, parse it only after it verified.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		ev, err := payrudder.ConstructEvent(body, r.Header.Get(payrudder.WebhookSignatureHeader), secret, 0)
		if err != nil {
			http.Error(w, "bad signature", http.StatusBadRequest) // not signed by this endpoint's secret, or too old
			return
		}
		// Deliveries are at least once: ignore an event id you have already handled.
		log.Printf("%q %q", ev.ID, ev.Type) //nolint:gosec // the body is signed by Payrudder, and %q quotes it
		w.WriteHeader(http.StatusNoContent)
	})
	srv := &http.Server{Addr: ":3000", Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Print("listening on http://localhost:3000/webhooks/payrudder")
	log.Fatal(srv.ListenAndServe())
}
