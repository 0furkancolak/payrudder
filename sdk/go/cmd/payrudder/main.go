// Command payrudder is the command-line client of the Payrudder tenant API: sign in once, send a usage event, follow a customer's usage, receive and
// forward webhooks on your own machine, redeliver a delivery, make customers and plans, and list customers and invoices. It is the Go SDK (package
// payrudder) behind a small command set, so it needs nothing but the standard library and talks to the same /v1 routes your code does, with an API key.
//
//	payrudder login
//	payrudder events send --customer cus_42 --meter api_calls --quantity 3
//	payrudder events tail --customer cus_42
//	payrudder webhooks listen --forward-to http://localhost:3000/webhooks/payrudder
//	payrudder customers list
//
// Install from a checkout: go install ./cmd/payrudder (in sdk/go). It is pre-release like the SDK; nothing is published yet.
package main

import (
	"context"
	"os"
	"os/signal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv)
	stop()
	os.Exit(code)
}
