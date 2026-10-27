package payrudder_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

// These examples are compiled, not run: they need a gateway.

func ExampleClient_ingest() {
	client, err := payrudder.New(payrudder.WithAPIKey("qk_test_..."))
	if err != nil {
		log.Fatal(err)
	}
	results, err := client.Events.Ingest(context.Background(), []payrudder.IncomingEvent{{
		CustomerExternalID: "cus_42",
		MeterCode:          "ai_tokens",
		Quantity:           "1500",
		EventTime:          time.Now(),
		Properties:         map[string]string{"model": "gpt-x"},
	}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(results[0].Status)
}

func ExampleError() {
	client, _ := payrudder.New(payrudder.WithAPIKey("qk_test_..."))
	_, err := client.Customers.GetCustomer(context.Background(), "cus_404")
	var perr *payrudder.Error
	if errors.As(err, &perr) && perr.Kind == payrudder.KindNotFound {
		fmt.Println("no such customer", perr.RequestID)
	}
}

func ExampleCustomersService_ListCustomersAll() {
	client, _ := payrudder.New(payrudder.WithAPIKey("qk_test_..."))
	for cust, err := range client.Customers.ListCustomersAll(context.Background(), &payrudder.ListCustomersParams{PageSize: payrudder.Ptr(100)}) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(cust.ExternalID)
	}
}

func ExampleConstructEvent() {
	http.HandleFunc("/webhooks/payrudder", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		ev, err := payrudder.ConstructEvent(body, r.Header.Get(payrudder.WebhookSignatureHeader), os.Getenv("PAYRUDDER_WEBHOOK_SECRET"), 0)
		if err != nil {
			http.Error(w, "bad signature", http.StatusBadRequest)
			return
		}
		if ev.Type == payrudder.EventInvoicePaid {
			var d payrudder.InvoiceEventData
			if err := ev.DecodeData(&d); err == nil {
				log.Printf("invoice %s paid: %d minor units", d.Number, int64(d.TotalMinor))
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
