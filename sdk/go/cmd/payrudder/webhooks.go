package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

func (c *cli) webhooksList(ctx context.Context, args []string) error {
	fs := c.flags("webhooks list")
	base := fs.String("base-url", "", "the gateway address")
	asJSON := fs.Bool("json", false, "print them as JSON")
	if err := parse(fs, args); err != nil {
		return err
	}
	client, err := c.client(*base)
	if err != nil {
		return err
	}
	r, err := client.WebhookEndpoints.ListWebhookEndpoints(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(r.Endpoints) // the secret is only in the answer of create and rotate, so there is none to print
	}
	rows := make([][]string, len(r.Endpoints))
	for i, e := range r.Endpoints {
		active := "active"
		if !e.Active {
			active = "paused"
		}
		types := "all events"
		if len(e.EventTypes) > 0 {
			types = strings.Join(e.EventTypes, ",")
		}
		rows[i] = []string{e.ID, e.URL, active, types}
	}
	c.table("ID\tURL\tSTATE\tEVENTS", rows)
	return nil
}

// stateOf is the delivery state without the enum prefix: pending, delivered or failed.
func stateOf(s string) string {
	return strings.ToLower(strings.TrimPrefix(s, "WEBHOOK_DELIVERY_STATE_"))
}

func deliveryLine(d payrudder.WebhookDelivery) string {
	answer := "-"
	if d.LastStatus > 0 {
		answer = "HTTP " + strconv.Itoa(d.LastStatus)
	}
	t := "-"
	if d.CreateTime != nil {
		t = d.CreateTime.Local().Format("15:04:05")
	}
	return fmt.Sprintf("%s  %-30s %-9s attempts %d  %s  %s", t, d.EventType, stateOf(d.State), d.Attempts, answer, d.ID)
}

// webhooksDeliveries lists deliveries, newest first; with --watch it keeps asking and prints each delivery that appears or changes (oldest first), which
// is how you see Payrudder send an event and your endpoint answer.
func (c *cli) webhooksDeliveries(ctx context.Context, args []string) error {
	fs := c.flags("webhooks deliveries")
	base := fs.String("base-url", "", "the gateway address")
	endpoint := fs.String("endpoint", "", "only this endpoint's deliveries (id)")
	state := fs.String("state", "", "only this state: pending, delivered or failed")
	limit := fs.Int("limit", 20, "how many deliveries to ask for (1 to 200)")
	watch := fs.Bool("watch", false, "keep asking and print what is new or changed")
	every := fs.Duration("interval", 2*time.Second, "how often to ask with --watch")
	count := fs.Int("count", 0, "with --watch, stop after this many polls (default: until interrupted)")
	asJSON := fs.Bool("json", false, "print the deliveries as JSON (not with --watch)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *limit < 1 || *limit > 200 {
		return errUsage("--limit is 1 to 200")
	}
	if *watch && *asJSON {
		return errUsage("--json does not combine with --watch")
	}
	client, err := c.client(*base)
	if err != nil {
		return err
	}
	params := &payrudder.ListWebhookDeliveriesParams{EndpointID: *endpoint, State: *state, PageSize: payrudder.Ptr(*limit)}
	list := func() ([]payrudder.WebhookDelivery, error) {
		r, err := client.WebhookDeliveries.ListWebhookDeliveries(ctx, params)
		if err != nil {
			return nil, err
		}
		return r.Deliveries, nil
	}
	ds, err := list()
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(ds)
	}
	seen := map[string]string{}
	show := func(ds []payrudder.WebhookDelivery) {
		for i := len(ds) - 1; i >= 0; i-- { // the API's order is newest first
			d := ds[i]
			sig := fmt.Sprintf("%s/%d/%d", d.State, d.Attempts, d.LastStatus)
			if seen[d.ID] != sig {
				seen[d.ID] = sig
				c.println("%s", deliveryLine(d))
			}
		}
	}
	if len(ds) == 0 && !*watch {
		c.println("no deliveries")
	}
	show(ds)
	if !*watch {
		return nil
	}
	for polls := 1; ; polls++ {
		if *count > 0 && polls >= *count {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(*every):
		}
		if ds, err = list(); err != nil {
			return err
		}
		show(ds)
	}
}

