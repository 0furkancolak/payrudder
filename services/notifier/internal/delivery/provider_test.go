package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/mailx"
)

type fakeIntegrations struct {
	resp  *plansv1.ResolveMailSenderResponse
	err   error
	calls int
}

func (f *fakeIntegrations) ResolveMailSender(_ context.Context, _ *plansv1.ResolveMailSenderRequest, _ ...grpc.CallOption) (*plansv1.ResolveMailSenderResponse, error) {
	f.calls++
	return f.resp, f.err
}

func TestPlansProvidersBuildsTheSenderAndCaches(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := &fakeIntegrations{resp: &plansv1.ResolveMailSenderResponse{Connected: true, Provider: "nafru", BaseUrl: "https://api.nafru.example", ApiKey: "nk", From: "Acme <a@acme.example>"}}
	s := &PlansProviders{Client: f, TTL: 10 * time.Second, Now: func() time.Time { return now }}
	id := uuid.New()
	for range 3 {
		p, err := s.For(context.Background(), id)
		if err != nil || p == nil || p.From != "Acme <a@acme.example>" {
			t.Fatalf("provider %+v err %v", p, err)
		}
		if r, ok := p.Sender.(mailx.Resend); !ok || r.BaseURL != "https://api.nafru.example" || r.APIKey != "nk" {
			t.Fatalf("sender %+v", p.Sender)
		}
	}
	if f.calls != 1 {
		t.Fatalf("looked up %d times, want 1", f.calls)
	}
	now = now.Add(11 * time.Second)
	if _, err := s.For(context.Background(), id); err != nil || f.calls != 2 {
		t.Fatalf("after the TTL: calls=%d err=%v", f.calls, err)
	}
}

func TestPlansProvidersNoConnectionAndFailure(t *testing.T) {
	f := &fakeIntegrations{resp: &plansv1.ResolveMailSenderResponse{}}
	s := &PlansProviders{Client: f, TTL: time.Second}
	if p, err := s.For(context.Background(), uuid.New()); p != nil || err != nil {
		t.Fatalf("no connection is nil, nil: %v %v", p, err)
	}
	f.err = errors.New("plans-svc down")
	if _, err := s.For(context.Background(), uuid.New()); err == nil {
		t.Fatal("a failed lookup must be an error: the mail is retried, not sent through the other sender")
	}
}
