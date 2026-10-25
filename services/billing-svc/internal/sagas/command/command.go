// Package command pushes an invoice saga through an operator event (retry, void). The saga engine holds the transition rules.
package command

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/saga"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/query"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Retry retries a stuck saga of an invoice.
func Retry(ctx context.Context, system *pgxpool.Pool, eng *saga.Engine, tenantID, invoiceID uuid.UUID) (sqlc.GetSagaRow, error) {
	return drive(ctx, system, eng, tenantID, invoiceID, saga.EvRetry, "operator:retry")
}

// Void voids an invoice through its saga.
func Void(ctx context.Context, system *pgxpool.Pool, eng *saga.Engine, tenantID, invoiceID uuid.UUID) (sqlc.GetSagaRow, error) {
	return drive(ctx, system, eng, tenantID, invoiceID, saga.EvVoid, "operator:void")
}

func sagaOf(ctx context.Context, tx pgx.Tx, tid, invoiceID uuid.UUID) (uuid.UUID, error) {
	id, err := sqlc.New(tx).GetSagaIDByInvoice(ctx, sqlc.GetSagaIDByInvoiceParams{TenantID: tid, InvoiceID: invoiceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errkey.New(codes.NotFound, "billing.saga.not_found", "invoice has no saga (it is not finalized)")
	}
	return id, err
}

func drive(ctx context.Context, system *pgxpool.Pool, eng *saga.Engine, tid, invID uuid.UUID, ev saga.Event, cause string) (sqlc.GetSagaRow, error) {
	var sagaID uuid.UUID
	var applied bool
	var state saga.State
	err := pgxx.InTx(ctx, system, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		if sagaID, err = sagaOf(ctx, tx, tid, invID); err != nil {
			return err
		}
		out, err := eng.Handle(ctx, tx, tid, sagaID, ev, cause, nil)
		applied, state = out.Applied, out.From
		return err
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return sqlc.GetSagaRow{}, err
		}
		return sqlc.GetSagaRow{}, status.Error(codes.Internal, "internal error")
	}
	if !applied {
		return sqlc.GetSagaRow{}, errkey.Newf(codes.FailedPrecondition, "billing.saga.event_not_possible", "%s is not possible while the invoice saga is in %s", ev, state)
	}
	return query.Saga(ctx, system, tid, sagaID)
}
