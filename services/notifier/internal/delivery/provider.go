package delivery

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// Provider is a tenant's own e-mail provider: the sender to use and the From of the mail it sends.
type Provider struct {
	Sender mailx.Sender
	From   string
}

// Providers finds the e-mail provider a tenant connected, if any. A nil Provider with no error means the tenant sends through Payrudder's built-in sender.
// An error means the answer could not be had, and the mail is retried rather than sent through the built-in sender against the tenant's choice.
type Providers interface {
	For(ctx context.Context, tenantID uuid.UUID) (*Provider, error)
}

// MailIntegrations is the part of plans-svc the lookup uses.
type MailIntegrations interface {
	ResolveMailSender(ctx context.Context, in *plansv1.ResolveMailSenderRequest, opts ...grpc.CallOption) (*plansv1.ResolveMailSenderResponse, error)
}

type found struct {
	p     *Provider
	until time.Time
}

// PlansProviders reads a tenant's connection from plans-svc and remembers each answer for TTL (a month close asks once per tenant; a change reaches the
// notifier within TTL). The key sits in memory for that long and is never logged.
type PlansProviders struct {
	Client MailIntegrations
	TTL    time.Duration
	// HTTP is the client that reaches the provider; it must refuse private addresses (safehttp.NewClient).
	HTTP *http.Client
	// Now is the clock; time.Now when nil.
	Now func() time.Time

	mu    sync.Mutex
	cache map[uuid.UUID]found
}

// For implements Providers.
func (s *PlansProviders) For(ctx context.Context, tenantID uuid.UUID) (*Provider, error) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	s.mu.Lock()
	c, ok := s.cache[tenantID]
	s.mu.Unlock()
	if ok && now().Before(c.until) {
		return c.p, nil
	}
	resp, err := s.Client.ResolveMailSender(tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: tenantID})), &plansv1.ResolveMailSenderRequest{})
	if err != nil {
		return nil, fmt.Errorf("mail provider lookup: %w", err)
	}
	var p *Provider
	if resp.GetConnected() {
		p = &Provider{Sender: mailx.Resend{BaseURL: resp.GetBaseUrl(), APIKey: resp.GetApiKey(), Client: s.HTTP}, From: resp.GetFrom()}
	}
	s.mu.Lock()
	if s.cache == nil || len(s.cache) >= maxCached {
		s.cache = make(map[uuid.UUID]found)
	}
	s.cache[tenantID] = found{p: p, until: now().Add(s.TTL)}
	s.mu.Unlock()
	return p, nil
}
