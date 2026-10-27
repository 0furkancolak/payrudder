// Package query reads invoice sagas and their history for the operator. It never writes.
package query

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
)

// ErrNoSaga is the answer for an invoice that has no saga: it is not finalized.
var ErrNoSaga = errkey.New(codes.NotFound, "billing.saga.not_found", "invoice has no saga (it is not finalized)")

// Saga reads one saga. The error is the database's, unmapped.
func Saga(ctx context.Context, db *tenantdb.DB, tenantID, sagaID uuid.UUID) (sqlc.GetSagaRow, error) {
	return db.Q(tenantID).GetSaga(ctx, sqlc.GetSagaParams{TenantID: tenantID, ID: sagaID})
}

// History is the saga of an invoice with its transitions.
type History struct {
	Saga   sqlc.GetSagaRow
	Events []sqlc.ListSagaEventsRow
}

// Get returns the saga of an invoice with its events.
func Get(ctx context.Context, db *tenantdb.DB, tenantID, invoiceID uuid.UUID) (*History, error) {
	q := db.Q(tenantID)
	sagaID, err := q.GetSagaIDByInvoice(ctx, sqlc.GetSagaIDByInvoiceParams{TenantID: tenantID, InvoiceID: invoiceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoSaga
	}
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	saga, err := Saga(ctx, db, tenantID, sagaID)
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	events, err := q.ListSagaEvents(ctx, sqlc.ListSagaEventsParams{TenantID: tenantID, SagaID: sagaID})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return &History{Saga: saga, Events: events}, nil
}
