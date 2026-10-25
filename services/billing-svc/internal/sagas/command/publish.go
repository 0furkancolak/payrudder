package command

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/workercmd"
)

// ChargeKey is the PSP idempotency key of a charge: stable across retries of the same dunning attempt.
func ChargeKey(invoice uuid.UUID, dunning int) string {
	return fmt.Sprintf("%s:charge:%d", invoice, dunning)
}

// CommandID is uuidv5(saga_id|step|attempt).
func CommandID(sagaID uuid.UUID, step string, attempt int) uuid.UUID {
	return uuid.NewSHA1(commandNS, fmt.Appendf(nil, "%s|%s|%d", sagaID, step, attempt))
}

// publishCommand sends the command of the saga's current step. The environment is the tenant's, from the snapshot frozen with the invoice; where the engine can
// reach plans-svc it also asserts that the snapshot agrees with the tenant.
func (e *Engine) publishCommand(ctx context.Context, tx pgx.Tx, s *saga, cmd *commandsv1.CommandEnvelope, step, route string) error {
	return workercmd.Send(ctx, tx, workercmd.Header{
		CommandID: CommandID(s.ID, step, s.Attempt), SagaID: s.ID, TenantID: s.TenantID, Attempt: s.Attempt, IssuedAt: e.cfg.Now(),
		Environment: s.Snap.Environment, Subject: "saga " + s.ID.String(), Tenants: e.cfg.Tenants,
	}, cmd, route)
}
