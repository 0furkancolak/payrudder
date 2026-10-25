package webhook

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/env"
	"github.com/0furkancolak/quotify/pkg/payments/banktransfer"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/store"
)

// syncBankTransfer copies the public part of the tenant's live bank transfer account to plans-svc, which hands it to billing-svc when an invoice is made, so the PDF,
// the hosted page and the mail can print where to pay. With no such account the details are cleared. The account itself is stored sealed here like every
// provider account; bank details are printed on invoices anyway, so plans-svc keeps them in the clear.
//
// It is called after a bank transfer account is stored or removed, and after any account is made the default (the details say whether they are the default
// account's, which decides whether invoices of customers without a provider show them).
func (s *Server) syncBankTransfer(ctx context.Context, tenant uuid.UUID) error {
	if s.Plans == nil {
		return nil
	}
	settings, _, err := s.Accounts.Settings(ctx, tenant, banktransfer.Name, env.Live.String())
	if errors.Is(err, store.ErrNoAccount) {
		return s.Plans.SetBankTransfer(ctx, tenant, nil)
	}
	if err != nil {
		return err
	}
	cfg, err := banktransfer.Parse(settings)
	if err != nil {
		return fmt.Errorf("stored bank transfer account: %w", err)
	}
	name, hasDefault, err := s.Accounts.Default(ctx, tenant, env.Live.String())
	if err != nil {
		return err
	}
	return s.Plans.SetBankTransfer(ctx, tenant, &plansv1.BankTransferDetails{BankName: cfg.BankName, AccountHolder: cfg.AccountHolder, Iban: cfg.IBAN, Bic: cfg.BIC,
		ReferenceTemplate: cfg.ReferenceTemplate, Currencies: cfg.Currencies, IsDefault: hasDefault && name == banktransfer.Name})
}
