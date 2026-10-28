// Package command changes a tenant's payment provider accounts. The settings are validated by building the adapter, sealed on the way in and never echoed back.
package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// BankTransferSetter is what the commands ask of plans-svc: the bank details printed on a tenant's invoices.
type BankTransferSetter interface {
	// SetBankTransfer replaces the bank details printed on the tenant's invoices; nil clears them.
	SetBankTransfer(ctx context.Context, tenant uuid.UUID, d *plansv1.BankTransferDetails) error
}

// Deps is what the commands need. Plans nil means plans-svc is not configured: bank details are then not passed on.
type Deps struct {
	Registry *payments.Registry
	Accounts store.Accounts
	Plans    BankTransferSetter
	Log      *slog.Logger
}

// HealthTimeout bounds the one call a health check makes to a provider.
const HealthTimeout = 8 * time.Second

// Put is a provider account to store.
type Put struct {
	Tenant      uuid.UUID
	Provider    string
	Environment string
	Settings    []byte
	MakeDefault bool
	// Actor is who asked, for the audit trail: "user:<id>" or "operator".
	Actor string
	// Verify is set when a person connects the account themselves (not support): the endpoint must be one of the provider's documented hosts (the platform would
	// otherwise send the person's secret keys wherever they point it), and the provider must accept the credentials before they are stored.
	Verify bool
}

// PutAccount stores (or replaces) an account. The settings are validated by building the adapter, and the provider's own mode must be the environment's.
func PutAccount(ctx context.Context, d Deps, in Put) error {
	if payments.BaseProvider(in.Provider) == banktransfer.Name && in.Provider != banktransfer.Name {
		return refuse.New(http.StatusBadRequest, "payments.account.single_only", "bank transfer has one account per environment")
	}
	built, err := d.Registry.Build(in.Provider, in.Settings)
	if err != nil {
		if errors.Is(err, payments.ErrUnknownProvider) {
			return refuse.New(http.StatusNotFound, "payments.provider.unknown", "unknown provider; known: "+strings.Join(d.Registry.Names(), ", "))
		}
		return refuse.New(http.StatusBadRequest, "payments.account.bad_settings", err.Error())
	}
	var remote bool
	if in.Verify {
		hctx, cancel := context.WithTimeout(ctx, HealthTimeout)
		defer cancel()
		h, err := payments.Verify(hctx, built, in.Environment)
		if err != nil {
			d.Log.WarnContext(ctx, "provider account refused", "provider", in.Provider, "environment", in.Environment, "err", err)
			switch {
			case errors.Is(err, payments.ErrUnknownEndpoint):
				return refuse.New(http.StatusBadRequest, "payments.account.endpoint_not_allowed", "base_url must be one of the provider's own hosts: leave it out to use the live one")
			case errors.Is(err, payments.ErrLivemodeMismatch):
				return refuse.New(http.StatusBadRequest, "payments.account.livemode_mismatch", err.Error())
			case errors.Is(err, payments.ErrUnreachable):
				return refuse.New(http.StatusBadGateway, "payments.account.health_check_unreachable", "the provider could not be reached to check these credentials: try again")
			default:
				return refuse.New(http.StatusBadRequest, "payments.account.health_check_failed", "the provider did not accept these credentials")
			}
		}
		remote = h.Remote
	} else if err := payments.CheckLivemode(built, in.Environment); err != nil {
		// A provider connection has a mode of its own (live or test at the provider), and it must be the environment's.
		return refuse.New(http.StatusBadRequest, "payments.account.livemode_mismatch", err.Error())
	}
	if err := d.Accounts.PutAudited(ctx, in.Tenant, in.Provider, in.Environment, in.Settings, in.MakeDefault, store.Audit{Actor: in.Actor, VerifiedRemote: remote}); err != nil {
		return fmt.Errorf("provider account write failed: %w", err)
	}
	if in.Environment == env.Live.String() && (in.Provider == banktransfer.Name || in.MakeDefault) {
		if err := syncBankTransfer(ctx, d, in.Tenant); err != nil {
			d.Log.ErrorContext(ctx, "bank details not passed on to plans-svc", "err", err)
			return refuse.New(http.StatusBadGateway, "payments.account.sync_failed", "the account is stored, but plans-svc could not be told the bank details for the invoices; send the request again")
		}
	}
	return nil
}

