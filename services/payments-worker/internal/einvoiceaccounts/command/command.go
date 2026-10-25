// Package command changes a tenant's e-invoice integrator account. The credentials are sealed on the way in and never come back.
package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/mock"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// Modes an integrator account can run in.
const (
	ModeTest       = "test"
	ModeProduction = "production"
)

// Deps is what the commands need. Without the sealed store or the registry the accounts are not configured on this deployment.
type Deps struct {
	Accounts store.EInvoiceAccounts
	Registry *einvoice.Registry
	// AllowMockLive lets a live account be the mock (demo mode).
	AllowMockLive bool
}

// Ready refuses with 503 when the deployment cannot hold e-invoice accounts.
func (d Deps) Ready() error {
	if d.Accounts.DB == nil || d.Registry == nil {
		return refuse.New(http.StatusServiceUnavailable, "common.unavailable", "e-invoice accounts are not configured on this deployment (PAYRUDDER_SECRETS_KEY)")
	}
	return nil
}

// Put is the account to store for an environment.
type Put struct {
	Provider    string            `json:"provider"`
	Mode        string            `json:"mode"`
	Credentials map[string]string `json:"credentials"`
}

// PutAccount validates and stores (or replaces) the tenant's account for the environment. The settings are validated by building the adapter; the credentials
// are never echoed back.
func PutAccount(ctx context.Context, d Deps, tenant uuid.UUID, environment string, in Put) (store.EInvoiceAccountInfo, error) {
	switch {
	case environment == env.Sandbox.String() && in.Provider != mock.Name:
		return store.EInvoiceAccountInfo{}, refuse.New(http.StatusBadRequest, "einvoice.account.sandbox_mock_only", "a sandbox account can only use the mock provider")
	case environment == env.Live.String() && in.Provider == mock.Name && !d.AllowMockLive:
		return store.EInvoiceAccountInfo{}, refuse.New(http.StatusBadRequest, "einvoice.account.invalid", "the mock provider is not allowed in the live environment")
	}
	if in.Mode == "" {
		in.Mode = ModeProduction
		if environment == env.Sandbox.String() {
			in.Mode = ModeTest
		}
	}
	if in.Mode != ModeTest && in.Mode != ModeProduction {
		return store.EInvoiceAccountInfo{}, refuse.New(http.StatusBadRequest, "einvoice.account.invalid", "mode must be test or production")
	}
	settings := maps.Clone(in.Credentials)
	if settings == nil {
		settings = map[string]string{}
	}
	settings["mode"] = in.Mode
	raw, err := json.Marshal(settings)
	if err != nil {
		return store.EInvoiceAccountInfo{}, fmt.Errorf("encode the e-invoice settings: %w", err)
	}
	// Building the adapter validates the settings (a URL that is not https, a missing key). The error text never contains a credential: adapters do not echo them.
	if _, err := d.Registry.Build(in.Provider, raw); err != nil {
		if errors.Is(err, einvoice.ErrUnknownProvider) {
			return store.EInvoiceAccountInfo{}, refuse.New(http.StatusNotFound, "einvoice.account.invalid", "unknown provider; known: "+strings.Join(d.Registry.Names(), ", "))
		}
		return store.EInvoiceAccountInfo{}, refuse.New(http.StatusBadRequest, "einvoice.account.invalid", err.Error())
	}
	if err := d.Accounts.Put(ctx, tenant, environment, in.Provider, in.Mode, raw); err != nil {
		return store.EInvoiceAccountInfo{}, fmt.Errorf("e-invoice account write failed: %w", err)
	}
	return store.EInvoiceAccountInfo{Environment: environment, Provider: in.Provider, Mode: in.Mode, HasCredentials: len(in.Credentials) > 0}, nil
}

// DeleteAccount removes the tenant's account for the environment.
func DeleteAccount(ctx context.Context, d Deps, tenant uuid.UUID, environment string) error {
	deleted, err := d.Accounts.Delete(ctx, tenant, environment)
	if err != nil {
		return fmt.Errorf("e-invoice account delete failed: %w", err)
	}
	if !deleted {
		return refuse.New(http.StatusNotFound, "einvoice.account.missing", "no such account")
	}
	return nil
}
