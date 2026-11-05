package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// EInvoiceAccounts is the table of a tenant's e-invoice integrator accounts. Settings are sealed; the box is bound to tenant, environment and account.
//
// An account is named by its key, as a payment account is (pkg/payments/accountkey.go): the integrator's name, or its name, a tilde and a label ("nilvera~tr").
// The key is what the `provider` column holds, what routing names and what an issuance records; only the registry looks through it to the adapter.
type EInvoiceAccounts struct {
	DB  DB
	Box *secretbox.Box
	// Shadow, when set, compares each account opened with the same account in the new tables (ADR 0026, step 3); what is returned does not change.
	Shadow *Shadow
	// Mirror, when set, writes every change to the new account tables too, in the same transaction (ADR 0026, step 3; PAYMENTS_ACCOUNT_MIRROR).
	Mirror bool
}

// EInvoiceAccountInfo is an account without its secrets.
type EInvoiceAccountInfo struct {
	Environment string `json:"environment"`
	// Account is the key ("nilvera", "nilvera~tr"); Provider the integrator it belongs to ("nilvera"); Label the part after the tilde, empty for the primary one.
	Account        string    `json:"account"`
	Provider       string    `json:"provider"`
	Label          string    `json:"label"`
	Mode           string    `json:"mode"`
	IsDefault      bool      `json:"is_default"`
	HasCredentials bool      `json:"has_credentials"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// EInvoiceAccount is a stored account with its settings opened.
type EInvoiceAccount struct {
	Key       string
	Mode      string
	Settings  []byte
	IsDefault bool
}

// EInvoiceRoute sends the documents of buyers of a country to an account.
type EInvoiceRoute struct {
	Country string `json:"country"`
	Account string `json:"account"`
}

// ErrDefaultInUse: the account is the default and others exist, so removing it would leave documents with no account to go to.
var ErrDefaultInUse = errors.New("store: the default e-invoice account cannot be removed while others exist")

// ErrUnknownRouteAccount: a routing rule names an account the tenant does not have.
var ErrUnknownRouteAccount = errors.New("store: routing names an unknown e-invoice account")

// einvoiceAAD binds a sealed account to its tenant, environment and key. The primary account of an integrator (no label) keeps the form it was sealed in before
// there could be several, so nothing stored earlier has to be re-sealed; a labelled account adds its key.
func einvoiceAAD(tenant uuid.UUID, env, key string) []byte {
	if payments.AccountLabel(key) == "" {
		return []byte(fmt.Sprintf("%s|einvoice|%s", tenant, env))
	}
	return []byte(fmt.Sprintf("%s|einvoice|%s|%s", tenant, env, key))
}

func einvoiceInfo(env, key, mode string, def bool, updated time.Time) EInvoiceAccountInfo {
	return EInvoiceAccountInfo{Environment: env, Account: key, Provider: payments.BaseProvider(key), Label: payments.AccountLabel(key), Mode: mode, IsDefault: def,
		HasCredentials: true, UpdatedAt: updated}
}

// einvoiceEntry is accountEntry for an e-invoice account: the entry of the platform's audit log (pkg/audit), in the transaction of the change, with no setting in it.
func einvoiceEntry(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action, key, env, summary string, detail map[string]any) error {
	return audit.Record(ctx, tx, audit.Event{TenantID: tenant, Action: action, TargetType: "einvoice_account", TargetID: strings.Trim(key+"/"+env, "/"), Summary: summary, Detail: detail})
}

// Put stores (or replaces) the account called key. The first account of an environment is the default; makeDefault makes this one the default, and the previous
// default stops being one. The change is recorded in the audit trail in the same transaction; no setting is.
func (a EInvoiceAccounts) Put(ctx context.Context, tenant uuid.UUID, env, key, mode string, settings []byte, makeDefault bool, who Audit) error {
	sealed, err := a.Box.Seal(settings, einvoiceAAD(tenant, env, key))
	if err != nil {
		return err
	}
	return tx(ctx, a.DB, tenant, func(tx pgx.Tx, q *sqlc.Queries) error {
		n, err := q.CountEInvoiceAccounts(ctx, sqlc.CountEInvoiceAccountsParams{TenantID: tenant, Environment: env})
		if err != nil {
			return err
		}
		action, isDefault := "connected", makeDefault || n == 0
		switch old, err := q.GetEInvoiceAccount(ctx, sqlc.GetEInvoiceAccountParams{TenantID: tenant, Environment: env, Provider: key}); {
		case err == nil:
			action, isDefault = "replaced", isDefault || old.IsDefault
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		if isDefault {
			if err := q.ClearDefaultEInvoiceAccounts(ctx, sqlc.ClearDefaultEInvoiceAccountsParams{TenantID: tenant, Environment: env, Provider: key}); err != nil {
				return err
			}
		}
		if err := q.UpsertEInvoiceAccount(ctx, sqlc.UpsertEInvoiceAccountParams{TenantID: tenant, Environment: env, Provider: key, Mode: mode, SettingsEnc: sealed, IsDefault: isDefault}); err != nil {
			return err
		}
		if a.Mirror {
			if err := mirrorUpsert(ctx, tx, a.Box, tenant, KindEInvoice, env, key, &mode, settings); err != nil {
				return err
			}
			if err := mirrorDefaults(ctx, tx, tenant, KindEInvoice, env, "einvoice_accounts"); err != nil {
				return err
			}
		}
		if err := q.InsertProviderAccountEvent(ctx, sqlc.InsertProviderAccountEventParams{TenantID: tenant, Kind: KindEInvoice, Provider: key, Environment: env, Action: action,
			Actor: who.Actor, IsDefault: isDefault}); err != nil {
			return err
		}
		return einvoiceEntry(ctx, tx, tenant, "einvoice_account."+action, key, env, fmt.Sprintf("%s the %s e-invoice account %s", action, env, key),
			map[string]any{"account": key, "environment": env, "mode": mode, "default": isDefault})
	})
}

// Delete removes the account called key. It reports whether there was one. The default cannot be removed while other accounts exist (ErrDefaultInUse): another is
// made the default first. The rules that name the account go with it.
func (a EInvoiceAccounts) Delete(ctx context.Context, tenant uuid.UUID, env, key string, who Audit) (bool, error) {
	var deleted bool
	err := tx(ctx, a.DB, tenant, func(tx pgx.Tx, q *sqlc.Queries) error {
		row, err := q.GetEInvoiceAccount(ctx, sqlc.GetEInvoiceAccountParams{TenantID: tenant, Environment: env, Provider: key})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.IsDefault {
			n, err := q.CountEInvoiceAccounts(ctx, sqlc.CountEInvoiceAccountsParams{TenantID: tenant, Environment: env})
			if err != nil {
				return err
			}
			if n > 1 {
				return ErrDefaultInUse
			}
		}
		if _, err := q.DeleteEInvoiceAccount(ctx, sqlc.DeleteEInvoiceAccountParams{TenantID: tenant, Environment: env, Provider: key}); err != nil {
			return err
		}
		deleted = true
		if a.Mirror {
			if err := mirrorDelete(ctx, tx, tenant, KindEInvoice, env, key); err != nil {
				return err
			}
		}
		if err := q.InsertProviderAccountEvent(ctx, sqlc.InsertProviderAccountEventParams{TenantID: tenant, Kind: KindEInvoice, Provider: key, Environment: env, Action: "removed", Actor: who.Actor}); err != nil {
			return err
		}
		return einvoiceEntry(ctx, tx, tenant, "einvoice_account.removed", key, env, fmt.Sprintf("removed the %s e-invoice account %s", env, key), map[string]any{"account": key, "environment": env})
	})
	return deleted, err
}

// SetDefault makes an existing account the default of the environment without touching its settings. It reports whether there was such an account.
func (a EInvoiceAccounts) SetDefault(ctx context.Context, tenant uuid.UUID, env, key string, who Audit) (bool, error) {
	var found bool
	err := tx(ctx, a.DB, tenant, func(tx pgx.Tx, q *sqlc.Queries) error {
		if err := q.ClearDefaultEInvoiceAccounts(ctx, sqlc.ClearDefaultEInvoiceAccountsParams{TenantID: tenant, Environment: env, Provider: key}); err != nil {
			return err
		}
		n, err := q.MarkEInvoiceAccountDefault(ctx, sqlc.MarkEInvoiceAccountDefaultParams{TenantID: tenant, Environment: env, Provider: key})
		if err != nil || n != 1 {
			if err == nil {
				err = ErrNoAccount // returning it rolls the clear back: the tenant's default stays where it was
			}
			return err
		}
		found = true
		if a.Mirror {
			if err := mirrorDefaults(ctx, tx, tenant, KindEInvoice, env, "einvoice_accounts"); err != nil {
				return err
			}
		}
		if err := q.InsertProviderAccountEvent(ctx, sqlc.InsertProviderAccountEventParams{TenantID: tenant, Kind: KindEInvoice, Provider: key, Environment: env, Action: "default_set",
			Actor: who.Actor, IsDefault: true}); err != nil {
			return err
		}
		return einvoiceEntry(ctx, tx, tenant, "einvoice_account.default_changed", key, env, fmt.Sprintf("made %s the default %s e-invoice account", key, env),
			map[string]any{"account": key, "environment": env})
	})
	if errors.Is(err, ErrNoAccount) {
		return false, nil
	}
	return found, err
}

// List returns a tenant's accounts without secrets.
func (a EInvoiceAccounts) List(ctx context.Context, tenant uuid.UUID) ([]EInvoiceAccountInfo, error) {
	rows, err := get(ctx, a.DB, tenant, func(q *sqlc.Queries) ([]sqlc.ListEInvoiceAccountsRow, error) {
		return q.ListEInvoiceAccounts(ctx, tenant)
	})
	if err != nil {
		return nil, err
	}
	out := make([]EInvoiceAccountInfo, len(rows))
	for i, r := range rows {
		out[i] = einvoiceInfo(r.Environment, r.Provider, r.Mode, r.IsDefault, r.UpdatedAt)
	}
	return out, nil
}

// Get opens the account called key; ErrNoAccount when there is none.
func (a EInvoiceAccounts) Get(ctx context.Context, tenant uuid.UUID, env, key string) (EInvoiceAccount, error) {
	row, ok, err := lookup(ctx, a.DB, tenant, func(q *sqlc.Queries) (sqlc.GetEInvoiceAccountRow, error) {
		return q.GetEInvoiceAccount(ctx, sqlc.GetEInvoiceAccountParams{TenantID: tenant, Environment: env, Provider: key})
	})
	if err != nil {
		return EInvoiceAccount{}, err
	}
	if !ok {
		return EInvoiceAccount{}, ErrNoAccount
	}
	return a.open(ctx, tenant, env, row.Provider, row.Mode, row.SettingsEnc, row.IsDefault)
}

// Resolve opens the account that takes the documents of a buyer of country: the one routing names for it, else the default. ErrNoAccount when neither exists
// (a tenant with no default has not finished connecting an integrator). country may be empty (an unknown buyer): the default.
func (a EInvoiceAccounts) Resolve(ctx context.Context, tenant uuid.UUID, env, country string) (EInvoiceAccount, error) {
	if country != "" {
		key, ok, err := lookup(ctx, a.DB, tenant, func(q *sqlc.Queries) (string, error) {
			return q.GetEInvoiceRoute(ctx, sqlc.GetEInvoiceRouteParams{TenantID: tenant, Environment: env, Country: country})
		})
		if err != nil {
			return EInvoiceAccount{}, err
		}
		if ok {
			return a.Get(ctx, tenant, env, key)
		}
	}
	row, ok, err := lookup(ctx, a.DB, tenant, func(q *sqlc.Queries) (sqlc.GetDefaultEInvoiceAccountRow, error) {
		return q.GetDefaultEInvoiceAccount(ctx, sqlc.GetDefaultEInvoiceAccountParams{TenantID: tenant, Environment: env})
	})
	if err != nil {
		return EInvoiceAccount{}, err
	}
	if !ok {
		return EInvoiceAccount{}, ErrNoAccount
	}
	return a.open(ctx, tenant, env, row.Provider, row.Mode, row.SettingsEnc, row.IsDefault)
}

func (a EInvoiceAccounts) open(ctx context.Context, tenant uuid.UUID, env, key, mode string, sealed []byte, def bool) (EInvoiceAccount, error) {
	settings, err := a.Box.Open(sealed, einvoiceAAD(tenant, env, key))
	if err == nil {
		settings = a.Shadow.Read(ctx, tenant, KindEInvoice, key, env, settings)
	}
	return EInvoiceAccount{Key: key, Mode: mode, Settings: settings, IsDefault: def}, err
}

// Routing returns the rules of an environment, by country.
func (a EInvoiceAccounts) Routing(ctx context.Context, tenant uuid.UUID, env string) ([]EInvoiceRoute, error) {
	rows, err := get(ctx, a.DB, tenant, func(q *sqlc.Queries) ([]sqlc.ListEInvoiceRoutingRow, error) {
		return q.ListEInvoiceRouting(ctx, sqlc.ListEInvoiceRoutingParams{TenantID: tenant, Environment: env})
	})
	if err != nil {
		return nil, err
	}
	out := make([]EInvoiceRoute, len(rows))
	for i, r := range rows {
		out[i] = EInvoiceRoute{Country: r.Country, Account: r.Account}
	}
	return out, nil
}

// SetRouting replaces the rules of an environment. Every rule must name an account the tenant has (ErrUnknownRouteAccount). The change is one entry of the audit
// trail; the rules themselves are not in it.
func (a EInvoiceAccounts) SetRouting(ctx context.Context, tenant uuid.UUID, env string, rules []EInvoiceRoute, who Audit) error {
	return tx(ctx, a.DB, tenant, func(tx pgx.Tx, q *sqlc.Queries) error {
		rows, err := q.ListEInvoiceAccounts(ctx, tenant)
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for _, r := range rows {
			if r.Environment == env {
				have[r.Provider] = true
			}
		}
		for _, r := range rules {
			if !have[r.Account] {
				return ErrUnknownRouteAccount
			}
		}
		if err := q.DeleteEInvoiceRouting(ctx, sqlc.DeleteEInvoiceRoutingParams{TenantID: tenant, Environment: env}); err != nil {
			return err
		}
		for _, r := range rules {
			if err := q.InsertEInvoiceRoute(ctx, sqlc.InsertEInvoiceRouteParams{TenantID: tenant, Environment: env, Country: r.Country, Account: r.Account}); err != nil {
				return err
			}
		}
		if err := q.InsertProviderAccountEvent(ctx, sqlc.InsertProviderAccountEventParams{TenantID: tenant, Kind: KindEInvoice, Environment: env, Action: "routing_changed", Actor: who.Actor}); err != nil {
			return err
		}
		return einvoiceEntry(ctx, tx, tenant, "einvoice_account.routing_changed", "", env, fmt.Sprintf("changed the %s e-invoice routing (%d rules)", env, len(rules)),
			map[string]any{"environment": env, "rules": len(rules)})
	})
}

// EInvoiceIssuances is the idempotency record of issued documents.
type EInvoiceIssuances struct{ DB DB }

// Issuance is the record of one document.
type Issuance struct {
	DocumentID                                uuid.UUID
	Environment, Provider, Kind, DocumentType string
	Status, LegalNumber, ExternalID           string
	XMLKey, PDFKey                            string
}

// Begin records that the integrator is about to be called for the document. It reports whether the row was new.
func (i EInvoiceIssuances) Begin(ctx context.Context, doc, tenant uuid.UUID, env, provider, kind, docType string) (bool, error) {
	n, err := get(ctx, i.DB, tenant, func(q *sqlc.Queries) (int64, error) {
		return q.BeginEInvoiceIssuance(ctx, sqlc.BeginEInvoiceIssuanceParams{DocumentID: doc, TenantID: tenant, Environment: env, Provider: provider, Kind: kind, DocumentType: docType})
	})
	return n == 1, err
}

// Get returns the record of a document, if any.
func (i EInvoiceIssuances) Get(ctx context.Context, doc, tenant uuid.UUID) (Issuance, bool, error) {
	r, ok, err := lookup(ctx, i.DB, tenant, func(q *sqlc.Queries) (sqlc.GetEInvoiceIssuanceRow, error) {
		return q.GetEInvoiceIssuance(ctx, sqlc.GetEInvoiceIssuanceParams{DocumentID: doc, TenantID: tenant})
	})
	if !ok {
		return Issuance{}, false, err
	}
	return Issuance{DocumentID: r.DocumentID, Environment: r.Environment, Provider: r.Provider, Kind: r.Kind, DocumentType: r.DocumentType, Status: r.Status,
		LegalNumber: r.LegalNumber, ExternalID: r.ExternalID, XMLKey: r.XmlKey, PDFKey: r.PdfKey}, true, nil
}

// Complete records the integrator's answer: from now on the document is issued under that legal number, for good.
func (i EInvoiceIssuances) Complete(ctx context.Context, doc, tenant uuid.UUID, legal, external, xmlKey, pdfKey string) error {
	return at(ctx, i.DB, tenant, func(q *sqlc.Queries) error {
		return q.CompleteEInvoiceIssuance(ctx, sqlc.CompleteEInvoiceIssuanceParams{DocumentID: doc, TenantID: tenant, LegalNumber: &legal, ExternalID: external,
			XmlKey: nilIfEmpty(xmlKey), PdfKey: nilIfEmpty(pdfKey)})
	})
}

// Cancel marks an issued document cancelled.
func (i EInvoiceIssuances) Cancel(ctx context.Context, doc, tenant uuid.UUID) error {
	return at(ctx, i.DB, tenant, func(q *sqlc.Queries) error {
		return q.CancelEInvoiceIssuance(ctx, sqlc.CancelEInvoiceIssuanceParams{DocumentID: doc, TenantID: tenant})
	})
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
