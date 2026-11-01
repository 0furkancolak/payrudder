package support

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// closeBatch bounds the tickets one pass closes in one transaction.
const closeBatch = 100

// AutoClose closes, once an `every`, the tickets that have waited for the person for `after` (status pending since before then) and mails the person who opened
// each. It is safe on several instances at once: a ticket another instance is closing is skipped. It returns when ctx ends.
func (s *Service) AutoClose(ctx context.Context, after, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		for {
			n, err := s.CloseStale(ctx, after)
			if err != nil {
				s.log.ErrorContext(ctx, "support: closing the tickets nobody answered failed", "error", err)
			} else if n > 0 {
				s.log.InfoContext(ctx, "support tickets closed after no answer", "count", n)
			}
			// Another pass only after a full batch: there may be more.
			if err != nil || n < closeBatch || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// CloseStale closes one batch and says how many it closed.
func (s *Service) CloseStale(ctx context.Context, after time.Duration) (int, error) {
	var ids []uuid.UUID
	var mails []usermail.Mail
	err := pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (err error) {
		q := sqlc.New(tx)
		if ids, err = q.SupportCloseStale(ctx, sqlc.SupportCloseStaleParams{Before: s.now().Add(-after), Batch: closeBatch}); err != nil {
			return err
		}
		for _, id := range ids {
			if err = q.InsertSupportEvent(ctx, sqlc.InsertSupportEventParams{ID: idgen.NewUUIDv7(), TicketID: id, Kind: "status", ActorKind: "system", FromValue: "pending", ToValue: "closed"}); err != nil {
				return err
			}
			var m []usermail.Mail
			if m, err = s.openerMail(ctx, q, id, usermail.KindTicketStatus); err != nil {
				return err
			}
			mails = append(mails, m...)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	s.send(ctx, mails)
	return len(ids), nil
}
