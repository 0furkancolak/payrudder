// Package query reads a tenant's payment provider accounts. It never writes, and no secret leaves it.
package query

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// Deps is what the queries read.
type Deps struct {
	Registry *payments.Registry
	Accounts store.Accounts
	Log      *slog.Logger
}

// List is the tenant's accounts, without their settings.
func List(ctx context.Context, d Deps, tenant uuid.UUID) ([]store.AccountInfo, error) {
	list, err := d.Accounts.List(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("list provider accounts: %w", err)
	}
	return list, nil
}

// GoLive is the answer to the one go-live question that payments-worker owns (phase 45).
type GoLive struct {
	Ready     bool     `json:"ready"`
	Providers []string `json:"providers"`
}

// GetGoLive answers: does the tenant have a payment provider connected in live mode that works? A provider counts when the tenant has a live-mode account of
// it whose stored settings still open and still build an adapter (the same check that stores them, so a rotated secret key or a broken record shows up here).
// The gateway asks this when it computes the checklist; plans-svc never sees provider data.
func GetGoLive(ctx context.Context, d Deps, tenant uuid.UUID) (GoLive, error) {
	list, err := d.Accounts.List(ctx, tenant)
	if err != nil {
		return GoLive{}, fmt.Errorf("go-live: accounts not listed: %w", err)
	}
	working := []string{}
	for _, a := range list {
		if a.Environment != env.Live.String() {
			continue
		}
		settings, _, err := d.Accounts.Settings(ctx, tenant, a.Provider, a.Environment)
		if err != nil {
			d.Log.WarnContext(ctx, "go-live: account settings do not open", "provider", a.Provider, "err", err)
			continue
		}
		if _, err := d.Registry.Build(a.Provider, settings); err != nil {
			d.Log.WarnContext(ctx, "go-live: account settings do not build an adapter", "provider", a.Provider, "err", err)
			continue
		}
		working = append(working, a.Provider)
	}
	return GoLive{Ready: len(working) > 0, Providers: working}, nil
}
