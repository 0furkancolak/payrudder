package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
)

// NoticeSweeper sends the notice mails of price changes that were held until N days before the new price (plan 15, notice_send_days_before). A held notice is the
// fact (PriceChangeScheduled) kept in its price_change_items row with the time it is due; the sweep writes it to the outbox when the time has come, in the same
// transaction that marks the item sent, so it is sent once, across restarts and on several instances (the second finds the item marked).
//
// It sends nothing while the operator's kill switch is on (the notices stay held, and go when it is released), and never for a change that is not waiting any more
// (withdrawn, or applied) or a price change that is not open. With cells on (ADR 0018) each tenant placed in a cell is listed in its own schema, as the trial
// reminder does.
type NoticeSweeper struct {
	system  *pgxpool.Pool
	app     pgxx.Beginner
	places  *placement.Places
	every   time.Duration
	batch   int
	now     func() time.Time
	log     *slog.Logger
	visited func(uuid.UUID) // tests: sees every tenant in a cell the sweep visits
}

// NewNoticeSweeper returns the sweep; every is how often it looks (default 10 minutes: a notice is days ahead, so the minute does not matter). appDB is the app
// pool, or the app-role router (placement.AppDB) when cells are on.
func NewNoticeSweeper(systemPool *pgxpool.Pool, appDB pgxx.Beginner, places *placement.Places, every time.Duration, log *slog.Logger) *NoticeSweeper {
	if every <= 0 {
		every = 10 * time.Minute
	}
	return &NoticeSweeper{system: systemPool, app: appDB, places: places, every: every, batch: 200, now: time.Now, log: log}
}

// WithClock sets the clock the sweep reads, and WithVisits makes it report each tenant in a cell it visits to fn (tests).
func (s *NoticeSweeper) WithClock(now func() time.Time) *NoticeSweeper { s.now = now; return s }

// WithVisits: see WithClock.
func (s *NoticeSweeper) WithVisits(fn func(uuid.UUID)) *NoticeSweeper { s.visited = fn; return s }

// Once sends the notices that are due at now, at most one batch per list, and says how many it sent and how many it looked at. A notice that fails is logged and
// left for the next pass. While the kill switch is on it does nothing.
func (s *NoticeSweeper) Once(ctx context.Context) (sent, looked int, err error) {
	p, err := sqlc.New(s.system).GetPriceChangePause(ctx)
	if err != nil || p.Paused {
		return 0, 0, err
	}
	now := s.now()
	due, err := sqlc.New(s.system).SystemListDueNotices(ctx, sqlc.SystemListDueNoticesParams{Now: now, Batch: int32(s.batch)}) //nolint:gosec // a small constant
	if err != nil {
		return 0, 0, err
	}
	sent, looked = s.sendAll(ctx, now, due)
	err = s.places.EachCell(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
		if s.visited != nil {
			s.visited(t.TenantID)
		}
		var due []sqlc.SystemListDueNoticesRow
		err := s.places.CellTx(ctx, t, func(tx pgx.Tx) (err error) {
			due, err = sqlc.New(tx).SystemListDueNotices(ctx, sqlc.SystemListDueNoticesParams{Now: now, Batch: int32(s.batch)}) //nolint:gosec // a small constant
			return err
		})
		if err != nil {
			return err
		}
		for _, d := range due {
			if d.TenantID != t.TenantID {
				return fmt.Errorf("price notice sweep: subscription %s of tenant %s is in the schema of tenant %s", d.SubscriptionID, d.TenantID, t.TenantID)
			}
		}
		n, l := s.sendAll(ctx, now, due)
		sent, looked = sent+n, looked+l
		return nil
	})
	return sent, looked, err
}

func (s *NoticeSweeper) sendAll(ctx context.Context, now time.Time, due []sqlc.SystemListDueNoticesRow) (sent, looked int) {
	for _, d := range due {
		ok, err := s.send(ctx, now, d)
		if err != nil {
			s.log.WarnContext(ctx, "price notice sweep: could not send", "tenant_id", d.TenantID, "subscription_id", d.SubscriptionID, "error", err)
			continue
		}
		if ok {
			sent++
		}
	}
	return sent, len(due)
}

// send writes one held notice to the outbox in its tenant's transaction, or does nothing when it was sent, withdrawn or applied since the list was read.
func (s *NoticeSweeper) send(ctx context.Context, now time.Time, d sqlc.SystemListDueNoticesRow) (bool, error) {
	done := false
	err := pgxx.InTenantTx(ctx, s.app, d.TenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		// The subscription is locked first, as the renewal and a withdrawal do, so a change cannot be applied or cancelled between the check and the mark.
		if _, err := subsqry.Lock(ctx, q, d.SubscriptionID); errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		payload, err := q.TakeDueNotice(ctx, sqlc.TakeDueNoticeParams{Now: now, PriceChangeID: d.PriceChangeID, SubscriptionID: d.SubscriptionID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var ev eventsv1.PriceChangeScheduled
		if err := proto.Unmarshal(payload, &ev); err != nil {
			return fmt.Errorf("held notice does not decode: %w", err)
		}
		if err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{Topic: PriceChangeTopic, PartitionKey: ev.GetTenantId() + "/" + ev.GetCustomerId(), Payload: payload}); err != nil {
			return err
		}
		done = true
		return q.NotifyOutbox(ctx)
	})
	return done && err == nil, err
}

// Run sweeps now and then every interval until ctx ends. A pass that fills a whole batch runs again at once.
func (s *NoticeSweeper) Run(ctx context.Context) error {
	t := time.NewTicker(s.every)
	defer t.Stop()
	for {
		for {
			n, looked, err := s.Once(ctx)
			if err != nil {
				s.log.ErrorContext(ctx, "price notice sweep: listing the notices failed", "error", err)
			} else if n > 0 {
				s.log.InfoContext(ctx, "held price change notices sent", "count", n)
			}
			// Another pass only when this one made progress through a full batch: a notice that keeps failing must not make it spin.
			if err != nil || n == 0 || looked < s.batch || ctx.Err() != nil {
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
