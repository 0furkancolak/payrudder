//go:build integration

package itest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/safehttp"
	"github.com/0furkancolak/payrudder/pkg/webhooksig"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
	webhooksworker "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/worker"
)

// receiver is a tenant's endpoint: it records what it is sent and answers with a status it is told to.
type receiver struct {
	*httptest.Server
	mu     sync.Mutex
	status int
	got    []received
}

type received struct {
	header http.Header
	body   []byte
}

func newReceiver(t *testing.T) *receiver {
	r := &receiver{status: http.StatusOK}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.got = append(r.got, received{req.Header.Clone(), b})
		st := r.status
		r.mu.Unlock()
		w.WriteHeader(st)
		_, _ = w.Write([]byte("nope"))
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *receiver) answer(status int) { r.mu.Lock(); r.status = status; r.mu.Unlock() }
func (r *receiver) calls() []received {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]received(nil), r.got...)
}

func testWebhooks(t *testing.T) {
	e := newEnv(t)
	acct := e.account(t, "Hooks")
	ctx := as(acct[plansv1.Environment_ENVIRONMENT_LIVE].GetId())
	other := as(e.account(t, "Hooks other")[plansv1.Environment_ENVIRONMENT_LIVE].GetId())
	code := func(err error) codes.Code { return status.Code(err) }
	// The dispatcher works across tenants; other tests' deliveries (to addresses that mean nothing here) must not be in this one's way.
	if _, err := e.system.Exec(ctx, `DELETE FROM webhook_deliveries`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Minute) // the dispatcher's clock runs a little ahead of the rows the database stamps
	clock := &now
	disp := &webhooksworker.Dispatcher{Pool: e.system, Box: e.box, Client: safehttp.NewClient(2*time.Second, true), Now: func() time.Time { return *clock }}
	pump := func() int {
		t.Helper()
		n, err := disp.Once(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	deliveries := func(endpoint string) []*plansv1.WebhookDelivery {
		t.Helper()
		r, err := e.hooks.ListWebhookDeliveries(ctx, &plansv1.ListWebhookDeliveriesRequest{EndpointId: endpoint})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetDeliveries()
	}

	rcv := newReceiver(t)
	t.Run("an endpoint is validated and its secret shown once", func(t *testing.T) {
		for name, req := range map[string]*plansv1.CreateWebhookEndpointRequest{
			"not https":     {Url: "ftp://example.com/hook"},
			"credentials":   {Url: "https://user:pw@example.com/hook"},
			"unknown event": {Url: "https://example.com/hook", EventTypes: []string{"invoice.exploded"}},
			"too long":      {Url: "https://example.com/" + strings.Repeat("a", 2000)},
		} {
			if _, err := e.hooks.CreateWebhookEndpoint(ctx, req); code(err) != codes.InvalidArgument {
				t.Errorf("%s: %v", name, err)
			}
		}
		made, err := e.hooks.CreateWebhookEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: rcv.URL, Description: "main", EventTypes: []string{webhooksmodel.SubscriptionCreated, webhooksmodel.SubscriptionCreated}})
		if err != nil {
			t.Fatal(err)
		}
		ep := made.GetEndpoint()
		if !strings.HasPrefix(ep.GetSecret(), "whsec_") || !ep.GetActive() || len(ep.GetEventTypes()) != 1 {
			t.Fatalf("endpoint = %v", ep)
		}
		l, _ := e.hooks.ListWebhookEndpoints(ctx, &plansv1.ListWebhookEndpointsRequest{})
		if len(l.GetEndpoints()) != 1 || l.GetEndpoints()[0].GetSecret() != "" {
			t.Fatalf("list = %v; the secret must not be readable again", l)
		}
		if o, _ := e.hooks.ListWebhookEndpoints(other, &plansv1.ListWebhookEndpointsRequest{}); len(o.GetEndpoints()) != 0 {
			t.Fatalf("another tenant sees %v", o)
		}
		if _, err := e.hooks.UpdateWebhookEndpoint(other, &plansv1.UpdateWebhookEndpointRequest{Id: ep.GetId(), Url: rcv.URL, Active: true}); code(err) != codes.NotFound {
			t.Errorf("another tenant's update: %v", err)
		}
	})

	l, _ := e.hooks.ListWebhookEndpoints(ctx, &plansv1.ListWebhookEndpointsRequest{})
	endpoint := l.GetEndpoints()[0]
	secret := ""
	// The secret is only in the create response; read it back from the database the way the dispatcher does.
	{
		var sealed []byte
		if err := e.system.QueryRow(ctx, `SELECT secret_enc FROM webhook_endpoints WHERE id = $1`, endpoint.GetId()).Scan(&sealed); err != nil {
			t.Fatal(err)
		}
		b, err := e.box.Open(sealed, webhooksmodel.SecretAAD(uuid.MustParse(endpoint.GetId())))
		if err != nil {
			t.Fatal(err)
		}
		secret = string(b)
	}

	t.Run("a subscription event is delivered, signed, once", func(t *testing.T) {
		if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: 1000}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_hook", Name: "Hook", Email: "hook@example.com"}); err != nil {
			t.Fatal(err)
		}
		sub, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_hook", PlanCode: "basic", StartTime: timestamppb.New(now.Add(-time.Hour))})
		if err != nil {
			t.Fatal(err)
		}
		if d := deliveries(endpoint.GetId()); len(d) != 1 || d[0].GetState() != plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_PENDING || d[0].GetEventType() != webhooksmodel.SubscriptionCreated {
			t.Fatalf("queued = %v", d)
		}
		if pump() != 1 {
			t.Fatal("the dispatcher did not claim the delivery")
		}
		got := rcv.calls()
		if len(got) != 1 {
			t.Fatalf("receiver got %d calls", len(got))
		}
		c := got[0]
		if err := webhooksig.Verify(c.header.Get(webhooksig.Header()), c.body, secret, *clock, 0); err != nil {
			t.Fatalf("signature: %v", err)
		}
		if c.header.Get("Payrudder-Event-Type") != webhooksmodel.SubscriptionCreated || c.header.Get("Content-Type") != "application/json" {
			t.Errorf("headers = %v", c.header)
		}
		var env struct {
			ID, Type, TenantID string
			Data               struct {
				Subscription struct {
					ID       string `json:"id"`
					PlanCode string `json:"plan_code"`
				}
			}
		}
		if err := json.Unmarshal(c.body, &env); err != nil {
			t.Fatal(err)
		}
		if env.Type != webhooksmodel.SubscriptionCreated || env.Data.Subscription.ID != sub.GetSubscription().GetId() || env.Data.Subscription.PlanCode != "basic" || env.ID != c.header.Get("Payrudder-Event-Id") {
			t.Fatalf("body = %s", c.body)
		}
		if d := deliveries(endpoint.GetId()); d[0].GetState() != plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_DELIVERED || d[0].GetAttempts() != 1 {
			t.Fatalf("delivery = %v", d[0])
		}
		if pump() != 0 || len(rcv.calls()) != 1 {
			t.Fatal("a delivered event was sent again")
		}
		// An event type the endpoint did not ask for is not queued.
		if _, err := e.plans.PauseSubscription(ctx, &plansv1.PauseSubscriptionRequest{SubscriptionId: sub.GetSubscription().GetId()}); err != nil {
			t.Fatal(err)
		}
		if d := deliveries(endpoint.GetId()); len(d) != 1 {
			t.Fatalf("an unwanted event type was queued: %v", d)
		}
	})

	t.Run("a failing endpoint is retried on the backoff and then given up on; a redelivery starts over", func(t *testing.T) {
		rcv.answer(http.StatusInternalServerError)
		if _, err := e.hooks.UpdateWebhookEndpoint(ctx, &plansv1.UpdateWebhookEndpointRequest{Id: endpoint.GetId(), Url: rcv.URL, Active: true}); err != nil { // every event type
			t.Fatal(err)
		}
		ev := uuid.NewString()
		rec := func(id string) int32 {
			r, err := e.hooks.RecordTenantEvent(ctx, &plansv1.RecordTenantEventRequest{EventId: id, Type: webhooksmodel.InvoicePaid, DataJson: `{"invoice_id":"inv_1"}`, OccurredTime: timestamppb.New(*clock)})
			if err != nil {
				t.Fatal(err)
			}
			return r.GetQueued()
		}
		if rec(ev) != 1 || rec(ev) != 0 {
			t.Fatal("recording the same event twice must queue it once")
		}
		var d *plansv1.WebhookDelivery
		find := func() *plansv1.WebhookDelivery {
			for _, x := range deliveries(endpoint.GetId()) {
				if x.GetEventId() == ev {
					return x
				}
			}
			t.Fatal("delivery not found")
			return nil
		}
		for attempt := 1; attempt <= len(webhooksmodel.Backoff)+1; attempt++ {
			if pump() != 1 {
				t.Fatalf("attempt %d: nothing was due", attempt)
			}
			d = find()
			if d.GetAttempts() != int32(attempt) || d.GetLastStatus() != 500 || !strings.Contains(d.GetLastError(), "HTTP 500") { //nolint:gosec // small
				t.Fatalf("attempt %d: %v", attempt, d)
			}
			if attempt <= len(webhooksmodel.Backoff) {
				if d.GetState() != plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_PENDING {
					t.Fatalf("attempt %d: state %v", attempt, d.GetState())
				}
				if pump() != 0 {
					t.Fatalf("attempt %d: retried before its backoff", attempt)
				}
				*clock = clock.Add(webhooksmodel.Backoff[attempt-1] + time.Second)
			}
		}
		if d.GetState() != plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_FAILED {
			t.Fatalf("after the last attempt: %v", d)
		}
		rcv.answer(http.StatusOK)
		if _, err := e.hooks.RedeliverWebhook(ctx, &plansv1.RedeliverWebhookRequest{DeliveryId: d.GetId()}); err != nil {
			t.Fatal(err)
		}
		pump()
		if d = find(); d.GetState() != plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_DELIVERED || d.GetAttempts() != 1 {
			t.Fatalf("after redelivery: %v", d)
		}
		if _, err := e.hooks.RecordTenantEvent(ctx, &plansv1.RecordTenantEventRequest{EventId: uuid.NewString(), Type: "nope", DataJson: `{}`}); code(err) != codes.InvalidArgument {
			t.Errorf("unknown type: %v", err)
		}
		if _, err := e.hooks.RecordTenantEvent(ctx, &plansv1.RecordTenantEventRequest{EventId: uuid.NewString(), Type: webhooksmodel.InvoicePaid, DataJson: `[1]`}); code(err) != codes.InvalidArgument {
			t.Errorf("data that is not an object: %v", err)
		}
	})

	t.Run("a rotated secret signs alongside the old one for a day, and a test event can be sent", func(t *testing.T) {
		*clock = time.Now().Add(time.Minute) // the retries above moved the dispatcher's clock days ahead of the server's
		rot, err := e.hooks.RotateWebhookSecret(ctx, &plansv1.RotateWebhookSecretRequest{Id: endpoint.GetId()})
		if err != nil || rot.GetEndpoint().GetSecret() == "" || rot.GetEndpoint().GetSecret() == secret {
			t.Fatalf("rotate = %v, %v", rot, err)
		}
		before := len(rcv.calls())
		if _, err := e.hooks.SendTestWebhook(ctx, &plansv1.SendTestWebhookRequest{Id: endpoint.GetId()}); err != nil {
			t.Fatal(err)
		}
		pump()
		got := rcv.calls()
		if len(got) != before+1 {
			t.Fatalf("calls = %d, want %d", len(got), before+1)
		}
		c := got[len(got)-1]
		if c.header.Get("Payrudder-Event-Type") != webhooksmodel.Ping {
			t.Errorf("event type = %s", c.header.Get("Payrudder-Event-Type"))
		}
		for _, s := range []string{secret, rot.GetEndpoint().GetSecret()} {
			if err := webhooksig.Verify(c.header.Get(webhooksig.Header()), c.body, s, *clock, 0); err != nil {
				t.Errorf("secret %.10s…: %v", s, err)
			}
		}
		// After the overlap only the new secret signs.
		*clock = clock.Add(25 * time.Hour)
		if _, err := e.hooks.SendTestWebhook(ctx, &plansv1.SendTestWebhookRequest{Id: endpoint.GetId()}); err != nil {
			t.Fatal(err)
		}
		pump()
		c = rcv.calls()[len(rcv.calls())-1]
		if err := webhooksig.Verify(c.header.Get(webhooksig.Header()), c.body, secret, *clock, 0); err == nil {
			t.Error("the old secret still signs after the overlap")
		}
		if err := webhooksig.Verify(c.header.Get(webhooksig.Header()), c.body, rot.GetEndpoint().GetSecret(), *clock, 0); err != nil {
			t.Errorf("the new secret: %v", err)
		}
	})

	t.Run("an inactive or deleted endpoint receives nothing", func(t *testing.T) {
		if _, err := e.hooks.UpdateWebhookEndpoint(ctx, &plansv1.UpdateWebhookEndpointRequest{Id: endpoint.GetId(), Url: rcv.URL, Active: false}); err != nil {
			t.Fatal(err)
		}
		r, err := e.hooks.RecordTenantEvent(ctx, &plansv1.RecordTenantEventRequest{EventId: uuid.NewString(), Type: webhooksmodel.InvoicePaid, DataJson: `{}`})
		if err != nil || r.GetQueued() != 0 {
			t.Fatalf("queued for an inactive endpoint: %v, %v", r, err)
		}
		if _, err := e.hooks.DeleteWebhookEndpoint(ctx, &plansv1.DeleteWebhookEndpointRequest{Id: endpoint.GetId()}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.hooks.DeleteWebhookEndpoint(ctx, &plansv1.DeleteWebhookEndpointRequest{Id: endpoint.GetId()}); code(err) != codes.NotFound {
			t.Errorf("deleting twice: %v", err)
		}
	})
}
