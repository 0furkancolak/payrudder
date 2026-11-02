package contracts

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
)

// OnTenantEvent copies the result of a credit note's refund onto the withdrawal that asked for the note (docs/plans/21-contracts.md). billing-svc announces credit_note.refunded and
// credit_note.refund_failed through RecordTenantEvent; this is told in the same transaction (webhookendpoints EventHook), so the withdrawal never disagrees with an event the seller
// was sent. It needs nothing from billing but the event, and it is idempotent: a state moves only forward (requested to refunded or refund_failed, refund_failed to refunded after
// the note's refund was retried), so a repeated or reordered event changes nothing. An event it cannot read is logged and left alone: refusing it would make billing send it again for ever.
func OnTenantEvent(ctx context.Context, q *sqlc.Queries, typ string, _ uuid.UUID, data json.RawMessage, at time.Time) error {
	var state string
	var from []string
	switch typ {
	case webhooksmodel.CreditNoteRefunded:
		state, from = "refunded", []string{"requested", "refund_failed"}
	case webhooksmodel.CreditNoteRefundFailed:
		state, from = "refund_failed", []string{"requested"}
	default:
		return nil
	}
	var d struct {
		CreditNoteID string `json:"credit_note_id"`
		RefundError  string `json:"refund_error"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		slog.WarnContext(ctx, "contracts: a credit note event with unreadable data is ignored", "type", typ, "error", err)
		return nil
	}
	id, err := uuid.Parse(d.CreditNoteID)
	if err != nil {
		slog.WarnContext(ctx, "contracts: a credit note event without a credit note id is ignored", "type", typ)
		return nil
	}
	note := ""
	if state == "refund_failed" {
		note = truncate(d.RefundError, 1000)
	}
	_, err = q.SettleContractWithdrawalRefund(ctx, sqlc.SettleContractWithdrawalRefundParams{State: state, Note: note, Now: &at, CreditNoteID: &id, FromStates: from})
	return err
}
