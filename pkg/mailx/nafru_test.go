package mailx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The mail's name travels as a tag and a header, so a Nafru Email application added for Payrudder can apply its own template to it (UNVERIFIED: that Nafru does);
// a name a provider would refuse is left out instead of failing the send.
func TestNafruCarriesTheTemplateName(t *testing.T) {
	var got resendBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = resendBody{}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"id":"em_1"}`))
	}))
	defer srv.Close()
	s := Nafru{Resend{BaseURL: srv.URL, APIKey: "k"}}
	if _, err := s.Send(context.Background(), Message{From: "a@b.c", To: "x@y.z", Subject: "s", Text: "t", Template: "invoice_paid.v1", Tags: []Tag{{"mine", "1"}}, Headers: map[string]string{"X-Mine": "1"}}); err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 2 || got.Tags[1] != (resendTag{Name: "payrudder_template", Value: "invoice_paid_v1"}) || got.Tags[0].Name != "mine" ||
		got.Headers["X-Payrudder-Template"] != "invoice_paid.v1" || got.Headers["X-Mine"] != "1" {
		t.Errorf("body = %+v", got)
	}
	for _, bad := range []string{"", "has space", "ünï"} {
		if _, err := s.Send(context.Background(), Message{From: "a@b.c", To: "x@y.z", Subject: "s", Text: "t", Template: bad}); err != nil {
			t.Fatal(err)
		}
		if len(got.Tags) != 0 || len(got.Headers) != 0 {
			t.Errorf("template %q was sent as %+v", bad, got)
		}
	}
}

func TestEventValidation(t *testing.T) {
	for _, c := range []struct {
		e        Event
		ok       bool
		suppress bool
	}{
		{Event{Type: EventBounced, MessageID: "m", BounceType: BounceHard}, true, true},
		{Event{Type: EventBounced, MessageID: "m", BounceType: BounceSoft}, true, false},
		{Event{Type: EventBounced, MessageID: "m"}, false, false},
		{Event{Type: EventComplained, MessageID: "m"}, true, true},
		{Event{Type: EventDelivered, MessageID: "m"}, true, false},
		{Event{Type: EventDelayed, MessageID: "m"}, true, false},
		{Event{Type: "opened", MessageID: "m"}, false, false},
		{Event{Type: EventDelivered}, false, false},
	} {
		if err := c.e.Validate(); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c.e, err)
		}
		if c.ok && c.e.Suppresses() != c.suppress {
			t.Errorf("%+v suppresses = %v", c.e, c.e.Suppresses())
		}
	}
}
