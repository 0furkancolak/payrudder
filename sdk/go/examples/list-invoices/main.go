// List a customer's paid invoices. The iterator follows next_page_token for you and yields invoices, not pages.
package main

import (
	"context"
	"log"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

func main() {
	client, err := payrudder.New()
	if err != nil {
		log.Fatal(err)
	}
	params := &payrudder.ListInvoicesParams{Customer: "cus_42", Status: "paid", PageSize: payrudder.Ptr(100)}
	for inv, err := range client.Invoices.ListInvoicesAll(context.Background(), params) {
		if err != nil {
			log.Fatal(err)
		}
		// TotalMinor is in the currency's minor units (cents, kurus), an int64 on the wire as a string.
		log.Printf("%s %s %s %s", inv.Number, inv.PeriodID, inv.TotalMinor, inv.Currency)
	}
}
