// Package store is notifier's dedup record: sent_notifications and the limit consumer's Kafka offsets.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
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
	tag, err := tx.Exec(ctx, `INSERT INTO sent_notifications (dedup_key, command_id, kind, template, recipient, status)
		VALUES ($1,$2,$3,$4,$5,'SENDING') ON CONFLICT (dedup_key) DO NOTHING`, key, commandID, kind, template, recipient)
	if err != nil {
		return 0, "", err
	}
	if tag.RowsAffected() == 1 {
		return Claimed, "", tx.Commit(ctx)
	}
	var status string
	var claimed time.Time
	var msgID *string
	if err := tx.QueryRow(ctx, `SELECT status, claimed_at, smtp_message_id FROM sent_notifications WHERE dedup_key = $1 FOR UPDATE`, key).Scan(&status, &claimed, &msgID); err != nil {
		return 0, "", err
	}
	id := ""
	if msgID != nil {
		id = *msgID
	}
	switch {
	case status == "SENT":
		return AlreadySent, id, tx.Commit(ctx)
	case time.Since(claimed) > n.stale():
		if _, err := tx.Exec(ctx, `UPDATE sent_notifications SET claimed_at = now() WHERE dedup_key = $1`, key); err != nil {
			return 0, "", err
		}
		return Reclaimed, "", tx.Commit(ctx)
	default:
		return InFlight, "", tx.Commit(ctx)
	}
}

// MarkSent records the SMTP message id.
func (n Notifications) MarkSent(ctx context.Context, key, messageID string) error {
	_, err := n.DB.Exec(ctx, `UPDATE sent_notifications SET status = 'SENT', sent_at = now(), smtp_message_id = $2 WHERE dedup_key = $1`, key, messageID)
	return err
}

// Release deletes a claim whose email was certainly not sent, so the next delivery starts clean.
func (n Notifications) Release(ctx context.Context, key string) error {
	_, err := n.DB.Exec(ctx, `DELETE FROM sent_notifications WHERE dedup_key = $1 AND status = 'SENDING'`, key)
	return err
}

// Lookup implements workerx.Inbox: a command whose email was sent gets its NotificationSent again.
func (n Notifications) Lookup(ctx context.Context, commandID uuid.UUID) (*commandsv1.ReplyEnvelope, bool, error) {
	var msgID *string
	err := n.DB.QueryRow(ctx, `SELECT smtp_message_id FROM sent_notifications WHERE dedup_key = $1 AND status = 'SENT'`, commandID.String()).Scan(&msgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	id := ""
	if msgID != nil {
		id = *msgID
	}
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
	rows, err := o.DB.Query(ctx, `SELECT partition, next_offset FROM kafka_offsets WHERE consumer_group = $1 AND topic = $2 AND partition = ANY($3)`, o.Group, o.Topic, partitions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int32]int64{}
	for rows.Next() {
		var p int32
		var next int64
		if err := rows.Scan(&p, &next); err != nil {
			return nil, err
		}
		out[p] = next
	}
	return out, rows.Err()
}

// Save advances the offset of a partition (never backwards).
func (o Offsets) Save(ctx context.Context, partition int32, next int64) error {
	_, err := o.DB.Exec(ctx, `INSERT INTO kafka_offsets (consumer_group, topic, partition, next_offset) VALUES ($1,$2,$3,$4)
		ON CONFLICT (consumer_group, topic, partition) DO UPDATE SET next_offset = GREATEST(kafka_offsets.next_offset, EXCLUDED.next_offset), updated_at = now()`,
		o.Group, o.Topic, partition, next)
	return err
}
