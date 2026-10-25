// Package tenantevents writes the events billing-svc announces to a tenant's webhook endpoints into the outbox, in the transaction of the change they announce.
// It sits below the saga and the credits module so that both can emit without importing each other.
package tenantevents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

var namespace = uuid.MustParse("3b9d6e12-7a40-5c85-91f3-e0b2a8d45c17")

// Event types of the credits module (plans-svc has the full list).
const (
	CreditsPurchased = "credits.purchased"
	CreditsLow       = "credits.low"
)

// Write puts an event for the tenant's endpoints into the outbox in tx. key says which occurrence it is: the same (tenant, type, key) is one event however often
// it is written.
func Write(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, typ, key string, at time.Time, data map[string]any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	id := uuid.NewSHA1(namespace, fmt.Appendf(nil, "%s|%s|%s", tenantID, typ, key))
	return sqlc.New(tx).InsertTenantEvent(ctx, sqlc.InsertTenantEventParams{TenantID: tenantID, EventID: id, Type: typ, Data: b, OccurredAt: at})
}
