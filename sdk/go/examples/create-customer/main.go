// Create a customer. The external id is the customer's natural key: creating it twice answers 409 instead of making a second customer.
package main

import (
	"context"
	"errors"
	"log"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

func main() {
	client, err := payrudder.New()
	if err != nil {
		log.Fatal(err)
	}
	customer, err := client.Customers.CreateCustomer(context.Background(), &payrudder.CreateCustomerRequest{
		ExternalID: "cus_42", Name: "Ada Lovelace", Email: "ada@example.com",
	})
	switch {
	case errors.Is(err, payrudder.ErrConflict):
		log.Print("cus_42 exists already")
	case err != nil:
		log.Fatal(err)
	default:
		log.Printf("created %s", customer.ExternalID)
	}
}
