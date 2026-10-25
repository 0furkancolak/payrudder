package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// replay and rerate drive the operator endpoints of docs/07-correctness.md#8-replay--re-rating.
//
//	payrudderctl replay start   --tenant ID --from 2026-10 [--to 2026-10] --reason "…" [--wait]
//	payrudderctl replay status  --tenant ID --job ID
//	payrudderctl replay promote --tenant ID --job ID
//	payrudderctl replay discard --tenant ID --job ID
//	payrudderctl rerate         --tenant ID --period 2026-10 --plan-version ID [--wait]
func replay(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: payrudderctl replay start|status|promote|discard --tenant ID [flags]")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("replay "+sub, flag.ContinueOnError)
	gateway := fs.String("gateway", envOr("PAYRUDDER_GATEWAY_URL", "http://localhost:42800"), "gateway base URL")
	token := fs.String("operator-token", getenv("PAYRUDDER_OPERATOR_TOKEN"), "operator token (PAYRUDDER_OPERATOR_TOKEN)")
	tenantID := fs.String("tenant", "", "tenant id (required)")
	job := fs.String("job", "", "replay job id")
	from := fs.String("from", "", "first period to rebuild, e.g. 2026-10")
	to := fs.String("to", "", "last period (default: --from)")
	reason := fs.String("reason", "", "why (required for start)")
	wait := fs.Bool("wait", false, "start: wait until the job is ready (or has failed) and print the difference")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if *tenantID == "" || *token == "" {
		return fmt.Errorf("--tenant and --operator-token (or PAYRUDDER_OPERATOR_TOKEN) are required")
	}
	c := &client{base: strings.TrimRight(*gateway, "/"), http: &http.Client{Timeout: 30 * time.Second}}
	base := "/admin/v1/tenants/" + *tenantID + "/replays"
	var out map[string]any
	var err error
	switch sub {
	case "start":
		if *from == "" || *reason == "" {
			return fmt.Errorf("start needs --from and --reason")
		}
		err = c.call("POST", base, *token, map[string]any{"from_period_id": *from, "to_period_id": *to, "reason": *reason}, &out)
		if err == nil && *wait {
			out, err = waitReplay(c, *token, base, out["id"].(string))
		}
	case "status", "promote", "discard":
		if *job == "" {
			return fmt.Errorf("%s needs --job", sub)
		}
		method, path := "GET", base+"/"+*job
		if sub != "status" {
			method, path = "POST", path+"/"+sub
		}
		err = c.call(method, path, *token, nil, &out)
	default:
		return fmt.Errorf("unknown replay command %q", sub)
	}
	if err != nil {
		return err
	}
	return printJSON(out)
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, string(b))
	return nil
}

func waitReplay(c *client, token, base, id string) (map[string]any, error) {
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		var out map[string]any
		if err := c.call("GET", base+"/"+id, token, nil, &out); err != nil {
			return nil, err
		}
		switch out["status"] {
		case "REPLAY_STATUS_READY", "REPLAY_STATUS_PROMOTED":
			return out, nil
		case "REPLAY_STATUS_FAILED", "REPLAY_STATUS_DISCARDED":
			return out, fmt.Errorf("the job ended %v: %v", out["status"], out["last_error"])
		}
		fmt.Fprintf(os.Stderr, "replay %s: %v\n", id, out["status"])
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("the replay did not become ready within 30 minutes")
}

func rerate(args []string) error {
	fs := flag.NewFlagSet("rerate", flag.ContinueOnError)
	gateway := fs.String("gateway", envOr("PAYRUDDER_GATEWAY_URL", "http://localhost:42800"), "gateway base URL")
	token := fs.String("operator-token", getenv("PAYRUDDER_OPERATOR_TOKEN"), "operator token (PAYRUDDER_OPERATOR_TOKEN)")
	tenantID := fs.String("tenant", "", "tenant id (required)")
	period := fs.String("period", "", "closed period to price again, e.g. 2026-10 (required)")
	pv := fs.String("plan-version", "", "plan version id to price under (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tenantID == "" || *period == "" || *pv == "" || *token == "" {
		return fmt.Errorf("--tenant, --period, --plan-version and --operator-token (or PAYRUDDER_OPERATOR_TOKEN) are required")
	}
	c := &client{base: strings.TrimRight(*gateway, "/"), http: &http.Client{Timeout: 5 * time.Minute}}
	var out map[string]any
	if err := c.call("POST", "/admin/v1/tenants/"+*tenantID+"/billing-runs/"+*period+"/rerating", *token, map[string]any{"plan_version_id": *pv}, &out); err != nil {
		return err
	}
	return printJSON(out)
}
