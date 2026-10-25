// Package command changes the payment ledger's data: the reset of a sandbox tenant.
package command

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
)

// ResetFunc deletes a sandbox tenant's data and reports the rows by table.
type ResetFunc func(ctx context.Context, tenant uuid.UUID) (map[string]int64, error)

// Deps is what the commands need.
type Deps struct {
	// Reset deletes a sandbox tenant's attempts, ledger and receipts; nil means the reset is not configured.
	Reset ResetFunc
}

// Configured reports whether a sandbox can be reset here.
func (d Deps) Configured() bool { return d.Reset != nil }

// ResetSandbox deletes a sandbox tenant's payment attempts, ledger and receipts (phase 45 E). The environment the caller names must be the sandbox:
// anything else is refused here too, whoever the caller is.
func ResetSandbox(ctx context.Context, d Deps, tenant uuid.UUID, environment string) (map[string]int64, error) {
	if e, err := env.Parse(environment); err != nil || !e.IsSandbox() {
		return nil, refuse.New(http.StatusConflict, "admin.sandbox.live_refused", "only a sandbox tenant can be reset")
	}
	deleted, err := d.Reset(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("sandbox reset of tenant %s: %w", tenant, err)
	}
	return deleted, nil
}
