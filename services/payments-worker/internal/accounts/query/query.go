// Package query reads a tenant's payment provider accounts. It never writes, and no secret leaves it.
package query

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// Deps is what the queries read. Health, when set, remembers the answer of a provider's health check for a short while, so a console that asks for the
// checklist every few seconds does not ask the provider just as often.
type Deps struct {
	Registry *payments.Registry
	Accounts store.Accounts
	Health   *HealthCache
	Log      *slog.Logger
}

// HealthTimeout bounds the one call a health check makes to a provider.
const HealthTimeout = 8 * time.Second

// HealthTTL is how long a health answer is reused. An answer is bound to the stored settings (their updated_at), so replacing the credentials asks again.
const HealthTTL = 30 * time.Second

// HealthCache remembers health answers per account and settings version. The zero value is ready.
type HealthCache struct {
	mu      sync.Mutex
	entries map[string]cached
	now     func() time.Time
}

type cached struct {
	answer  answer
	expires time.Time
}

type answer struct {
	checked bool
	problem string
}

func (c *HealthCache) get(key string) (answer, bool) {
	if c == nil {
		return answer{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || !c.clock().Before(e.expires) {
		delete(c.entries, key)
		return answer{}, false
	}
	return e.answer, true
}

func (c *HealthCache) put(key string, a answer) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]cached{}
	}
	if len(c.entries) > 4096 { // bounded: a flood of tenants cannot grow it without end
		c.entries = map[string]cached{}
	}
	c.entries[key] = cached{answer: a, expires: c.clock().Add(HealthTTL)}
}

func (c *HealthCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// List is the tenant's accounts, without their settings.
func List(ctx context.Context, d Deps, tenant uuid.UUID) ([]store.AccountInfo, error) {
	list, err := d.Accounts.List(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("list provider accounts: %w", err)
	}
	return list, nil
}

// The problems an account can have; the console words each in its own language.
const (
	// ProblemUnreadable: the stored settings do not open or do not build an adapter (a rotated secret key, a broken record).
	ProblemUnreadable = "unreadable"
	// ProblemRefused: the provider answered and did not accept the credentials.
	ProblemRefused = "refused"
	// ProblemUnreachable: the provider could not be asked just now.
	ProblemUnreachable = "unreachable"
)

// AccountHealth is one account of an environment and whether it works. No setting is in it.
type AccountHealth struct {
	Provider  string    `json:"provider"`
	Default   bool      `json:"default"`
	Working   bool      `json:"working"`
	Checked   bool      `json:"checked"` // the provider itself was asked (a bank transfer account is only bank details)
	Problem   string    `json:"problem,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GoLive is the answer to the one go-live question that payments-worker owns (phase 45).
type GoLive struct {
	Ready     bool            `json:"ready"`
	Providers []string        `json:"providers"`
	Accounts  []AccountHealth `json:"accounts"`
}

// GetGoLive answers: does the tenant have a payment provider connected in live mode that works? A provider counts when the tenant has a live-mode account of
// it whose stored settings still open and still build an adapter, and, for an adapter that can ask its provider, whose provider accepts the credentials now (a
// revoked or rotated key shows up here, not at the first charge). The gateway asks this when it computes the checklist; plans-svc never sees provider data.
func GetGoLive(ctx context.Context, d Deps, tenant uuid.UUID) (GoLive, error) {
	return GetHealth(ctx, d, tenant, env.Live.String())
}

// GetHealth is GetGoLive for one environment: the accounts the tenant has in it and whether each works. The console reads the sandbox's through it (the app's
// sandbox tenant and its test-mode accounts); Ready then only says that one works.
func GetHealth(ctx context.Context, d Deps, tenant uuid.UUID, environment string) (GoLive, error) {
	list, err := d.Accounts.List(ctx, tenant)
	if err != nil {
		return GoLive{}, fmt.Errorf("%s: accounts not listed: %w", environment, err)
	}
	out := GoLive{Providers: []string{}, Accounts: []AccountHealth{}}
	for _, a := range list {
		if a.Environment != environment {
			continue
		}
		h := AccountHealth{Provider: a.Provider, Default: a.IsDefault, UpdatedAt: a.UpdatedAt}
		h.Working, h.Checked, h.Problem = d.health(ctx, tenant, a)
		out.Accounts = append(out.Accounts, h)
		if h.Working {
			out.Providers = append(out.Providers, a.Provider)
		}
	}
	out.Ready = len(out.Providers) > 0
	return out, nil
}

func (d Deps) health(ctx context.Context, tenant uuid.UUID, a store.AccountInfo) (working, checked bool, problem string) {
	settings, updated, err := d.Accounts.Settings(ctx, tenant, a.Provider, a.Environment)
	if err != nil {
		d.Log.WarnContext(ctx, "go-live: account settings do not open", "provider", a.Provider, "err", err)
		return false, false, ProblemUnreadable
	}
	p, err := d.Registry.Build(a.Provider, settings)
	if err != nil {
		d.Log.WarnContext(ctx, "go-live: account settings do not build an adapter", "provider", a.Provider, "err", err)
		return false, false, ProblemUnreadable
	}
	key := fmt.Sprintf("%s|%s|%s|%d", tenant, a.Provider, a.Environment, updated.UnixNano())
	if got, ok := d.Health.get(key); ok {
		return got.problem == "", got.checked, got.problem
	}
	hctx, cancel := context.WithTimeout(ctx, HealthTimeout)
	defer cancel()
	res, err := payments.Check(hctx, p)
	ans := answer{checked: res.Remote}
	if err != nil {
		d.Log.WarnContext(ctx, "go-live: provider health check failed", "provider", a.Provider, "err", err)
		ans.problem = ProblemRefused
		if errors.Is(err, payments.ErrUnreachable) {
			ans.problem = ProblemUnreachable
		}
	}
	d.Health.put(key, ans)
	return ans.problem == "", ans.checked, ans.problem
}
