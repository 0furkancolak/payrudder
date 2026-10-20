package main

import (
	"flag"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// demo plays the story of the README against a running stack (make up): a new account with six customers, one per
// payment scenario; a month of usage; a period close with real workers; a price increase; and the correction invoice that
// re-rating bills for the difference. Everything goes through the public API and the operator endpoints, as an operator would.
func demo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	gateway := fs.String("gateway", envOr("QUOTIFY_GATEWAY_URL", "http://localhost:8000"), "gateway base URL")
	token := fs.String("operator-token", os.Getenv("QUOTIFY_OPERATOR_TOKEN"), "operator token (QUOTIFY_OPERATOR_TOKEN)")
	mailpit := fs.String("mailpit", envOr("MAILPIT_URL", "http://localhost:8025"), "Mailpit URL")
	events := fs.Int("events", 600, "usage events to send")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *token == "" {
		return fmt.Errorf("--operator-token (or QUOTIFY_OPERATOR_TOKEN) is required")
	}
	d := &demoRun{c: &client{base: strings.TrimRight(*gateway, "/"), token: *token, http: &http.Client{Timeout: 30 * time.Second}}}

	step("1/7 A new account: sandbox and live tenants, a plan, six customers (one per payment scenario)")
	if err := d.seed(); err != nil {
		return err
	}
	step("2/7 Usage: %d events (AI tokens and GB-hours) through POST /v1/events, 5 %% of them sent twice", *events)
	if err := d.sendUsage(*events); err != nil {
		return err
	}
	step("3/7 Close the period now (sandbox ClosePeriodNow): watermark, rating, invoice numbers, the saga with real workers")
	if err := d.closePeriod(); err != nil {
		return err
	}
	step("4/7 A price change: a new version of the plan, 20 %% dearer base fee and GB-hours")
	if err := d.changePrice(); err != nil {
		return err
	}
	step("5/7 Re-rating %s under the new version: the difference becomes one CORRECTION invoice per customer", d.period)
	if err := d.rerate(); err != nil {
		return err
	}
	step("6/7 The correction invoices go through the same saga: PDF, charge, receipt")
	if err := d.waitCorrections(); err != nil {
		return err
	}
	step("7/7 Where to look")
	printPointers(*mailpit)
	return nil
}

// demoRun carries what the steps of the demo hand to each other.
type demoRun struct {
	c      *client
	tenant string // sandbox tenant
	key    string // its API key
	period string // the period closed in step 3
	pvID   string // the plan version created in step 4
}

func step(format string, a ...any) { fmt.Fprintf(os.Stdout, "\n\033[1m==> "+format+"\033[0m\n", a...) }

func (d *demoRun) seed() error {
	res, err := seedAccount(d.c, fmt.Sprintf("Demo %s", time.Now().Format("150405")), "TRY", "Europe/Istanbul", 24*time.Hour, "standard", 6)
	if err != nil {
		return err
	}
	d.tenant, d.key = res.Tenants["sandbox"], res.APIKeys["sandbox"]
	fmt.Fprintf(os.Stdout, "sandbox tenant %s\n", d.tenant)
	return nil
}

func (d *demoRun) sendUsage(events int) error {
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // demo data, not security
	at := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	var batch []map[string]any
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := d.c.call("POST", "/v1/events", d.key, map[string]any{"events": batch}, nil)
		batch = batch[:0]
		return err
	}
	for i := 0; i < events; i++ {
		meter, qty := "ai_tokens", fmt.Sprint(500+rng.IntN(1500))
		if i%3 == 0 {
			meter, qty = "gb_hours", fmt.Sprintf("%d.%03d", rng.IntN(3), rng.IntN(1000))
		}
		e := map[string]any{"idempotency_key": fmt.Sprintf("demo-%d", i), "customer_external_id": fmt.Sprintf("cus_%03d", 1+i%6),
			"meter_code": meter, "quantity": qty, "event_time": at}
		batch = append(batch, e)
		if rng.IntN(100) < 5 {
			batch = append(batch, e) // a client retry: same key, same payload
		}
		if len(batch) >= 100 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

func (d *demoRun) closePeriod() error {
	var run map[string]any
	if err := d.c.call("POST", "/v1/sandbox/close-period", d.key, nil, &run); err != nil {
		return err
	}
	d.period, _ = run["period_id"].(string)
	fmt.Fprintf(os.Stdout, "closing %s\n", d.period)
	if err := waitFor(5*time.Minute, "every invoice saga to reach a final state", d.allInvoicesFinal); err != nil {
		return err
	}
	// The run completes on the scheduler's next look at it; re-rating needs it COMPLETED.
	if err := waitFor(2*time.Minute, "the billing run to complete", d.runCompleted); err != nil {
		return err
	}
	printInvoices(d.c, d.key, d.period)
	return nil
}

func isFinalSaga(inv map[string]any) bool {
	s, _ := inv["saga_state"].(string)
	return s == "COMPLETED" || s == "VOIDED"
}

func (d *demoRun) allInvoicesFinal() (bool, string) {
	var list struct {
		Invoices []map[string]any `json:"invoices"`
	}
	if err := d.c.call("GET", "/v1/invoices?page_size=100", d.key, nil, &list); err != nil || len(list.Invoices) < 6 {
		return false, "waiting for the invoices"
	}
	final := 0
	for _, inv := range list.Invoices {
		if isFinalSaga(inv) {
			final++
		}
	}
	return final == len(list.Invoices), fmt.Sprintf("%d of %d invoices final", final, len(list.Invoices))
}

func (d *demoRun) runCompleted() (bool, string) {
	var br map[string]any
	if err := d.c.call("GET", "/admin/v1/tenants/"+d.tenant+"/billing-runs/"+d.period, d.c.token, nil, &br); err != nil {
		return false, err.Error()
	}
	st, _ := br["status"].(string)
	return strings.HasSuffix(st, "COMPLETED"), "billing run " + st
}

func (d *demoRun) changePrice() error {
	var pv map[string]any
	if err := d.c.call("POST", "/v1/plans/pro/versions", d.key, map[string]any{
		"base_fee_minor": "11900",
		"prices": []any{
			map[string]any{"meter_code": "ai_tokens", "tiered": map[string]any{"tiers": []any{
				map[string]any{"up_to": "100000", "unit_price": "0"}, map[string]any{"unit_price": "0.002"}}}},
			map[string]any{"meter_code": "gb_hours", "per_unit": map[string]any{"unit_price": "15"}},
		},
	}, &pv); err != nil {
		return err
	}
	d.pvID, _ = pv["id"].(string)
	fmt.Fprintf(os.Stdout, "plan version %v (id %s)\n", pv["version"], d.pvID)
	return nil
}

func (d *demoRun) rerate() error {
	path := "/admin/v1/tenants/" + d.tenant + "/billing-runs/" + d.period + "/rerating"
	var job map[string]any
	if err := d.c.call("POST", path, d.c.token, map[string]any{"plan_version_id": d.pvID}, &job); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "job %v: %v customers rated, %v corrections totalling %v minor units\n", job["id"], job["customers_rated"], job["corrections"], job["correction_total_minor"])
	if err := d.c.call("POST", path, d.c.token, map[string]any{"plan_version_id": d.pvID}, &job); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "asking again returns the same job (%v) and changes nothing\n", job["id"])
	return nil
}

