package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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

// plansCreate makes the plan itself: a code and a name. Its prices and entitlements are a version, which `plans publish` reads from a file: a version is
// immutable and its prices are a document, better written in a file under review than in flags.
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
	c.println("created plan %s (%s); it has no version yet: publish one with `payrudder plans publish --code %s -f version.json`", plan.Code, plan.Name, plan.Code)
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

// The four commands below make things that no key deduplicates: a repeated meter or plan version is a new version, a repeated endpoint a second endpoint. The SDK
// retries a POST after a connection error whatever it is, so these turn its retries off: a call that failed midway is shown as a failure, and the person looks
// (`webhooks list`, the console) before running it again. docs-site/content/idempotency.md says the same of every write without a key.
var noRetry = payrudder.WithRequestMaxRetries(0)

// enumName spells an API enum from what a person types: "month" is SUBSCRIPTION_INTERVAL_MONTH, and the full name is accepted as it is.
func enumName(prefix, v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	if v == "" || strings.HasPrefix(v, prefix) {
		return v
	}
	return prefix + v
}

// webhooksCreate registers an endpoint (POST /v1/webhook-endpoints). The signing secret is in this answer only; it is printed once and cannot be read again.
func (c *cli) webhooksCreate(ctx context.Context, args []string) error {
	fs := c.flags("webhooks create")
	base := fs.String("base-url", "", "the gateway address")
	endpointURL := fs.String("url", "", "the https address Payrudder posts to (required; public, no credentials)")
	description := fs.String("description", "", "a note shown beside the endpoint in the console")
	events := fs.String("events", "", "the event types it receives, comma separated (default: all)")
	asJSON := fs.Bool("json", false, "print the endpoint as JSON, with its secret")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *endpointURL == "" {
		return errUsage("webhooks create needs --url")
	}
	req := &payrudder.CreateWebhookEndpointRequest{URL: *endpointURL, Description: *description}
	if *events != "" {
		for _, e := range strings.Split(*events, ",") {
			req.EventTypes = append(req.EventTypes, strings.TrimSpace(e))
		}
	}
	client, err := c.client(*base)
	if err != nil {
		return err
	}
	ep, err := client.WebhookEndpoints.CreateWebhookEndpoint(ctx, req, noRetry)
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(ep)
	}
	types := "all events"
	if len(ep.EventTypes) > 0 {
		types = strings.Join(ep.EventTypes, ",")
	}
	c.println("created endpoint %s for %s (%s)", ep.ID, ep.URL, types)
	c.println("signing secret (shown only now, store it): %s", ep.Secret)
	return nil
}

// filters is a repeatable --filter property=value1,value2 flag: the meter counts only events whose property has one of the values.
type filters map[string]payrudder.CreateMeterRequestFilterValue

func (f filters) String() string {
	return fmt.Sprint(map[string]payrudder.CreateMeterRequestFilterValue(f))
}

func (f filters) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok || k == "" || v == "" {
		return fmt.Errorf("a filter is property=value[,value...]")
	}
	f[k] = payrudder.CreateMeterRequestFilterValue{Values: strings.Split(v, ",")}
	return nil
}

// metersCreate makes a meter, or the next version of the code (POST /v1/meters answers 201 either way).
func (c *cli) metersCreate(ctx context.Context, args []string) error {
	fs := c.flags("meters create")
	base := fs.String("base-url", "", "the gateway address")
	code := fs.String("code", "", "the meter's code, which events name (required)")
	aggregation := fs.String("aggregation", "", "how a period's events combine: sum, count, max, unique or last (required)")
	unit := fs.String("unit", "", "the unit label shown on invoices, for example tokens or GB-h (required)")
	valueProperty := fs.String("value-property", "", "the event property that holds the quantity (sum, max, last) or whose distinct values are counted (unique)")
	scale := fs.Int("scale", -1, "decimal places of the quantity, fixed per code (default: the API's)")
	category := fs.String("category", "", "a group to file the meter under; changes nothing in billing")
	asJSON := fs.Bool("json", false, "print the meter as JSON")
	filter := filters{}
	fs.Var(filter, "filter", "count only events whose property has one of the values, property=value[,value...] (repeatable)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *code == "" || *aggregation == "" || *unit == "" {
		return errUsage("meters create needs --code, --aggregation and --unit")
	}
	req := &payrudder.CreateMeterRequest{
		Code: *code, Aggregation: enumName("AGGREGATION_", *aggregation), UnitLabel: *unit, ValueProperty: *valueProperty, Category: *category,
	}
	if *scale >= 0 {
		req.QuantityScale = scale
	}
	if len(filter) > 0 {
		req.Filter = filter
	}
	client, err := c.client(*base)
	if err != nil {
		return err
	}
	m, err := client.Meters.CreateMeter(ctx, req, noRetry)
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(m)
	}
	c.println("meter %s is at version %d (%s, unit %s)", m.Code, m.Version, strings.ToLower(strings.TrimPrefix(m.Aggregation, "AGGREGATION_")), m.UnitLabel)
	return nil
}

