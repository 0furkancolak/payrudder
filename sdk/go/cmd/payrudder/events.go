package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

// properties is a repeatable --property key=value flag.
type properties map[string]string

func (p properties) String() string { return fmt.Sprint(map[string]string(p)) }

func (p properties) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok || k == "" {
		return errors.New("a property is key=value")
	}
	p[k] = v
	return nil
}

// eventsSend sends one usage event through Events.Ingest, which gives it an idempotency key when --idempotency-key is not set (so a repeated run with the
// same key never counts twice, and the automatic retries of a failed call never do).
func (c *cli) eventsSend(ctx context.Context, args []string) error {
	fs := c.flags("events send")
	base := fs.String("base-url", "", "the gateway address")
	customer := fs.String("customer", "", "the customer's external id (required)")
	meter := fs.String("meter", "", "the meter code (required)")
	quantity := fs.String("quantity", "1", "the quantity: a non-negative decimal with at most the meter's decimal places")
	at := fs.String("time", "now", "when it happened: now or an RFC 3339 time (not older than 35 days, not more than 5 minutes ahead)")
	key := fs.String("idempotency-key", "", "an idempotency key (default: one is generated; live keys require one)")
	asJSON := fs.Bool("json", false, "print the API's result as JSON")
	props := properties{}
	fs.Var(props, "property", "a property, key=value (repeatable; at most 32 entries and 4 KiB)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *customer == "" || *meter == "" {
		return errUsage("events send needs --customer and --meter")
	}
	when := time.Now()
	if *at != "now" {
		t, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			return errUsage("--time must be now or an RFC 3339 time such as 2026-11-02T10:00:00Z")
		}
		when = t
	}
	client, err := c.client(*base)
	if err != nil {
		return err
	}
	ev := payrudder.IncomingEvent{
		IdempotencyKey: *key, CustomerExternalID: *customer, MeterCode: *meter, Quantity: payrudder.Decimal(*quantity), EventTime: when,
	}
	if len(props) > 0 {
		ev.Properties = props
	}
	results, err := client.Events.Ingest(ctx, []payrudder.IncomingEvent{ev})
	if err != nil {
		return err
	}
	r := results[0]
	if *asJSON {
		if err := c.printJSON(r); err != nil {
			return err
		}
	} else {
		switch r.Status {
		case payrudder.EventAccepted:
			c.println("accepted   event %s", r.EventID)
		case payrudder.EventDuplicate:
			c.println("duplicate  event %s (this idempotency key was counted before; nothing was added)", r.EventID)
		default:
			c.println("rejected   %s: %s", r.ErrorCode, r.ErrorMessage)
		}
	}
	if r.Status == payrudder.EventRejected {
		return errFailed
	}
	return nil
}

// eventsTail follows a customer's usage. The API has no call that lists events (they are counted, not kept for reading), so this polls the usage totals of the
// period and prints a line whenever a meter's count moves. It stops after --count polls or on Ctrl-C.
func (c *cli) eventsTail(ctx context.Context, args []string) error {
	fs := c.flags("events tail")
	base := fs.String("base-url", "", "the gateway address")
	customer := fs.String("customer", "", "the customer's external id (required)")
	period := fs.String("period", "", "the billing period, YYYY-MM (default: the current one)")
	every := fs.Duration("interval", 2*time.Second, "how often to ask")
	count := fs.Int("count", 0, "stop after this many polls (default: until interrupted)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *customer == "" {
		return errUsage("events tail needs --customer")
	}
	client, err := c.client(*base)
	if err != nil {
		return err
	}
	seen := map[string]int64{}
	first := true
	for polls := 1; ; polls++ {
		u, err := client.Customers.GetCustomerUsage(ctx, *customer, &payrudder.GetCustomerUsageParams{Period: *period})
		if err != nil {
			return err
		}
		stamp := time.Now().Format("15:04:05")
		if first {
			c.println("%s  usage of %s in %s", stamp, u.CustomerExternalID, u.PeriodID)
			if len(u.Usage) == 0 {
				c.println("%s  no usage yet; waiting for events", stamp)
			}
		}
		for _, m := range u.Usage {
			n, err := parseCount(m.EventCount)
			if err != nil {
				return err
			}
			prev, known := seen[m.MeterCode]
			seen[m.MeterCode] = n
			switch {
			case first:
				c.println("%s  %-24s %s (%d events)", stamp, m.MeterCode, m.Quantity, n)
			case !known:
				c.println("%s  %-24s %s (+%d events, new meter)", stamp, m.MeterCode, m.Quantity, n)
			case n != prev:
				c.println("%s  %-24s %s (%+d events)", stamp, m.MeterCode, m.Quantity, n-prev)
			}
		}
		first = false
		if *count > 0 && polls >= *count {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(*every):
		}
	}
}

func parseCount(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("the API sent an event count that is not a number: %q", s)
	}
	return n, nil
}
