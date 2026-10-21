package handler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/mailcategory"
	"github.com/0furkancolak/quotify/pkg/tenant"
)

// Switch tells whether a tenant has a category of mail on (the tenant's mail switch, stored by plans-svc).
type Switch interface {
	Enabled(ctx context.Context, tenantID uuid.UUID, category string) (bool, error)
}

// MailSettings is the part of plans-svc the switch uses.
type MailSettings interface {
	GetMailSettings(ctx context.Context, in *plansv1.GetMailSettingsRequest, opts ...grpc.CallOption) (*plansv1.GetMailSettingsResponse, error)
}

// maxCached bounds the switch's cache; when it is full the cache is emptied, which only costs a few lookups.
const maxCached = 1024

type cached struct {
	disabled map[string]bool
	until    time.Time
}

// PlansSwitch reads a tenant's mail switch from plans-svc and remembers each answer for TTL, so that a month close that sends thousands of receipts asks once
// per tenant and a change of the switch reaches the notifier within TTL. A failed lookup is an error: the mail is retried, never sent against a setting
// that could not be read.
type PlansSwitch struct {
	Client MailSettings
	TTL    time.Duration
	// Now is the clock; time.Now when nil.
	Now func() time.Time

	mu    sync.Mutex
	cache map[uuid.UUID]cached
}

// Enabled implements Switch.
func (s *PlansSwitch) Enabled(ctx context.Context, tenantID uuid.UUID, category string) (bool, error) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	s.mu.Lock()
	c, ok := s.cache[tenantID]
	s.mu.Unlock()
	if ok && now().Before(c.until) {
		return !c.disabled[category], nil
	}
	resp, err := s.Client.GetMailSettings(tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: tenantID})), &plansv1.GetMailSettingsRequest{})
	if err != nil {
		return false, fmt.Errorf("mail settings lookup: %w", err)
	}
	c = cached{disabled: map[string]bool{}, until: now().Add(s.TTL)}
	for _, cs := range resp.GetCategories() {
		if !cs.GetEnabled() {
			c.disabled[cs.GetCategory()] = true
		}
	}
	s.mu.Lock()
	if s.cache == nil || len(s.cache) >= maxCached {
		s.cache = make(map[uuid.UUID]cached)
	}
	s.cache[tenantID] = c
	s.mu.Unlock()
	return !c.disabled[category], nil
}

var droppedTotal = func() metric.Int64Counter {
	c, err := otel.Meter("github.com/0furkancolak/quotify/services/notifier").Int64Counter("notifier_mail_dropped",
		metric.WithDescription("Mails not sent because the tenant switched their category off, by category"))
	if err != nil {
		panic(err)
	}
	return c
}()

// Permitted says whether the mail of a template may be sent for the tenant. A mail whose category the tenant switched off is dropped: counted in
// notifier_mail_dropped_total and reported as not permitted, which the caller treats as done (no error, no retry). Without a Switch every mail is permitted,
// and so is one without a tenant id. An error means the setting could not be read and the caller retries.
func (m *Mailer) Permitted(ctx context.Context, tenantID uuid.UUID, template string) (bool, error) {
	if m.Switch == nil || tenantID == uuid.Nil {
		return true, nil
	}
	category := mailcategory.ForTemplate(template)
	on, err := m.Switch.Enabled(ctx, tenantID, category)
	if err != nil {
		return false, err
	}
	if !on {
		droppedTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("category", category)))
	}
	return on, nil
}
