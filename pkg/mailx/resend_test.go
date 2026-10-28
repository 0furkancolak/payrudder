package mailx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResendSendsTheResendShape(t *testing.T) {
	var got struct {
		auth, idem, ctype, path string
		body                    resendBody
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.auth, got.idem, got.ctype, got.path = r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key"), r.Header.Get("Content-Type"), r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		_, _ = w.Write([]byte(`{"id":"em_123"}`))
	}))
	defer srv.Close()
	id, err := Resend{BaseURL: srv.URL + "/", APIKey: "nk_secret"}.Send(context.Background(), Message{From: "Acme <billing@acme.test>", To: "ada@example.com", Subject: "Hi", HTML: "<p>hi</p>", Text: "hi", ID: "cmd-1"})
	if err != nil || id != "em_123" {
		t.Fatalf("send: %q %v", id, err)
	}
	if got.path != "/emails" || got.auth != "Bearer nk_secret" || got.idem != "cmd-1" || got.ctype != "application/json" {
		t.Errorf("request = %+v", got)
	}
	if got.body.From != "Acme <billing@acme.test>" || len(got.body.To) != 1 || got.body.To[0] != "ada@example.com" || got.body.Subject != "Hi" || got.body.HTML != "<p>hi</p>" || got.body.Text != "hi" {
		t.Errorf("body = %+v", got.body)
	}
}

func TestResendTellsRefusalFromTrouble(t *testing.T) {
	for status, want := range map[int]string{401: "permanent", 403: "permanent", 422: "permanent", 429: "retry", 500: "retry", 503: "retry"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"no"}`))
		}))
		_, err := Resend{BaseURL: srv.URL, APIKey: "k"}.Send(context.Background(), Message{From: "a@b.test", To: "c@d.test", Subject: "s"})
		srv.Close()
		var perm *PermanentError
		var notSent *NotSentError
		switch {
		case err == nil:
			t.Errorf("%d: no error", status)
		case errors.As(err, &perm) != (want == "permanent"):
			t.Errorf("%d: error %v, want %s", status, err, want)
		case errors.As(err, &notSent):
			t.Errorf("%d: a mail the provider answered for is not 'not sent': %v", status, err)
		}
	}
}

func TestResendUnreachableIsNotSent(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := Resend{BaseURL: url, APIKey: "k"}.Send(context.Background(), Message{From: "a@b.test", To: "c@d.test", Subject: "s"})
	var ns *NotSentError
	if !errors.As(err, &ns) {
		t.Errorf("a refused connection: %v, want NotSentError", err)
	}
	if _, err := (Resend{BaseURL: "not a url", APIKey: "k"}).Send(context.Background(), Message{From: "a@b.test", To: "c@d.test", Subject: "s"}); !errors.As(err, &ns) {
		t.Errorf("a bad base URL: %v", err)
	}
	if _, err := (Resend{BaseURL: url, APIKey: "k"}).Send(context.Background(), Message{From: "a@b.test", To: "c@d.test\r\nBcc: x", Subject: "s"}); err == nil {
		t.Error("header injection was sent")
	}
}

func TestResendCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/domains" || r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"invalid key"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	if err := (Resend{BaseURL: srv.URL, APIKey: "good"}).Check(context.Background()); err != nil {
		t.Errorf("a good key: %v", err)
	}
	var perm *PermanentError
	if err := (Resend{BaseURL: srv.URL, APIKey: "bad"}).Check(context.Background()); !errors.As(err, &perm) || perm.Status != 401 {
		t.Errorf("a bad key: %v", err)
	}
}

// The mail's name travels as a tag and a header, so a Nafru Email application added for Payrudder can apply its own template to it; a name a provider would
// refuse is left out instead of failing the send.
func TestResendCarriesTheTemplateName(t *testing.T) {
	var got resendBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = resendBody{}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"id":"em_1"}`))
	}))
	defer srv.Close()
	s := Resend{BaseURL: srv.URL, APIKey: "k"}
	if _, err := s.Send(context.Background(), Message{From: "a@b.c", To: "x@y.z", Subject: "s", Text: "t", Template: "invoice_issued"}); err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 1 || got.Tags[0] != (resendTag{Name: "payrudder_template", Value: "invoice_issued"}) || got.Headers["X-Payrudder-Template"] != "invoice_issued" {
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
