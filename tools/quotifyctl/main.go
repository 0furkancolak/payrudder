// Command quotifyctl is the operator CLI. It talks to the public gateway API only, like any client would.
//
//	quotifyctl seed --currency TRY --timezone Europe/Istanbul --grace 24h
//
// dlq is the exception: it talks to RabbitMQ directly. Later phases add replay, rerate and close-period subcommands.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "seed":
		err = seed(os.Args[2:])
	case "dlq":
		err = dlq(os.Args[2:])
	case "demo":
		err = demo(os.Args[2:])
	case "replay":
		err = replay(os.Args[2:])
	case "rerate":
		err = rerate(os.Args[2:])
	case "rename":
		err = rename(os.Args[2:])
	case "dev":
		err = dev(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "quotifyctl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: quotifyctl <command> [flags]

commands:
  seed    create a demo account (sandbox + live), API keys, meters, a plan and customers
  demo    the whole story against a running stack: usage, a period close, a price change, a correction invoice
  replay  start | status | promote | discard: rebuild a tenant's aggregates from the event log into a new generation
  rerate  price a closed period again under another plan version and bill the difference
  dev     run the services on the host against the infrastructure containers, restarting on change (--only, --skip, --list)
  rename  --name "Acme Billing" [--apply]: list (or make) the changes that rename the product through the whole repository
  dlq     ls | show | redrive | purge the dead-letter queues (--broker rabbitmq, default) or ls | show | redrive the DLQ topics (--broker kafka)`)
}

type client struct {
	base  string
	token string
	http  *http.Client
}

// call sends a JSON request and decodes the JSON response into out (if non-nil).
func (c *client) call(method, path, token string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, body) //nolint:gosec // an operator tool: the URL is the operator's own --gateway flag
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req) //nolint:gosec // see above
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type tenantJSON struct {
	ID          string `json:"id"`
	Environment string `json:"environment"`
}

type seedResult struct {
	AccountID string            `json:"account_id"`
	Tenants   map[string]string `json:"tenants"`
	APIKeys   map[string]string `json:"api_keys"`
}

// sandboxScenarios are assigned round-robin to sandbox customers, so every payment path is exercised.
var sandboxScenarios = []string{"success", "declined", "insufficient_funds_once", "error_then_success", "timeout_committed", "timeout_not_committed"}

func seed(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	gateway := fs.String("gateway", envOr("QUOTIFY_GATEWAY_URL", "http://localhost:8000"), "gateway base URL")
	token := fs.String("operator-token", os.Getenv("QUOTIFY_OPERATOR_TOKEN"), "operator token (QUOTIFY_OPERATOR_TOKEN)")
	name := fs.String("name", "Demo Co", "account name")
	currency := fs.String("currency", "", "ISO 4217 currency (required, e.g. TRY)")
	timezone := fs.String("timezone", "", "IANA time zone (required, e.g. Europe/Istanbul)")
	grace := fs.Duration("grace", -1, "grace period (required, e.g. 24h)")
	tier := fs.String("tier", "standard", "free | standard | enterprise")
	customers := fs.Int("customers", 10, "customers per tenant")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// The account settings are a deliberate choice (ADR 0011): no defaults here either.
	var missing []string
	if *currency == "" {
		missing = append(missing, "--currency")
	}
	if *timezone == "" {
		missing = append(missing, "--timezone")
	}
	if *grace < 0 {
		missing = append(missing, "--grace")
	}
	if *token == "" {
		missing = append(missing, "--operator-token")
	}
	if len(missing) > 0 {
		return fmt.Errorf("seed: missing %s", strings.Join(missing, ", "))
	}

	c := &client{base: strings.TrimRight(*gateway, "/"), token: *token, http: &http.Client{Timeout: 15 * time.Second}}
	res, err := seedAccount(c, *name, *currency, *timezone, *grace, *tier, *customers)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

// seedAccount creates an account (sandbox and live tenants), an API key for each, and the demo catalog.
func seedAccount(c *client, name, currency, timezone string, grace time.Duration, tier string, customers int) (*seedResult, error) {
	var acct struct {
		ID      string       `json:"id"`
		Tenants []tenantJSON `json:"tenants"`
	}
	err := c.call("POST", "/admin/v1/accounts", c.token, map[string]any{
		"name": name, "currency": currency, "timezone": timezone,
		"grace_period": fmt.Sprintf("%ds", int64(grace.Seconds())), "tier": "TENANT_TIER_" + strings.ToUpper(tier),
	}, &acct)
	if err != nil {
		return nil, err
	}
	res := &seedResult{AccountID: acct.ID, Tenants: map[string]string{}, APIKeys: map[string]string{}}
	for _, t := range acct.Tenants {
		env := strings.ToLower(strings.TrimPrefix(t.Environment, "ENVIRONMENT_"))
		var key struct {
			Plaintext string `json:"plaintext"`
		}
		if err := c.call("POST", "/admin/v1/tenants/"+t.ID+"/api-keys", c.token, nil, &key); err != nil {
			return nil, err
		}
		res.Tenants[env], res.APIKeys[env] = t.ID, key.Plaintext
		if err := seedCatalog(c, key.Plaintext, env, customers); err != nil {
			return nil, fmt.Errorf("seed %s: %w", env, err)
		}
	}
	return res, nil
}

func seedCatalog(c *client, key, env string, customers int) error {
	steps := []struct {
		path string
		body map[string]any
	}{
		{"/v1/meters", map[string]any{"code": "ai_tokens", "aggregation": "AGGREGATION_SUM", "value_property": "tokens",
			"quantity_scale": 0, "unit_label": "tokens"}},
		{"/v1/meters", map[string]any{"code": "gb_hours", "aggregation": "AGGREGATION_SUM", "value_property": "gb_h",
			"quantity_scale": 3, "unit_label": "GB-h"}},
		{"/v1/plans", map[string]any{"code": "pro", "name": "Pro"}},
		{"/v1/plans/pro/versions", map[string]any{
			"base_fee_minor": "9900",
			"prices": []any{
				map[string]any{"meter_code": "ai_tokens", "tiered": map[string]any{"tiers": []any{
					map[string]any{"up_to": "100000", "unit_price": "0"},
					map[string]any{"unit_price": "0.002"},
				}}},
				map[string]any{"meter_code": "gb_hours", "per_unit": map[string]any{"unit_price": "12.5"}},
			},
			"entitlements": []any{
				map[string]any{"meter_code": "ai_tokens", "limit": "1000000", "alert_thresholds_pct": []int{80, 100}},
				map[string]any{"meter_code": "gb_hours", "limit": "500.5"},
			},
		}},
	}
	for _, s := range steps {
		if err := c.call("POST", s.path, key, s.body, nil); err != nil {
			return err
		}
	}
	for i := 1; i <= customers; i++ {
		ext := fmt.Sprintf("cus_%03d", i)
		body := map[string]any{"external_id": ext, "name": fmt.Sprintf("Customer %d", i), "email": fmt.Sprintf("customer%d@example.com", i)}
		if env == "sandbox" {
			body["payment_method"] = "pm_sandbox_" + sandboxScenarios[(i-1)%len(sandboxScenarios)]
		}
		if err := c.call("POST", "/v1/customers", key, body, nil); err != nil {
			return err
		}
		if err := c.call("POST", "/v1/subscriptions", key, map[string]any{"customer_external_id": ext, "plan_code": "pro", "fee_timing": "FEE_TIMING_ARREARS"}, nil); err != nil {
			return err
		}
	}
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
