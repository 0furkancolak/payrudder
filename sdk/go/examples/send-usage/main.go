// Send one usage event. PAYRUDDER_API_KEY (qk_test_... for the sandbox) and PAYRUDDER_BASE_URL are read from the environment.
package main

import (
	"context"
	"log"
	"time"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

func main() {
	client, err := payrudder.New()
	if err != nil {
		log.Fatal(err)
	}
	results, err := client.Events.Ingest(context.Background(), []payrudder.IncomingEvent{{
		CustomerExternalID: "cus_42",
		MeterCode:          "api_calls",
		Quantity:           "1",
		EventTime:          time.Now(),
		// Name the thing that happened (a request id, a job id) so a retry never counts twice. A live key must send one.
		IdempotencyKey: "request-8f2c",
	}})
	if err != nil {
		log.Fatal(err)
	}
	r := results[0]
	if r.Status == payrudder.EventRejected {
		log.Fatalf("rejected: %s: %s", r.ErrorCode, r.ErrorMessage)
	}
	log.Printf("%s event %s", r.Status, r.EventID) // accepted | duplicate
}
