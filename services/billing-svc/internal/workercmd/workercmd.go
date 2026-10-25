// Package workercmd puts the commands billing-svc gives its workers (payments-worker, invoice-worker, notifier) into the outbox, in the transaction of the change
// that asks for them. The saga engine, the e-invoice documents and the credit note refunds all send commands the same way; this is that way.
package workercmd

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
)

// Header is what a command carries besides the command itself.
type Header struct {
	// CommandID is the outbox message id: sending the same id twice is a no-op. SagaID is what the worker's reply finds its owner by (the e-invoice document id for
	// an e-document).
	CommandID, SagaID, TenantID uuid.UUID
	Attempt                     int
	IssuedAt                    time.Time
	// Environment is the tenant's, from the snapshot frozen with the invoice. It is never defaulted: a command whose snapshot names none is not sent anywhere.
	Environment string
	// Subject says what the command is for ("saga <id>"); it prefixes the errors.
	Subject string
	// Tenants, when set, is asked whether the environment agrees with the tenant's own (it always does unless a row was written by hand).
	Tenants *env.Directory
}

// Send stamps cmd with the header and puts it into the outbox for the queue route of the commands exchange.
func Send(ctx context.Context, tx pgx.Tx, h Header, cmd *commandsv1.CommandEnvelope, route string) error {
	cmd.CommandId, cmd.SagaId, cmd.TenantId = h.CommandID.String(), h.SagaID.String(), h.TenantID.String()
	cmd.Attempt = int32(h.Attempt) //nolint:gosec // small
	cmd.IssuedAt = timestamppb.New(h.IssuedAt)
	environment, err := env.Parse(h.Environment)
	if err != nil {
		return fmt.Errorf("%s: %w", h.Subject, err)
	}
	cmd.Environment = environment.Proto()
	if h.Tenants != nil {
		if err := h.Tenants.Check(ctx, h.TenantID, cmd.Environment, h.Subject+" command"); err != nil {
			return err
		}
	}
	body, err := proto.Marshal(cmd)
	if err != nil {
		return err
	}
	return store.EnqueueOutbox(ctx, tx, h.TenantID, h.CommandID, rabbitx.ExchangeCommands, route, outbox.DestRabbitMQ, body)
}
