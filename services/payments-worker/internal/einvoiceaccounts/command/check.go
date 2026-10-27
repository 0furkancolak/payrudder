package command

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// Check is what a test of an account found.
type Check struct {
	OK bool `json:"ok"`
	// Checked says how far the test went: "credentials" when the integrator was asked and accepted them, "configuration" when the adapter has nothing to ask
	// (the account's settings are valid, but only a real document proves the rest).
	Checked string `json:"checked"`
	// Problem is "refused" (the integrator did not accept the credentials) or "unreachable" (it did not answer); empty when OK.
	Problem string `json:"problem,omitempty"`
}

// probeTaxID is asked about when the caller names none: any well-formed tax number does, the answer is not used.
const probeTaxID = "1111111111"

// CheckAccount tests the stored account of the environment: it opens the settings, builds the adapter and, for an integrator that can look up a registered
// business (einvoice.BuyerChecker), asks it about taxID, which needs valid credentials. Nothing is issued and nothing is stored. A problem is the answer, not an
// error of the call; an account that does not exist is a 404.
func CheckAccount(ctx context.Context, d Deps, tenant uuid.UUID, environment, taxID string) (Check, error) {
	provider, settings, err := d.Accounts.Settings(ctx, tenant, environment)
	if errors.Is(err, store.ErrNoAccount) {
		return Check{}, refuse.New(http.StatusNotFound, "einvoice.account.missing", "no such account")
	}
	if err != nil {
		return Check{}, fmt.Errorf("open the e-invoice account: %w", err)
	}
	p, err := d.Registry.Build(provider, settings)
	if err != nil {
		return Check{OK: false, Checked: "configuration", Problem: "refused"}, nil
	}
	bc, ok := p.(einvoice.BuyerChecker)
	if !ok {
		return Check{OK: true, Checked: "configuration"}, nil
	}
	if taxID == "" {
		taxID = probeTaxID
	}
	if _, _, err := bc.IsEInvoiceUser(ctx, taxID); err != nil {
		var perm *einvoice.PermanentError
		if errors.As(err, &perm) {
			return Check{Checked: "credentials", Problem: "refused"}, nil
		}
		return Check{Checked: "credentials", Problem: "unreachable"}, nil
	}
	return Check{OK: true, Checked: "credentials"}, nil
}
