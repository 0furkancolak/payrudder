// Package query lists a tenant's e-invoice integrator accounts. It never writes.
package query

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// List is the tenant's accounts, without their credentials.
func List(ctx context.Context, accounts store.EInvoiceAccounts, tenant uuid.UUID) ([]store.EInvoiceAccountInfo, error) {
	list, err := accounts.List(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("list e-invoice accounts: %w", err)
	}
	if list == nil {
		list = []store.EInvoiceAccountInfo{}
	}
	return list, nil
}

// Routing is the routing rules of an environment, by country.
func Routing(ctx context.Context, accounts store.EInvoiceAccounts, tenant uuid.UUID, environment string) ([]store.EInvoiceRoute, error) {
	rules, err := accounts.Routing(ctx, tenant, environment)
	if err != nil {
		return nil, fmt.Errorf("list e-invoice routing: %w", err)
	}
	if rules == nil {
		rules = []store.EInvoiceRoute{}
	}
	return rules, nil
}
