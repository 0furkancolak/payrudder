package fakenafru_test

import (
	"context"
	"errors"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/mailx/fakenafru"
)

func TestFakeRecordsWhatNafruSends(t *testing.T) {
	f := fakenafru.New()
	defer f.Close()
	f.APIKey = "nk_live"
	s := mailx.Nafru{Resend: mailx.Resend{BaseURL: f.URL, APIKey: "nk_live"}}
	m := mailx.Message{From: "Acme <b@acme.test>", To: "ada@example.com", Subject: "Hi", Text: "t", ID: "k1", Template: "payment_failed.v1"}
	id, err := s.Send(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := s.Send(context.Background(), m) // the same Idempotency-Key is the same message
	got := f.Sent()
	if again != id || len(got) != 1 || got[0].Tags["payrudder_template"] != "payment_failed_v1" || got[0].Headers["X-Payrudder-Template"] != "payment_failed.v1" || got[0].To[0] != "ada@example.com" {
		t.Errorf("sent = %+v, ids %q %q", got, id, again)
	}
	var perm *mailx.PermanentError
	if _, err := (mailx.Nafru{Resend: mailx.Resend{BaseURL: f.URL, APIKey: "wrong"}}).Send(context.Background(), m); !errors.As(err, &perm) || perm.Status != 401 {
		t.Errorf("a wrong key: %v", err)
	}
	f.Fail(503)
	if _, err := s.Send(context.Background(), mailx.Message{From: "a@b.c", To: "x@y.z", Subject: "s", ID: "k2"}); err == nil || errors.As(err, &perm) {
		t.Errorf("an outage is a retry, not a refusal: %v", err)
	}
}
