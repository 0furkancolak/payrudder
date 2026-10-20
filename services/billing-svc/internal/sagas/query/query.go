// Package query reads invoice sagas and their history for the operator. It never writes.
package query

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// Saga reads one saga. The error is the database's, unmapped.
func Saga(ctx context.Context, system *pgxpool.Pool, tenantID, sagaID uuid.UUID) (sqlc.GetSagaRow, error) {
	return sqlc.New(system).GetSaga(ctx, sqlc.GetSagaParams{TenantID: tenantID, ID: sagaID})
}

// History is the saga of an invoice with its transitions.
type History struct {
	Saga   sqlc.GetSagaRow
	Events []sqlc.ListSagaEventsRow
}

// Get returns the saga of an invoice with its events.
func Get(ctx context.Context, system *pgxpool.Pool, tenantID, invoiceID uuid.UUID) (*History, error) {
	q := sqlc.New(system)
	sagaID, err := q.GetSagaIDByInvoice(ctx, sqlc.GetSagaIDByInvoiceParams{TenantID: tenantID, InvoiceID: invoiceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "invoice has no saga (it is not finalized)")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	saga, err := Saga(ctx, system, tenantID, sagaID)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	events, err := q.ListSagaEvents(ctx, sqlc.ListSagaEventsParams{TenantID: tenantID, SagaID: sagaID})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &History{Saga: saga, Events: events}, nil
}
