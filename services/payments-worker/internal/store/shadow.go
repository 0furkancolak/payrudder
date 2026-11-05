package store

import (
	"bytes"
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// Step 3 of the account_id migration (ADR 0026): the new tables are read beside the old and compared; the old row is still the one that is used. This is a shadow, not a
// dual read: a read that preferred the binding would serve stale settings to the payment path until the writes go to both tables (the next step of the ADR), and a
// charge made with an old credential is the failure the rehearsal exists to prevent. When the writes are mirrored, this comparison is what proves the two equal, and
// returning the binding instead of the old row is then one line.

// boundAAD is the associated data of an integration_accounts box: the account id and nothing else (places.go of pkg/secretbox/reseal writes it too).
func boundAAD(id uuid.UUID) []byte { return []byte("integration-account:" + id.String()) }

// BoundSettings opens the settings of the account the tenant's binding of key names in the environment (kind KindPayment or KindEInvoice). found is false when the tenant
// has no such binding.
func (a Accounts) BoundSettings(ctx context.Context, tenant uuid.UUID, kind, key, env string) (settings []byte, found bool, err error) {
	row, ok, err := lookup(ctx, a.DB, tenant, func(q *sqlc.Queries) (sqlc.GetBoundAccountSettingsRow, error) {
		return q.GetBoundAccountSettings(ctx, sqlc.GetBoundAccountSettingsParams{TenantID: tenant, Environment: env, Kind: kind,
			Provider: payments.BaseProvider(key), Label: payments.AccountLabel(key)})
	})
	if err != nil || !ok {
		return nil, false, err
	}
	settings, err = a.Box.Open(row.SettingsEnc, boundAAD(row.ID))
	return settings, true, err
}

// Shadow compares the settings read from an old row with the bound account's. A nil *Shadow does nothing, which is how the service runs unless PAYMENTS_ACCOUNT_SHADOW is on.
type Shadow struct {
	Accounts Accounts
	Log      *slog.Logger
}

var shadowReads = func() metric.Int64Counter {
	c, err := otel.Meter("github.com/0furkancolak/payrudder/services/payments-worker").Int64Counter("account_shadow_reads",
		metric.WithDescription("Reads of the new account tables beside the old, by kind and result (equal, missing, differs, error)"))
	if err != nil {
		panic(err)
	}
	return c
}()

// Compare reads the binding and counts the result; it never fails the caller and never returns what it read. Nothing but ids and the result goes to the log.
func (s *Shadow) Compare(ctx context.Context, tenant uuid.UUID, kind, key, env string, old []byte) string {
	if s == nil {
		return ""
	}
	result := "equal"
	got, found, err := s.Accounts.BoundSettings(ctx, tenant, kind, key, env)
	switch {
	case errors.Is(err, context.Canceled):
		return ""
	case err != nil:
		result = "error"
	case !found:
		result = "missing"
	case !bytes.Equal(got, old):
		result = "differs"
	}
	shadowReads.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", kind), attribute.String("result", result)))
	if result != "equal" && s.Log != nil {
		s.Log.WarnContext(ctx, "the account in the new tables is not the account in the old", "tenant", tenant, "kind", kind, "account", key, "environment", env, "result", result, "error", err)
	}
	return result
}
