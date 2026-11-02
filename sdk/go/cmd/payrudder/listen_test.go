package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const whsec = "whsec_test_secret_0123456789"

func sign(secret string, body []byte, at time.Time) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", at.Unix())
	mac.Write(body)
	return fmt.Sprintf("t=%d,v1=%s", at.Unix(), hex.EncodeToString(mac.Sum(nil)))
}

type listener struct {
	addr    string
	out     *syncBuf
	errOut  *syncBuf
	stop    func() int
	stopped bool
}

// startListen runs "webhooks listen --port 0 ..." until the test stops it, and waits until it says where it listens.
func startListen(t *testing.T, e *env, args ...string) *listener {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	l := &listener{out: &syncBuf{}, errOut: &syncBuf{}}
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, append([]string{"webhooks", "listen", "--port", "0"}, args...), strings.NewReader(""), l.out, l.errOut, e.getenv)
	}()
	l.stop = func() int {
		cancel()
		select {
		case code := <-done:
			l.stopped = true
			return code
		case <-time.After(10 * time.Second):
			t.Fatal("webhooks listen did not stop")
			return -1
		}
	}
	t.Cleanup(func() {
		if !l.stopped {
			l.stop()
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case code := <-done:
			t.Fatalf("webhooks listen exited with %d: %s %s", code, l.out, l.errOut)
		default:
		}
		if _, rest, ok := strings.Cut(l.out.String(), "Listening on http://"); ok {
			l.addr, _, _ = strings.Cut(rest, " ")
			return l
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("webhooks listen never listened: %s %s", l.out, l.errOut)
	return nil
}

// post sends a delivery the way Payrudder does.
func (l *listener) post(t *testing.T, body []byte, header http.Header) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://"+l.addr+"/", bytes.NewReader(body))
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// app is the developer's webhook handler.
type app struct {
	*httptest.Server
	mu     sync.Mutex
	bodies [][]byte
	header []http.Header
	status int
}

func newApp(t *testing.T, status int) *app {
	a := &app{status: status}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		a.mu.Lock()
		a.bodies, a.header = append(a.bodies, b), append(a.header, r.Header.Clone())
		a.mu.Unlock()
		w.WriteHeader(a.status)
	}))
	t.Cleanup(a.Close)
	return a
}

func (a *app) calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.bodies)
}

func delivered(secret string, body []byte) http.Header {
	return http.Header{
		"Content-Type":               {"application/json"},
		"Payrudder-Signature":        {sign(secret, body, time.Now())},
		"Payrudder-Event-Id":         {"evt-1"},
		"Payrudder-Event-Type":       {"invoice.paid"},
		"Payrudder-Delivery-Attempt": {"2"},
	}
}

var deliveryBody = []byte(`{"id":"evt-1","type":"invoice.paid","created":"2026-11-02T10:00:00Z","tenant_id":"t1","data":{"invoice_id":"inv-1","total_minor":"1116"}}`)

func TestListenVerifiesAndForwardsTheDeliveryUnchanged(t *testing.T) {
	e := newEnv(t)
	target := newApp(t, http.StatusNoContent)
	l := startListen(t, e, "--secret", whsec, "--forward-to", target.URL+"/webhooks/payrudder")
	if !strings.HasPrefix(l.addr, "127.0.0.1:") {
		t.Errorf("listening on %s: the default must be the loopback address, not the network", l.addr)
	}

	h := delivered(whsec, deliveryBody)
	if got := l.post(t, deliveryBody, h); got != http.StatusNoContent {
		t.Errorf("Payrudder was answered %d, want the app's 204", got)
	}
	if target.calls() != 1 {
		t.Fatalf("the app got %d deliveries", target.calls())
	}
	if !bytes.Equal(target.bodies[0], deliveryBody) {
		t.Errorf("body changed: %s", target.bodies[0])
	}
	for _, k := range []string{"Payrudder-Signature", "Payrudder-Event-Id", "Payrudder-Event-Type", "Payrudder-Delivery-Attempt", "Content-Type"} {
		if target.header[0].Get(k) != h.Get(k) {
			t.Errorf("header %s = %q, want %q", k, target.header[0].Get(k), h.Get(k))
		}
	}
	out := l.out.String()
	contains(t, "stdout", out, "invoice.paid", "evt-1", "attempt 2", "signature ok", "-> HTTP 204", "forwarded to "+target.URL)
	lacks(t, "output", out+l.errOut.String(), whsec, "total_minor")
	if code := l.stop(); code != 0 {
		t.Errorf("exit %d after Ctrl-C", code)
	}
}

func TestListenPassesTheAppsFailureBackSoPayrudderRetries(t *testing.T) {
	e := newEnv(t)
	target := newApp(t, http.StatusInternalServerError)
	l := startListen(t, e, "--secret", whsec, "--forward-to", target.URL)
	if got := l.post(t, deliveryBody, delivered(whsec, deliveryBody)); got != http.StatusInternalServerError {
		t.Errorf("answered %d, want the app's 500", got)
	}
	contains(t, "stdout", l.out.String(), "-> HTTP 500")

	target.Close()
	if got := l.post(t, deliveryBody, delivered(whsec, deliveryBody)); got != http.StatusBadGateway {
		t.Errorf("with the app down: answered %d, want 502", got)
	}
}

