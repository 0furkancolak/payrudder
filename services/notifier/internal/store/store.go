// Package store is notifier's dedup record: sent_notifications and the limit consumer's Kafka offsets.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store/sqlc"
)

// Claim is the outcome of trying to take the right to send one email.
type Claim int

const (
	// Claimed: nobody had it; the caller sends.
	Claimed Claim = iota
	// Reclaimed: a previous attempt claimed it and never finished (older than the stale age); the caller sends again.
	// SMTP is not idempotent, so this is the documented at-least-once case.
	Reclaimed
	// AlreadySent: the email went out; the stored message id is returned.
	AlreadySent
	// InFlight: another delivery is sending right now.
	InFlight
)

// Notifications implements the SENDING/SENT protocol and workerx.Inbox.
type Notifications struct {
	DB *pgxpool.Pool
	// Stale is how old a SENDING row must be before another delivery re-sends (default 5 minutes).
	Stale time.Duration
}

func (n Notifications) stale() time.Duration {
	if n.Stale <= 0 {
		return 5 * time.Minute
	}
	return n.Stale
}

// Claim takes the right to send the email with the dedup key. The row is inserted before sending, so a crash between the
// SMTP acceptance and MarkSent leaves SENDING (re-sent after Stale) rather than a lost record.
func (n Notifications) Claim(ctx context.Context, key string, commandID *uuid.UUID, kind, template, recipient string) (Claim, string, error) {
	tx, err := n.DB.Begin(ctx)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New(tx)
	inserted, err := q.ClaimNotification(ctx, sqlc.ClaimNotificationParams{DedupKey: key, CommandID: commandID, Kind: kind, Template: template, Recipient: recipient})
	if err != nil {
		return 0, "", err
	}
	if inserted == 1 {
		return Claimed, "", tx.Commit(ctx)
	}
	row, err := q.LockNotification(ctx, key)
	if err != nil {
		return 0, "", err
	}
	switch {
	case row.Status == "SENT":
		return AlreadySent, deref(row.SmtpMessageID), tx.Commit(ctx)
	case time.Since(row.ClaimedAt) > n.stale():
		if err := q.RenewClaim(ctx, key); err != nil {
			return 0, "", err
		}
		return Reclaimed, "", tx.Commit(ctx)
	default:
		return InFlight, "", tx.Commit(ctx)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// MarkSent records the SMTP message id.
func (n Notifications) MarkSent(ctx context.Context, key, messageID string) error {
	return sqlc.New(n.DB).MarkNotificationSent(ctx, sqlc.MarkNotificationSentParams{DedupKey: key, SmtpMessageID: &messageID})
}

// Release deletes a claim whose email was certainly not sent, so the next delivery starts clean.
func (n Notifications) Release(ctx context.Context, key string) error {
	return sqlc.New(n.DB).ReleaseNotification(ctx, key)
}

// Lookup implements workerx.Inbox: a command whose email was sent gets its NotificationSent again.
func (n Notifications) Lookup(ctx context.Context, commandID uuid.UUID) (*commandsv1.ReplyEnvelope, bool, error) {
	msgID, err := sqlc.New(n.DB).GetSentMessageID(ctx, commandID.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	id := deref(msgID)
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_NotificationSent{NotificationSent: &commandsv1.NotificationSent{MessageId: id}}}, true, nil
}

// Store implements workerx.Inbox. The reply is derived from the SENT row, so there is nothing more to record.
func (n Notifications) Store(context.Context, uuid.UUID, *commandsv1.ReplyEnvelope) error { return nil }

// Offsets is the Kafka offset store of the limit alert consumer.
type Offsets struct {
	DB    *pgxpool.Pool
	Group string
	Topic string
}

// Load implements kafkax.OffsetLoader.
func (o Offsets) Load(ctx context.Context, _ string, partitions []int32) (map[int32]int64, error) {
	rows, err := sqlc.New(o.DB).LoadOffsets(ctx, sqlc.LoadOffsetsParams{ConsumerGroup: o.Group, Topic: o.Topic, Partitions: partitions})
	if err != nil {
		return nil, err
	}
	out := make(map[int32]int64, len(rows))
	for _, r := range rows {
		out[r.Partition] = r.NextOffset
	}
	return out, nil
}

// Save advances the offset of a partition (never backwards).
func (o Offsets) Save(ctx context.Context, partition int32, next int64) error {
	return sqlc.New(o.DB).SaveOffset(ctx, sqlc.SaveOffsetParams{ConsumerGroup: o.Group, Topic: o.Topic, Partition: partition, NextOffset: next})
}