// webhooksTrigger sends the ping event to an endpoint (POST /v1/webhook-endpoints/{id}/test) and waits for the first answer. The API can send nothing
// but ping: every other event happens when the thing it announces happens (an invoice is paid, a subscription is cancelled).
func (c *cli) webhooksTrigger(ctx context.Context, args []string) error {
	fs := c.flags("webhooks trigger")
	base := fs.String("base-url", "", "the gateway address")
	endpoint := fs.String("endpoint", "", "the endpoint's id (default: the only endpoint, if there is just one)")
	wait := fs.Duration("wait", 30*time.Second, "how long to wait for the endpoint's answer")
	every := fs.Duration("interval", time.Second, "how often to ask for it")
	// The event type comes first, as in "trigger ping --endpoint ep1" (flag stops at the first argument that is not a flag).
	var kind string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		kind, args = args[0], args[1:]
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	if kind == "" {
		return errUsage("webhooks trigger needs the event type: payrudder webhooks trigger ping")
	}
	if kind != payrudder.EventPing {
		return errUsage(fmt.Sprintf("the API can send only %q to an endpoint (POST /v1/webhook-endpoints/{id}/test), not %q; other events are sent when they happen", payrudder.EventPing, kind))
	}
	client, err := c.client(*base)
	if err != nil {
		return err
	}
	list, err := client.WebhookEndpoints.ListWebhookEndpoints(ctx)
	if err != nil {
		return err
	}
	var target *payrudder.WebhookEndpoint
	switch {
	case *endpoint != "":
		for i := range list.Endpoints {
			if list.Endpoints[i].ID == *endpoint {
				target = &list.Endpoints[i]
			}
		}
		if target == nil {
			return fmt.Errorf("no endpoint with id %s (see `payrudder webhooks list`)", *endpoint)
		}
	case len(list.Endpoints) == 1:
		target = &list.Endpoints[0]
	case len(list.Endpoints) == 0:
		return errors.New("there is no webhook endpoint yet: register one in the console (or with the API), or use `payrudder webhooks listen --public-url ...`")
	default:
		return errUsage("there are several endpoints; choose one with --endpoint (see `payrudder webhooks list`)")
	}

	d, err := client.WebhookEndpoints.SendTestWebhook(ctx, target.ID)
	if err != nil {
		return err
	}
	c.println("Sent a ping (event %s) to %s; waiting for its answer...", d.EventID, target.URL)
	deadline := time.Now().Add(*wait)
	for {
		r, err := client.WebhookDeliveries.ListWebhookDeliveries(ctx, &payrudder.ListWebhookDeliveriesParams{EndpointID: target.ID, PageSize: payrudder.Ptr(50)})
		if err != nil {
			return err
		}
		for _, x := range r.Deliveries {
			if x.ID != d.ID {
				continue
			}
			switch {
			case stateOf(x.State) == "delivered":
				c.println("Delivered: your endpoint answered HTTP %d.", x.LastStatus)
				return nil
			case x.Attempts > 0:
				answer := "no answer"
				if x.LastStatus > 0 {
					answer = "HTTP " + strconv.Itoa(x.LastStatus)
				}
				c.println("Not accepted: %s%s. Payrudder will try again (1 minute, 5 minutes, 30 minutes, 2 hours, 6 hours, 24 hours).", answer, lastError(x.LastError))
				return errFailed
			}
		}
		if time.Now().After(deadline) {
			c.println("No answer after %s: the delivery is still queued. Check it with `payrudder webhooks deliveries --endpoint %s`.", *wait, target.ID)
			return errFailed
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(*every):
		}
	}
}

func lastError(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return " (" + s + ")"
}

