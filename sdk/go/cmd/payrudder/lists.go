package main

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

func (c *cli) table(header string, rows [][]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, header)
	for _, r := range rows {
		for i, cell := range r {
			if i > 0 {
				fmt.Fprint(w, "\t")
			}
			fmt.Fprint(w, cell)
		}
		fmt.Fprintln(w)
	}
	_ = w.Flush()
}

func day(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.UTC().Format("2006-01-02")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// customersList prints the first --limit customers (ordered by external id, as the API does).
func (c *cli) customersList(ctx context.Context, args []string) error {
	fs := c.flags("customers list")
	base := fs.String("base-url", "", "the gateway address")
	limit := fs.Int("limit", 25, "how many customers to show at most")
	asJSON := fs.Bool("json", false, "print them as JSON")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *limit < 1 {
		return errUsage("--limit must be at least 1")
	}
	client, err := c.client(*base)
	if err != nil {
		return err
	}
	list := []payrudder.Customer{}
	for cust, err := range client.Customers.ListCustomersAll(ctx, &payrudder.ListCustomersParams{PageSize: payrudder.Ptr(min(*limit, 500))}) {
		if err != nil {
			return err
		}
		list = append(list, cust)
		if len(list) == *limit {
			break
		}
	}
	if *asJSON {
		return c.printJSON(list)
	}
	rows := make([][]string, len(list))
	for i, x := range list {
		blocked := ""
		if x.BlockTime != nil {
			blocked = "blocked"
		}
		rows[i] = []string{x.ExternalID, dash(x.Name), dash(x.Email), dash(x.Country), dash(x.BillingCurrency), day(x.CreateTime), blocked}
	}
	c.table("EXTERNAL ID\tNAME\tEMAIL\tCOUNTRY\tCURRENCY\tCREATED\tBLOCKED", rows)
	return nil
}

// invoicesList prints the first --limit invoices (ordered by id, as the API does).
func (c *cli) invoicesList(ctx context.Context, args []string) error {
	fs := c.flags("invoices list")
	base := fs.String("base-url", "", "the gateway address")
	customer := fs.String("customer", "", "only this customer's invoices (external id)")
	status := fs.String("status", "", "only this status: draft, open, paid, uncollectible or void")
	period := fs.String("period", "", "only this billing period, YYYY-MM")
	limit := fs.Int("limit", 25, "how many invoices to show at most")
	asJSON := fs.Bool("json", false, "print them as JSON")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *limit < 1 {
		return errUsage("--limit must be at least 1")
	}
	client, err := c.client(*base)
	if err != nil {
		return err
	}
	list := []payrudder.Invoice{}
	params := &payrudder.ListInvoicesParams{Customer: *customer, Status: *status, Period: *period, PageSize: payrudder.Ptr(min(*limit, 500))}
	for inv, err := range client.Invoices.ListInvoicesAll(ctx, params) {
		if err != nil {
			return err
		}
		list = append(list, inv)
		if len(list) == *limit {
			break
		}
	}
	if *asJSON {
		return c.printJSON(list)
	}
	rows := make([][]string, len(list))
	for i, x := range list {
		rows[i] = []string{dash(x.Number), x.CustomerExternalID, x.PeriodID, x.Status, x.TotalMinor.String() + " " + x.Currency, day(x.CreateTime), x.ID}
	}
	// The total is in the currency's minor units (cents, kurus), as the API sends it.
	c.table("NUMBER\tCUSTOMER\tPERIOD\tSTATUS\tTOTAL (MINOR UNITS)\tCREATED\tID", rows)
	return nil
}
