package webhook_test

import (
	"errors"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/conformance"
	"github.com/0furkancolak/payrudder/pkg/einvoice/webhook"
)

// The webhook adapter against the fake integrator that speaks the protocol of doc.go.
func TestConformance(t *testing.T) {
	var cur *conformance.WebhookServer
	conformance.Run(t, conformance.Harness{
		New: func(t testing.TB) einvoice.Provider {
			cur = conformance.NewWebhookServer(t)
			p, err := webhook.New([]byte(`{"url":"`+cur.URL+`/hook","secret":"`+cur.Secret+`","allow_private":true}`), webhook.Options{AllowPrivate: true, Timeout: 2 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			return p
		},
		Secret:     "conformance-secret-key",
		Fail:       func(s int) { cur.Fail(s) },
		SetPending: func(on bool) { cur.SetPending(on) },
		Settle:     func() { cur.Settle() },
		Created:    func() int { return cur.Created() },
		Requests:   func() int { return cur.Requests() },
	})
}

// A slow endpoint is a transient failure: the document is retried, not failed.
func TestSlowEndpointIsTransient(t *testing.T) {
	srv := conformance.NewWebhookServer(t)
	srv.Delay(500 * time.Millisecond)
	p, err := webhook.New([]byte(`{"url":"`+srv.URL+`/hook","secret":"`+srv.Secret+`","allow_private":true}`), webhook.Options{AllowPrivate: true, Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Issue(t.Context(), conformance.Request(t, einvoice.TypePeppol, "slow-1"))
	var tr *einvoice.TransientError
	if !errors.As(err, &tr) {
		t.Fatalf("want a *TransientError, got %T: %v", err, err)
	}
}
