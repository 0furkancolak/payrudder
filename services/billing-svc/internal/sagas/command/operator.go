package command

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/query"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
)

// Retry retries a stuck saga of an invoice.
func Retry(ctx context.Context, db *tenantdb.DB, eng *Engine, tenantID, invoiceID uuid.UUID) (sqlc.GetSagaRow, error) {
	return drive(ctx, db, eng, tenantID, invoiceID, model.EvRetry, "operator:retry")
}

// Void voids an invoice through its saga.
func Void(ctx context.Context, db *tenantdb.DB, eng *Engine, tenantID, invoiceID uuid.UUID) (sqlc.GetSagaRow, error) {
	return drive(ctx, db, eng, tenantID, invoiceID, model.EvVoid, "operator:void")
}

func sagaOf(ctx context.Context, tx pgx.Tx, tid, invoiceID uuid.UUID) (uuid.UUID, error) {
	id, err := sqlc.New(tx).GetSagaIDByInvoice(ctx, sqlc.GetSagaIDByInvoiceParams{TenantID: tid, InvoiceID: invoiceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, query.ErrNoSaga
	}
	return id, err
}

func drive(ctx context.Context, db *tenantdb.DB, eng *Engine, tid, invID uuid.UUID, ev model.Event, cause string) (sqlc.GetSagaRow, error) {
	var sagaID uuid.UUID
	var applied bool
	var state model.State
	err := db.InTx(ctx, tid, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		if sagaID, err = sagaOf(ctx, tx, tid, invID); err != nil {
			return err
		}
		out, err := eng.Handle(ctx, tx, tid, sagaID, ev, cause, nil)
		applied, state = out.Applied, out.From
		return err
	})
	if err != nil {
		return sqlc.GetSagaRow{}, apierr.Wrap(ctx, err)
	}
	if !applied {
		return sqlc.GetSagaRow{}, errkey.Newf(codes.FailedPrecondition, "billing.saga.event_not_possible", "%s is not possible while the invoice saga is in %s", ev, state)
	}
	row, err := query.Saga(ctx, db, tid, sagaID)
	if err != nil {
		return sqlc.GetSagaRow{}, apierr.Internal(ctx, err)
	}
	return row, nil
}
