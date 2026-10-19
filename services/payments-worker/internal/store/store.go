// Package store is payments-worker's payment_attempts table: the inbox that makes a redelivered command return its
// stored reply instead of touching the provider again.
package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
)

// Attempts implements workerx.Inbox over payment_attempts.
type Attempts struct{ DB *pgxpool.Pool }

// Lookup returns the stored reply of a finished command.
func (a Attempts) Lookup(ctx context.Context, commandID uuid.UUID) (*commandsv1.ReplyEnvelope, bool, error) {
	var raw []byte
	err := a.DB.QueryRow(ctx, `SELECT reply FROM payment_attempts WHERE command_id = $1 AND status = 'DONE'`, commandID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var r commandsv1.ReplyEnvelope
	if err := proto.Unmarshal(raw, &r); err != nil {
		return nil, false, err
	}
	return &r, true, nil
}

// Store records the reply and marks the attempt DONE.
func (a Attempts) Store(ctx context.Context, commandID uuid.UUID, reply *commandsv1.ReplyEnvelope) error {
	raw, err := proto.Marshal(reply)
	if err != nil {
		return err
	}
	_, err = a.DB.Exec(ctx, `UPDATE payment_attempts SET reply = $2, status = 'DONE', finished_at = now() WHERE command_id = $1`, commandID, raw)
	return err
}

// Start records that the provider is about to be called (a crash after this and before Store leaves a STARTED row). It
// reports whether the row was already there: the command is a redelivery and the provider may have been called. With a
// provider whose charges are idempotent that does not matter; with one whose are not, the caller asks the provider first.
func (a Attempts) Start(ctx context.Context, commandID, tenantID, invoiceID uuid.UUID, kind, environment, provider, idempotencyKey string, request any) (bool, error) {
	b, err := json.Marshal(request)
	if err != nil {
		return false, err
	}
	tag, err := a.DB.Exec(ctx, `INSERT INTO payment_attempts (command_id, tenant_id, invoice_id, kind, environment, provider, idempotency_key, request, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'STARTED') ON CONFLICT (command_id) DO NOTHING`,
		commandID, tenantID, invoiceID, kind, environment, provider, idempotencyKey, b)
	return err == nil && tag.RowsAffected() == 0, err
}