// webhooksListen receives Payrudder's deliveries on this machine. Payrudder pushes (the API does not hand out the delivered bodies, so there is nothing to
// poll), which means the address Payrudder posts to has to reach this machine: a stack on your own computer can post to it directly; a hosted one needs a
// tunnel in front (cloudflared, ngrok, ...) and --public-url, which registers that address as a temporary endpoint, learns its secret and deletes the
// endpoint when the command stops. Each delivery is verified with the secret (--secret or $PAYRUDDER_WEBHOOK_SECRET, or the temporary endpoint's), printed,
// and with --forward-to passed on to your app unchanged, headers and signature included, so your own verification code runs for real; your app's status
// is what Payrudder gets back, so a failing handler is retried like in production.
func (c *cli) webhooksListen(ctx context.Context, args []string) (err error) {
	fs := c.flags("webhooks listen")
	base := fs.String("base-url", "", "the gateway address (needed with --public-url)")
	host := fs.String("host", "127.0.0.1", "the address to listen on (0.0.0.0 to be reachable from a container or another machine)")
	port := fs.Int("port", 4242, "the port to listen on (0: any free port)")
	forward := fs.String("forward-to", "", "pass every verified delivery on to this URL, for example http://localhost:3000/webhooks/payrudder")
	secret := fs.String("secret", "", "the endpoint's signing secret, whsec_... (default $PAYRUDDER_WEBHOOK_SECRET)")
	public := fs.String("public-url", "", "register this public https address (your tunnel) as a temporary endpoint for the length of the command")
	events := fs.String("events", "", "with --public-url, the event types the endpoint receives, comma separated (default: all)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *forward != "" {
		u, perr := url.Parse(*forward)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errUsage("--forward-to must be an http(s) URL")
		}
	}
	whsec := firstNonEmpty(*secret, c.getenv("PAYRUDDER_WEBHOOK_SECRET"))

	if *public != "" {
		client, cerr := c.client(*base)
		if cerr != nil {
			return cerr
		}
		req := &payrudder.CreateWebhookEndpointRequest{URL: *public, Description: "payrudder CLI: webhooks listen (temporary)"}
		if *events != "" {
			req.EventTypes = strings.Split(*events, ",")
		}
		ep, cerr := client.WebhookEndpoints.CreateWebhookEndpoint(ctx, req)
		if cerr != nil {
			return cerr
		}
		whsec = ep.Secret
		c.println("Registered the temporary endpoint %s for %s; it is deleted when this command stops.", ep.ID, *public)
		defer func() {
			// The command's context is cancelled by Ctrl-C, so the clean-up has its own.
			dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if derr := client.WebhookEndpoints.DeleteWebhookEndpoint(dctx, ep.ID); derr != nil {
				fmt.Fprintf(c.err, "payrudder: could not delete the temporary endpoint %s: %v (delete it in the console)\n", ep.ID, derr)
				err = errFailed
				return
			}
			c.println("Deleted the temporary endpoint %s.", ep.ID)
		}()
	}

	lis, lerr := net.Listen("tcp", net.JoinHostPort(*host, strconv.Itoa(*port)))
	if lerr != nil {
		return lerr
	}
	srv := &http.Server{Handler: c.receiver(whsec, *forward), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second}
	c.println("Listening on http://%s for Payrudder deliveries.", lis.Addr())
	switch {
	case whsec == "":
		c.println("No secret: signatures are NOT checked. Pass --secret whsec_... (or --public-url) to verify them.")
	case *forward != "":
		c.println("Verified deliveries are forwarded to %s.", *forward)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(lis) }()
	select {
	case serr := <-done:
		return serr
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}

// receiver handles one delivery: read it, verify it, show it, forward it, and answer with what the app answered.
func (c *cli) receiver(secret, forward string) http.Handler {
	hc := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Payrudder posts its deliveries", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, payrudder.MaxRequestBytes))
		if err != nil {
			http.Error(w, "unreadable body", http.StatusBadRequest)
			return
		}
		kind := r.Header.Get(payrudder.WebhookEventTypeHeader)
		line := fmt.Sprintf("%s  %-30s %s  attempt %s", time.Now().Format("15:04:05"), firstNonEmpty(kind, "(no event type)"), r.Header.Get(payrudder.WebhookEventIDHeader), firstNonEmpty(r.Header.Get(payrudder.WebhookAttemptHeader), "?"))
		if secret == "" {
			line += "  signature not checked"
		} else {
			if verr := payrudder.VerifyWebhook(body, r.Header.Get(payrudder.WebhookSignatureHeader), secret, 0); verr != nil {
				// What a real receiver does: refuse, and do not let it reach the app.
				c.println("%s  REFUSED: %v", line, verr)
				http.Error(w, "signature check failed", http.StatusBadRequest)
				return
			}
			line += "  signature ok"
		}
		if forward == "" {
			c.println("%s", line)
			w.WriteHeader(http.StatusOK)
			return
		}
		status, took, ferr := c.forward(hc, r, body, forward)
		if ferr != nil {
			c.println("%s  -> %s: %v", line, forward, ferr)
			http.Error(w, "the forward target did not answer", http.StatusBadGateway)
			return
		}
		c.println("%s  -> HTTP %d (%s)", line, status, took.Round(time.Millisecond))
		w.WriteHeader(status)
	})
}

// forward posts the delivery to the app as it came: the body byte for byte and the headers Payrudder set.
func (c *cli) forward(hc *http.Client, in *http.Request, body []byte, target string) (int, time.Duration, error) {
	req, err := http.NewRequestWithContext(in.Context(), http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	for _, h := range []string{"Content-Type", "User-Agent", payrudder.WebhookSignatureHeader, payrudder.WebhookEventIDHeader, payrudder.WebhookEventTypeHeader, payrudder.WebhookAttemptHeader} {
		if v := in.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	start := time.Now()
	resp, err := hc.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, time.Since(start), nil
}
