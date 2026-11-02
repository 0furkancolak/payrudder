package main

import (
	"context"
	"strings"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

// customersCreate makes one customer (POST /v1/customers). A customer is not deduplicated by a key: the external id is the natural key, so running the
// command again answers 409 and exits 1 instead of making a second customer.
func (c *cli) customersCreate(ctx context.Context, args []string) error {
	fs := c.flags("customers create")
	base := fs.String("base-url", "", "the gateway address")
	id := fs.String("id", "", "the customer's external id: your own id for them (required)")
	name := fs.String("name", "", "the customer's name (required)")
	email := fs.String("email", "", "the customer's e-mail address (required)")
	country := fs.String("country", "", "ISO 3166-1 alpha-2 country, e.g. TR")
	currency := fs.String("currency", "", "ISO 4217 billing currency (default: the tenant's)")
	locale := fs.String("locale", "", "the language of the customer's mails and pages: en, tr, de, fr, es or ar")
	kind := fs.String("type", "", "individual or business")
	asJSON := fs.Bool("json", false, "print the customer as JSON")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *id == "" || *name == "" || *email == "" {
		return errUsage("customers create needs --id, --name and --email")
	}
	client, err := c.client(*base)
	if err != nil {
		return err
	}
	cust, err := client.Customers.CreateCustomer(ctx, &payrudder.CreateCustomerRequest{
		ExternalID: *id, Name: *name, Email: *email, Country: *country, BillingCurrency: *currency, Locale: *locale, CustomerType: *kind,
	})
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(cust)
	}
	c.println("created customer %s (%s, %s)", cust.ExternalID, cust.Name, cust.Email)
	return nil
}

// plansCreate makes the plan itself: a code and a name. Its prices and entitlements are a version (POST /v1/plans/{code}/versions), which the command line
// does not publish: a version is immutable and its prices are a document, better written in a file under review than in flags.
func (c *cli) plansCreate(ctx context.Context, args []string) error {
	fs := c.flags("plans create")
	base := fs.String("base-url", "", "the gateway address")
	code := fs.String("code", "", "the plan's code, which names it in the API (required)")
	name := fs.String("name", "", "the plan's name (required)")
	asJSON := fs.Bool("json", false, "print the plan as JSON")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *code == "" || *name == "" {
		return errUsage("plans create needs --code and --name")
	}
	client, err := c.client(*base)
	if err != nil {
		return err
	}
	plan, err := client.Plans.CreatePlan(ctx, &payrudder.CreatePlanRequest{Code: *code, Name: *name})
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(plan)
	}
	c.println("created plan %s (%s); it has no version yet: publish one with POST /v1/plans/%s/versions", plan.Code, plan.Name, plan.Code)
	return nil
}

// webhooksRedeliver sends a delivery again from the start (POST /v1/webhook-deliveries/{id}/redeliver), whatever became of it. The API queues it and
// answers at once; --wait is not offered, `webhooks deliveries --watch` shows how the endpoint answers.
func (c *cli) webhooksRedeliver(ctx context.Context, args []string) error {
	fs := c.flags("webhooks redeliver")
	base := fs.String("base-url", "", "the gateway address")
	asJSON := fs.Bool("json", false, "print the delivery as JSON")
	// The delivery id comes first, as in "redeliver 0192… --json" (flag stops at the first argument that is not a flag).
	var id string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	if id == "" {
		return errUsage("webhooks redeliver needs the delivery's id (see `payrudder webhooks deliveries`)")
	}
	client, err := c.client(*base)
	if err != nil {
		return err
	}
	d, err := client.WebhookDeliveries.RedeliverWebhook(ctx, id)
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(d)
	}
	c.println("queued delivery %s again (%s, event %s); follow it with `payrudder webhooks deliveries --endpoint %s --watch`", d.ID, d.EventType, d.EventID, d.EndpointID)
	return nil
}
