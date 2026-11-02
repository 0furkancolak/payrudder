// Package command changes a tenant's e-invoice integrator account. The credentials are sealed on the way in and never come back.
package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/mock"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/payments"
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

// Put is the account to store for an environment. Label tells several accounts at one integrator apart ("nilvera" with the label "tr" is the account
// "nilvera~tr"); Default makes it the default account (the first account of an environment is one anyway).
type Put struct {
	Provider    string            `json:"provider"`
	Label       string            `json:"label"`
	Mode        string            `json:"mode"`
	Credentials map[string]string `json:"credentials"`
	Default     bool              `json:"default"`
}

// PutAccount validates and stores (or replaces) the tenant's account for the environment. The settings are validated by building the adapter; the credentials
// are never echoed back. actor is who asked, for the audit trail.
func PutAccount(ctx context.Context, d Deps, tenant uuid.UUID, environment, actor string, in Put) (store.EInvoiceAccountInfo, error) {
	switch {
	case !payments.ValidAccountLabel(in.Label):
		return store.EInvoiceAccountInfo{}, refuse.New(http.StatusBadRequest, "einvoice.account.invalid", "label: lower-case letters, digits and dashes, up to 31 characters")
	case environment == env.Sandbox.String() && (in.Provider != mock.Name || in.Label != ""):
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
	key := payments.AccountKey(in.Provider, in.Label)
	if err := d.Accounts.Put(ctx, tenant, environment, key, in.Mode, raw, in.Default, store.Audit{Actor: actor}); err != nil {
		return store.EInvoiceAccountInfo{}, fmt.Errorf("e-invoice account write failed: %w", err)
	}
	return store.EInvoiceAccountInfo{Environment: environment, Account: key, Provider: in.Provider, Label: in.Label, Mode: in.Mode, IsDefault: in.Default, HasCredentials: len(in.Credentials) > 0}, nil
}

// accountKey is the account a request names, or, when it names none and the environment has exactly one, that one (how a request worked before an environment
// could hold several). More than one account and no name is the caller's mistake.
func accountKey(ctx context.Context, d Deps, tenant uuid.UUID, environment, key string) (string, error) {
	if key != "" {
		if !payments.ValidAccountKey(key) {
			return "", refuse.New(http.StatusBadRequest, "common.invalid_argument", "account is not an account key")
		}
		return key, nil
	}
	list, err := d.Accounts.List(ctx, tenant)
	if err != nil {
		return "", fmt.Errorf("list the e-invoice accounts: %w", err)
	}
	var only []string
	for _, a := range list {
		if a.Environment == environment {
			only = append(only, a.Account)
		}
	}
	switch len(only) {
	case 0:
		return "", refuse.New(http.StatusNotFound, "einvoice.account.missing", "no such account")
	case 1:
		return only[0], nil
	}
	return "", refuse.New(http.StatusBadRequest, "common.invalid_argument", "the environment has several e-invoice accounts: name one with ?account=")
}

// DeleteAccount removes an account of the environment (the only one when none is named). The default is removed last: while others exist another is made the
// default first.
func DeleteAccount(ctx context.Context, d Deps, tenant uuid.UUID, environment, key, actor string) error {
	key, err := accountKey(ctx, d, tenant, environment, key)
	if err != nil {
		return err
	}
	deleted, err := d.Accounts.Delete(ctx, tenant, environment, key, store.Audit{Actor: actor})
	if errors.Is(err, store.ErrDefaultInUse) {
		return refuse.New(http.StatusConflict, "einvoice.account.default_in_use", "this is the default account: make another one the default first")
	}
	if err != nil {
		return fmt.Errorf("e-invoice account delete failed: %w", err)
	}
	if !deleted {
		return refuse.New(http.StatusNotFound, "einvoice.account.missing", "no such account")
	}
	return nil
}

// SetDefaultAccount makes a stored account the default of the environment.
func SetDefaultAccount(ctx context.Context, d Deps, tenant uuid.UUID, environment, key, actor string) error {
	if !payments.ValidAccountKey(key) {
		return refuse.New(http.StatusBadRequest, "common.invalid_argument", "account is not an account key")
	}
	found, err := d.Accounts.SetDefault(ctx, tenant, environment, key, store.Audit{Actor: actor})
	if err != nil {
		return fmt.Errorf("e-invoice account default failed: %w", err)
	}
	if !found {
		return refuse.New(http.StatusNotFound, "einvoice.account.missing", "no such account")
	}
	return nil
}

// maxRules bounds a routing list: there are fewer countries than that.
const maxRules = 300

var countryPattern = regexp.MustCompile(`^[A-Z]{2}$`)

// SetRouting replaces the routing rules of the environment: the documents of a buyer of each country go to the account named. A country appears once, and every
// account must exist.
func SetRouting(ctx context.Context, d Deps, tenant uuid.UUID, environment, actor string, rules []store.EInvoiceRoute) error {
	seen := map[string]bool{}
	if len(rules) > maxRules {
		return refuse.New(http.StatusBadRequest, "einvoice.routing.invalid", "too many rules")
	}
	for i := range rules {
		rules[i].Country = strings.ToUpper(strings.TrimSpace(rules[i].Country))
		switch {
		case !countryPattern.MatchString(rules[i].Country):
			return refuse.New(http.StatusBadRequest, "einvoice.routing.invalid", "country must be an ISO 3166-1 alpha-2 code")
		case seen[rules[i].Country]:
			return refuse.New(http.StatusBadRequest, "einvoice.routing.invalid", "a country can be named once: "+rules[i].Country)
		case !payments.ValidAccountKey(rules[i].Account):
			return refuse.New(http.StatusBadRequest, "einvoice.routing.invalid", "account is not an account key")
		}
		seen[rules[i].Country] = true
	}
	err := d.Accounts.SetRouting(ctx, tenant, environment, rules, store.Audit{Actor: actor})
	if errors.Is(err, store.ErrUnknownRouteAccount) {
		return refuse.New(http.StatusBadRequest, "einvoice.routing.unknown_account", "a rule names an account that is not connected")
	}
	if err != nil {
		return fmt.Errorf("e-invoice routing write failed: %w", err)
	}
	return nil
}