// SetDefaultAccount makes a stored account the default of the environment. Bank details follow the default (see syncBankTransfer).
func SetDefaultAccount(ctx context.Context, d Deps, tenant uuid.UUID, provider, environment string) error {
	found, err := d.Accounts.SetDefault(ctx, tenant, provider, environment)
	if err != nil {
		return fmt.Errorf("provider account default failed: %w", err)
	}
	if !found {
		return refuse.New(http.StatusNotFound, "payments.account.not_found", "no such account")
	}
	if environment == env.Live.String() {
		if err := syncBankTransfer(ctx, d, tenant); err != nil {
			d.Log.ErrorContext(ctx, "bank details not passed on to plans-svc", "err", err)
			return refuse.New(http.StatusBadGateway, "payments.account.sync_failed", "the default is changed, but plans-svc could not be told the bank details for the invoices; send the request again")
		}
	}
	return nil
}

// DeleteAccount removes a tenant's account at a provider in an environment; actor is who asked, for the audit trail.
func DeleteAccount(ctx context.Context, d Deps, tenant uuid.UUID, provider, environment, actor string) error {
	deleted, err := d.Accounts.DeleteAudited(ctx, tenant, provider, environment, store.Audit{Actor: actor})
	if err != nil {
		return fmt.Errorf("provider account delete failed: %w", err)
	}
	if !deleted {
		return refuse.New(http.StatusNotFound, "payments.account.not_found", "no such account")
	}
	if environment == env.Live.String() && provider == banktransfer.Name {
		if err := syncBankTransfer(ctx, d, tenant); err != nil {
			d.Log.ErrorContext(ctx, "bank details not cleared at plans-svc", "err", err)
			return refuse.New(http.StatusBadGateway, "payments.account.sync_failed", "the account is removed, but plans-svc could not be told; its bank details stay on new invoices until the request is sent again")
		}
	}
	return nil
}

// syncBankTransfer copies the public part of the tenant's live bank transfer account to plans-svc, which hands it to billing-svc when an invoice is made, so the PDF,
// the hosted page and the mail can print where to pay. With no such account the details are cleared. The account itself is stored sealed here like every
// provider account; bank details are printed on invoices anyway, so plans-svc keeps them in the clear.
//
// It is called after a bank transfer account is stored or removed, and after any account is made the default (the details say whether they are the default
// account's, which decides whether invoices of customers without a provider show them).
func syncBankTransfer(ctx context.Context, d Deps, tenant uuid.UUID) error {
	if d.Plans == nil {
		return nil
	}
	settings, _, err := d.Accounts.Settings(ctx, tenant, banktransfer.Name, env.Live.String())
	if errors.Is(err, store.ErrNoAccount) {
		return d.Plans.SetBankTransfer(ctx, tenant, nil)
	}
	if err != nil {
		return err
	}
	cfg, err := banktransfer.Parse(settings)
	if err != nil {
		return fmt.Errorf("stored bank transfer account: %w", err)
	}
	name, hasDefault, err := d.Accounts.Default(ctx, tenant, env.Live.String())
	if err != nil {
		return err
	}
	return d.Plans.SetBankTransfer(ctx, tenant, &plansv1.BankTransferDetails{BankName: cfg.BankName, AccountHolder: cfg.AccountHolder, Iban: cfg.IBAN, Bic: cfg.BIC,
		ReferenceTemplate: cfg.ReferenceTemplate, Currencies: cfg.Currencies, IsDefault: hasDefault && name == banktransfer.Name})
}