func (d *demoRun) waitCorrections() error {
	if err := waitFor(3*time.Minute, "the correction sagas", d.correctionsFinal); err != nil {
		return err
	}
	printInvoices(d.c, d.key, d.period)
	return nil
}

func (d *demoRun) correctionsFinal() (bool, string) {
	var list struct {
		Invoices []map[string]any `json:"invoices"`
	}
	if err := d.c.call("GET", "/v1/invoices?page_size=100", d.key, nil, &list); err != nil {
		return false, err.Error()
	}
	corr, final := 0, 0
	for _, inv := range list.Invoices {
		if inv["kind"] == "CORRECTION" {
			corr++
			if isFinalSaga(inv) {
				final++
			}
		}
	}
	return corr > 0 && corr == final, fmt.Sprintf("%d of %d correction invoices final", final, corr)
}

// printPointers lists the Mailpit messages and the places in the stack worth opening after the demo.
func printPointers(mailpit string) {
	if mails, err := mailSubjects(mailpit); err == nil {
		fmt.Fprintf(os.Stdout, "Mailpit has %d messages, for example:\n", len(mails))
		for i, s := range mails {
			if i >= 6 {
				break
			}
			fmt.Fprintf(os.Stdout, "  - %s\n", s)
		}
	}
	fmt.Fprintf(os.Stdout, `
  Mailpit (receipts)   %s
  Grafana (dashboards) http://localhost:3000   Quotify / Billing, Pipeline lag, Correctness
  Traces (Tempo)       http://localhost:3000/explore   (data source Tempo)
  MinIO (PDFs)         http://localhost:9002   (user quotify, password quotify_local)
  Redpanda Console     http://localhost:%s
`, mailpit, envOr("QUOTIFY_CONSOLE_HOST_PORT", "8080"))
}

func printInvoices(c *client, key, period string) {
	var list struct {
		Invoices []map[string]any `json:"invoices"`
	}
	if err := c.call("GET", "/v1/invoices?period="+url.QueryEscape(period)+"&page_size=100", key, nil, &list); err != nil {
		fmt.Fprintln(os.Stdout, "(could not list invoices:", err, ")")
		return
	}
	fmt.Fprintf(os.Stdout, "%-18s %-11s %-9s %-14s %s\n", "NUMBER", "KIND", "TOTAL", "STATUS", "SAGA")
	for _, inv := range list.Invoices {
		fmt.Fprintf(os.Stdout, "%-18v %-11v %-9v %-14v %v\n", inv["number"], inv["kind"], inv["total_minor"], inv["status"], inv["saga_state"])
	}
}

func mailSubjects(base string) ([]string, error) {
	var out struct {
		Messages []struct{ Subject string } `json:"messages"`
	}
	c := &client{base: base, http: &http.Client{Timeout: 5 * time.Second}}
	if err := c.call("GET", "/api/v1/messages?limit=20", "", nil, &out); err != nil {
		return nil, err
	}
	var s []string
	for _, m := range out.Messages {
		s = append(s, m.Subject)
	}
	return s, nil
}

func waitFor(within time.Duration, what string, ok func() (bool, string)) error {
	deadline := time.Now().Add(within)
	last := ""
	for time.Now().Before(deadline) {
		done, msg := ok()
		if done {
			fmt.Fprintln(os.Stdout, "done:", msg)
			return nil
		}
		if msg != last {
			fmt.Fprintln(os.Stdout, "  ...", msg)
			last = msg
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out after %s waiting for %s (%s)", within, what, last)
}
