// Package store is payments-worker's payment_attempts table: the inbox that makes a redelivered command return its
// stored reply instead of touching the provider again.
package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// Attempts implements workerx.Inbox (and its tenant-aware form, workerx.TenantInbox) over payment_attempts.
type Attempts struct{ DB DB }

// found maps pgx.ErrNoRows to "not there": the value, whether it was found, and any other error.
func found[T any](v T, err error) (T, bool, error) {
	if err != nil {
		var zero T
		return zero, false, notFound(err)
	}
	return v, true, nil
}

// notFound turns pgx.ErrNoRows into nil and keeps every other error.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

// Lookup returns the stored reply of a finished command. It does not know the command's tenant, so it can only look where every tenant is when cells are off;
// with cells on it refuses, and the worker uses LookupFor.
func (a Attempts) Lookup(ctx context.Context, commandID uuid.UUID) (*commandsv1.ReplyEnvelope, bool, error) {
	if err := requireTenant(a.DB, uuid.Nil); err != nil {
		return nil, false, err
	}
	return a.LookupFor(ctx, uuid.Nil, commandID)
}

// LookupFor returns the stored reply of a finished command of the tenant. A command that names no tenant has no reply (the handler refuses it before it could
// store one), so with cells on there is nowhere to look.
func (a Attempts) LookupFor(ctx context.Context, tenant, commandID uuid.UUID) (*commandsv1.ReplyEnvelope, bool, error) {
	if requireTenant(a.DB, tenant) != nil {
		return nil, false, nil
	}
	raw, ok, err := lookup(ctx, a.DB, tenant, func(q *sqlc.Queries) ([]byte, error) { return q.GetAttemptReply(ctx, commandID) })
	if !ok {
		return nil, false, err
	}
	var r commandsv1.ReplyEnvelope
	if err := proto.Unmarshal(raw, &r); err != nil {
		return nil, false, err
	}
	return &r, true, nil
}

// Store records the reply and marks the attempt DONE (see Lookup for the tenant).
func (a Attempts) Store(ctx context.Context, commandID uuid.UUID, reply *commandsv1.ReplyEnvelope) error {
	if err := requireTenant(a.DB, uuid.Nil); err != nil {
		return err
	}
	return a.StoreFor(ctx, uuid.Nil, commandID, reply)
}

// StoreFor records the reply of a command of the tenant and marks the attempt DONE.
func (a Attempts) StoreFor(ctx context.Context, tenant, commandID uuid.UUID, reply *commandsv1.ReplyEnvelope) error {
	if err := requireTenant(a.DB, tenant); err != nil {
		return err
	}
	raw, err := proto.Marshal(reply)
	if err != nil {
		return err
	}
	return at(ctx, a.DB, tenant, func(q *sqlc.Queries) error {
		return q.StoreAttemptReply(ctx, sqlc.StoreAttemptReplyParams{CommandID: commandID, Reply: raw})
	})
}

// Start records that the provider is about to be called (a crash after this and before Store leaves a STARTED row). It
// reports whether the row was already there: the command is a redelivery and the provider may have been called. With a
// provider whose charges are idempotent that does not matter; with one whose are not, the caller asks the provider first.
func (a Attempts) Start(ctx context.Context, commandID, tenantID, invoiceID uuid.UUID, kind, environment, provider, idempotencyKey string, request any) (bool, error) {
	b, err := json.Marshal(request)
	if err != nil {
		return false, err
	}
	if err := requireTenant(a.DB, tenantID); err != nil {
		return false, err
	}
	n, err := get(ctx, a.DB, tenantID, func(q *sqlc.Queries) (int64, error) {
		return q.StartAttempt(ctx, sqlc.StartAttemptParams{
			CommandID: commandID, TenantID: tenantID, InvoiceID: invoiceID, Kind: kind, Environment: environment,
			Provider: provider, IdempotencyKey: idempotencyKey, Request: b,
		})
	})
	return err == nil && n == 0, err
}
