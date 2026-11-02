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
	return drive(ctx, db, eng, tenantID, invoiceID, action{ev: model.EvRetry, cause: "operator:retry", audit: "invoice.retry"})
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
	return drive(ctx, db, eng, tenantID, invoiceID, action{ev: model.EvVoid, cause: "operator:void", audit: "invoice.void", guard: guard, detail: detail})
}

// Release is the seller's Retry of an invoice whose first charge is held: the payment gate is asked again and the charge goes out when it lets it through, otherwise the
// invoice stays held. It never overrides the gate. Any other state is refused (billing.invoice.not_held): the operator's Retry is the one for the rest.
func Release(ctx context.Context, db *tenantdb.DB, eng *Engine, tenantID, invoiceID uuid.UUID) (sqlc.GetSagaRow, error) {
	return drive(ctx, db, eng, tenantID, invoiceID, action{ev: model.EvRetry, cause: "seller:release", audit: "invoice.release", guard: onlyHeld})
}

// VoidHeld is the seller's Void of an invoice whose first charge is held: nothing was charged, so nothing is refunded. Any other state is refused (billing.invoice.not_held):
// a paid invoice is voided by the operator, with its refund.
func VoidHeld(ctx context.Context, db *tenantdb.DB, eng *Engine, tenantID, invoiceID uuid.UUID) (sqlc.GetSagaRow, error) {
	return drive(ctx, db, eng, tenantID, invoiceID, action{ev: model.EvVoid, cause: "seller:void", audit: "invoice.void", guard: onlyHeld})
}

func onlyHeld(state model.State) error {
	if state != model.PaymentHeld {
		return errkey.Newf(codes.FailedPrecondition, "billing.invoice.not_held", "the first charge of this invoice is not held: its payment is in %s", state)
	}
	return nil
}

// action is what drive does to a saga: the event, the cause the saga log records (before the colon, who asked: operator or seller), the audit action, a check of the state the
// saga is in under the row lock (nil for none) and what is added to the audit entry's detail.
type action struct {
	ev     model.Event
	cause  string
	audit  string
	guard  func(model.State) error
	detail map[string]any
}

func sagaOf(ctx context.Context, tx pgx.Tx, tid, invoiceID uuid.UUID) (uuid.UUID, error) {
	id, err := sqlc.New(tx).GetSagaIDByInvoice(ctx, sqlc.GetSagaIDByInvoiceParams{TenantID: tid, InvoiceID: invoiceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, query.ErrNoSaga
	}
	return id, err
}

func drive(ctx context.Context, db *tenantdb.DB, eng *Engine, tid, invID uuid.UUID, a action) (sqlc.GetSagaRow, error) {
	var sagaID uuid.UUID
	var applied bool
	var state model.State
	err := db.InTx(ctx, tid, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		if sagaID, err = sagaOf(ctx, tx, tid, invID); err != nil {
			return err
		}
		if a.guard != nil {
			cur, err := sqlc.New(tx).LockSagaStateByInvoice(ctx, sqlc.LockSagaStateByInvoiceParams{TenantID: tid, InvoiceID: invID})
			if err != nil {
				return err
			}
			if err := a.guard(model.State(cur)); err != nil {
				return err
			}
		}
		out, err := eng.Handle(ctx, tx, tid, sagaID, a.ev, a.cause, nil)
		applied, state = out.Applied, out.From
		if err != nil || !out.Applied {
			return err
		}
		detail := map[string]any{"saga_id": sagaID.String(), "from": string(out.From), "to": string(out.To), "cause": a.cause}
		maps.Copy(detail, a.detail)
		by, _, _ := strings.Cut(a.cause, ":")
		return audit.Record(ctx, tx, audit.Event{TenantID: tid, Action: a.audit, TargetType: "invoice", TargetID: invID.String(),
			Summary: fmt.Sprintf("%s action %s on the invoice; its saga was in %s and is in %s", by, a.ev, out.From, out.To), Detail: detail})
	})
	if err != nil {
		return sqlc.GetSagaRow{}, apierr.Wrap(ctx, err)
	}
	if !applied {
		return sqlc.GetSagaRow{}, errkey.Newf(codes.FailedPrecondition, "billing.saga.event_not_possible", "%s is not possible while the invoice saga is in %s", a.ev, state)
	}
	row, err := query.Saga(ctx, db, tid, sagaID)
	if err != nil {
		return sqlc.GetSagaRow{}, apierr.Internal(ctx, err)
	}
	return row, nil
}