// subscriptionsCreate subscribes a customer to a plan version (POST /v1/subscriptions). A customer has at most one overlapping subscription, so a repeat of
// the command is the API's 409, not a second subscription (and the SDK's retries stay on).
func (c *cli) subscriptionsCreate(ctx context.Context, args []string) error {
	fs := c.flags("subscriptions create")
	base := fs.String("base-url", "", "the gateway address")
	customer := fs.String("customer", "", "the customer's external id (required)")
	plan := fs.String("plan", "", "the plan's code (required)")
	version := fs.Int("plan-version", 0, "the plan version (default: the latest)")
	interval := fs.String("interval", "", "month or year (default: month)")
	currency := fs.String("currency", "", "ISO 4217 currency (default: the customer's, then the tenant's)")
	trial := fs.Int("trial-days", -1, "trial days; 0 means none (default: the version's own trial)")
	quantity := fs.Int("quantity", 0, "seats: the fixed fee is multiplied by it (default 1)")
	timing := fs.String("fee-timing", "", "advance or arrears (default: advance)")
	provider := fs.String("provider", "", "the payment provider that sells it (default: the tenant's default account)")
	coupon := fs.String("coupon", "", "a coupon code to redeem on the new subscription")
	start := fs.String("start-time", "", "an RFC 3339 start time (default: now)")
	asJSON := fs.Bool("json", false, "print the subscription as JSON")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *customer == "" || *plan == "" {
		return errUsage("subscriptions create needs --customer and --plan")
	}
	req := &payrudder.CreateSubscriptionRequest{
		CustomerExternalID: *customer, PlanCode: *plan, Interval: enumName("SUBSCRIPTION_INTERVAL_", *interval), Currency: *currency,
		FeeTiming: enumName("FEE_TIMING_", *timing), Provider: *provider, CouponCode: *coupon,
	}
	if *version > 0 {
		req.PlanVersion = version
	}
	if *trial >= 0 {
		req.TrialDays = trial
	}
	if *quantity > 0 {
		req.Quantity = quantity
	}
	if *start != "" {
		t, err := time.Parse(time.RFC3339, *start)
		if err != nil {
			return errUsage("--start-time must be an RFC 3339 time such as 2026-11-02T10:00:00Z")
		}
		req.StartTime = &t
	}
	client, err := c.client(*base)
	if err != nil {
		return err
	}
	sub, err := client.Subscriptions.CreateSubscription(ctx, req)
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(sub)
	}
	c.println("subscribed %s to %s version %d: subscription %s, %s, %s", sub.CustomerExternalID, sub.PlanCode, sub.PlanVersion, sub.ID,
		strings.ToLower(strings.TrimPrefix(sub.State, "SUBSCRIPTION_STATE_")), strings.ToLower(strings.TrimPrefix(sub.Interval, "SUBSCRIPTION_INTERVAL_")))
	return nil
}

// plansPublish publishes the next version of a plan (POST /v1/plans/{code}/versions) from a JSON file, the body of that request as the API reference
// describes it ("-f -" reads the standard input). A version is immutable and cannot be undone, so an unknown field (a typo of base_fee_minor would otherwise
// publish a free version) and anything after the document are refused before the request is made.
func (c *cli) plansPublish(ctx context.Context, args []string) error {
	fs := c.flags("plans publish")
	base := fs.String("base-url", "", "the gateway address")
	code := fs.String("code", "", "the plan's code (required)")
	var file string
	fs.StringVar(&file, "f", "", "the JSON file with the version, or - for the standard input (required)")
	fs.StringVar(&file, "file", "", "same as -f")
	asJSON := fs.Bool("json", false, "print the version as JSON")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *code == "" || file == "" {
		return errUsage("plans publish needs --code and -f")
	}
	var req payrudder.PublishPlanVersionRequest
	if err := c.readVersion(file, &req); err != nil {
		return err
	}
	client, err := c.client(*base)
	if err != nil {
		return err
	}
	v, err := client.Plans.PublishPlanVersion(ctx, *code, &req, noRetry)
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(v)
	}
	c.println("published version %d of plan %s (base fee %d minor units, %d prices, %d entitlements)", v.Version, v.PlanCode, v.BaseFeeMinor, len(v.Prices), len(v.Entitlements))
	return nil
}

func (c *cli) readVersion(file string, req *payrudder.PublishPlanVersionRequest) error {
	r := c.in
	if file != "-" {
		f, err := os.Open(file) //nolint:gosec // the file the person named on the command line
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, payrudder.MaxRequestBytes+1))
	if err != nil {
		return err
	}
	if len(b) > payrudder.MaxRequestBytes {
		return fmt.Errorf("%s is larger than the %d bytes the API accepts", file, payrudder.MaxRequestBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(req); err != nil {
		return fmt.Errorf("%s is not a plan version: %w", file, err)
	}
	if dec.More() {
		return fmt.Errorf("%s has more than one JSON document", file)
	}
	return nil
}