func TestListenRefusesWhatDoesNotVerifyAndNeverForwardsIt(t *testing.T) {
	e := newEnv(t)
	target := newApp(t, http.StatusOK)
	l := startListen(t, e, "--secret", whsec, "--forward-to", target.URL)

	tampered := append([]byte(nil), deliveryBody...)
	tampered[len(tampered)-5] = '9'
	forged := delivered("whsec_somebody_elses", deliveryBody)
	stale := delivered(whsec, deliveryBody)
	stale.Set("Payrudder-Signature", sign(whsec, deliveryBody, time.Now().Add(-time.Hour)))
	unsigned := delivered(whsec, deliveryBody)
	unsigned.Del("Payrudder-Signature")

	for name, c := range map[string]struct {
		body []byte
		h    http.Header
	}{
		"tampered body": {tampered, delivered(whsec, deliveryBody)}, "wrong secret": {deliveryBody, forged}, "old timestamp": {deliveryBody, stale}, "no signature": {deliveryBody, unsigned},
	} {
		if got := l.post(t, c.body, c.h); got != http.StatusBadRequest {
			t.Errorf("%s: answered %d, want 400", name, got)
		}
	}
	if target.calls() != 0 {
		t.Errorf("%d unverified deliveries reached the app", target.calls())
	}
	contains(t, "stdout", l.out.String(), "REFUSED", "signature does not match", "outside the tolerance", "malformed")

	req, _ := http.NewRequest(http.MethodGet, "http://"+l.addr+"/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET answered %d", resp.StatusCode)
	}
}

func TestListenWithoutASecretSaysSoAndStillForwards(t *testing.T) {
	e := newEnv(t)
	target := newApp(t, http.StatusOK)
	l := startListen(t, e, "--forward-to", target.URL)
	contains(t, "stdout", l.out.String(), "signatures are NOT checked")
	if got := l.post(t, deliveryBody, http.Header{"Payrudder-Event-Type": {"invoice.paid"}}); got != http.StatusOK || target.calls() != 1 {
		t.Errorf("answered %d, app got %d", got, target.calls())
	}
	contains(t, "stdout", l.out.String(), "signature not checked")
}

func TestListenTakesTheSecretFromTheEnvironmentAndPrintsWithoutForwarding(t *testing.T) {
	e := newEnv(t)
	e.vars["PAYRUDDER_WEBHOOK_SECRET"] = whsec
	l := startListen(t, e)
	if got := l.post(t, deliveryBody, delivered(whsec, deliveryBody)); got != http.StatusOK {
		t.Errorf("answered %d", got)
	}
	contains(t, "stdout", l.out.String(), "signature ok", "evt-1")
	lacks(t, "stdout", l.out.String(), "->")
	if got := l.post(t, deliveryBody, delivered("whsec_other", deliveryBody)); got != http.StatusBadRequest {
		t.Errorf("a wrong signature answered %d", got)
	}
}

func TestListenPublicURLRegistersATemporaryEndpointAndDeletesItOnExit(t *testing.T) {
	e := newEnv(t)
	e.gw.handle("POST /v1/webhook-endpoints", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 201, map[string]any{"id": "ep-tmp", "url": "https://tunnel.example.com/", "active": true, "secret": whsec})
	})
	e.gw.handle("DELETE /v1/webhook-endpoints/ep-tmp", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	target := newApp(t, http.StatusOK)
	l := startListen(t, e, "--public-url", "https://tunnel.example.com/", "--events", "invoice.paid,invoice.voided", "--forward-to", target.URL)

	var created struct {
		URL         string   `json:"url"`
		Description string   `json:"description"`
		EventTypes  []string `json:"event_types"`
	}
	reqs := e.gw.requests("POST", "/v1/webhook-endpoints")
	if len(reqs) != 1 || json.Unmarshal(reqs[0].body, &created) != nil || created.URL != "https://tunnel.example.com/" || len(created.EventTypes) != 2 || !strings.Contains(created.Description, "temporary") {
		t.Fatalf("created %+v from %+v", created, reqs)
	}
	// The secret came from the endpoint's creation: deliveries signed with it verify.
	if got := l.post(t, deliveryBody, delivered(whsec, deliveryBody)); got != http.StatusOK || target.calls() != 1 {
		t.Errorf("answered %d, app got %d", got, target.calls())
	}
	contains(t, "stdout", l.out.String(), "Registered the temporary endpoint ep-tmp")
	lacks(t, "output", l.out.String()+l.errOut.String(), whsec)

	if len(e.gw.requests("DELETE", "/v1/webhook-endpoints/ep-tmp")) != 0 {
		t.Fatal("the endpoint was deleted while the command ran")
	}
	if code := l.stop(); code != 0 {
		t.Errorf("exit %d (%s)", code, l.errOut)
	}
	if len(e.gw.requests("DELETE", "/v1/webhook-endpoints/ep-tmp")) != 1 {
		t.Error("the temporary endpoint was not deleted on exit")
	}
	contains(t, "stdout", l.out.String(), "Deleted the temporary endpoint ep-tmp")
}

func TestListenPublicURLRefusedByTheAPIStartsNothing(t *testing.T) {
	e := newEnv(t)
	e.gw.handle("POST /v1/webhook-endpoints", func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, 400, "INVALID_ARGUMENT", "webhooks.endpoint.url_not_public", "the URL must be https and public")
	})
	e.expect(1, "", "webhooks", "listen", "--port", "0", "--public-url", "http://localhost:4242")
	contains(t, "stderr", e.errOut.String(), "webhooks.endpoint.url_not_public")
}

func TestListenRejectsABadForwardTarget(t *testing.T) {
	e := newEnv(t)
	e.expect(2, "", "webhooks", "listen", "--port", "0", "--forward-to", "localhost:3000")
	contains(t, "stderr", e.errOut.String(), "--forward-to must be an http(s) URL")
}
