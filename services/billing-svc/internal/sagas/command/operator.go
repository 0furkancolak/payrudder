package command

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/query"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
)

// Retry retries a stuck saga of an invoice.
func Retry(ctx context.Context, db *tenantdb.DB, eng *Engine, tenantID, invoiceID uuid.UUID) (sqlc.GetSagaRow, error) {
	return drive(ctx, db, eng, tenantID, invoiceID, model.EvRetry, "operator:retry", nil, nil)
}

// maxVoidReason bounds the reason an operator gives for a void in the audit log.
const maxVoidReason = 500

// Void voids an invoice through its saga. An invoice whose payment is unknown (PAYMENT_UNKNOWN: the provider may have taken the money and not said so) is voided only
// when the operator confirms that no charge was made (confirmNotCharged, after checking at the provider); anything else is refused with
// billing.saga.void_needs_confirmation and the saga is left as it was. The confirmation and the reason are recorded in the audit log.
func Void(ctx context.Context, db *tenantdb.DB, eng *Engine, tenantID, invoiceID uuid.UUID, confirmNotCharged bool, reason string) (sqlc.GetSagaRow, error) {
	detail := map[string]any{}
	if confirmNotCharged {
		detail["confirmed_not_charged"] = true
	}
	if r := strings.TrimSpace(reason); r != "" {
		detail["reason"] = string([]rune(r)[:min(len([]rune(r)), maxVoidReason)])
	}
	guard := func(state model.State) error {
		if state == model.PaymentUnknown && !confirmNotCharged {
			return errkey.New(codes.FailedPrecondition, "billing.saga.void_needs_confirmation",
				"the payment of this invoice is unknown: the provider may have charged it. Check at the provider that nothing was charged, then void it with confirm_not_charged")
		}
		return nil
	}
	return drive(ctx, db, eng, tenantID, invoiceID, model.EvVoid, "operator:void", guard, detail)
}

func sagaOf(ctx context.Context, tx pgx.Tx, tid, invoiceID uuid.UUID) (uuid.UUID, error) {
	id, err := sqlc.New(tx).GetSagaIDByInvoice(ctx, sqlc.GetSagaIDByInvoiceParams{TenantID: tid, InvoiceID: invoiceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, query.ErrNoSaga
	}
	return id, err
}

func drive(ctx context.Context, db *tenantdb.DB, eng *Engine, tid, invID uuid.UUID, ev model.Event, cause string, guard func(model.State) error, extra map[string]any) (sqlc.GetSagaRow, error) {
	var sagaID uuid.UUID
	var applied bool
	var state model.State
	err := db.InTx(ctx, tid, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		if sagaID, err = sagaOf(ctx, tx, tid, invID); err != nil {
			return err
		}
		if guard != nil {
			cur, err := sqlc.New(tx).LockSagaStateByInvoice(ctx, sqlc.LockSagaStateByInvoiceParams{TenantID: tid, InvoiceID: invID})
			if err != nil {
				return err
			}
			if err := guard(model.State(cur)); err != nil {
				return err
			}
		}
		out, err := eng.Handle(ctx, tx, tid, sagaID, ev, cause, nil)
		applied, state = out.Applied, out.From
		if err != nil || !out.Applied {
			return err
		}
		detail := map[string]any{"saga_id": sagaID.String(), "from": string(out.From), "cause": cause}
		maps.Copy(detail, extra)
		return audit.Record(ctx, tx, audit.Event{TenantID: tid, Action: "invoice." + strings.ToLower(string(ev)), TargetType: "invoice", TargetID: invID.String(),
			Summary: fmt.Sprintf("operator action %s on the invoice; its saga was in %s", ev, out.From), Detail: detail})
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
