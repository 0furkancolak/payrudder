// Package store is notifier's dedup record: sent_notifications, the suppression list and the Kafka offsets of the consumers.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/strx"
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
// The tenant is recorded so that a bounce, which names only the provider's message id, can be traced back to the tenant it was sent for (uuid.Nil: none).
func (n Notifications) Claim(ctx context.Context, key string, commandID *uuid.UUID, tenantID uuid.UUID, kind, template, recipient string) (Claim, string, error) {
	tx, err := n.DB.Begin(ctx)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New(tx)
	var tid *uuid.UUID
	if tenantID != uuid.Nil {
		tid = &tenantID
	}
	inserted, err := q.ClaimNotification(ctx, sqlc.ClaimNotificationParams{DedupKey: key, CommandID: commandID, Kind: kind, Template: template, Recipient: recipient, TenantID: tid})
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
		return AlreadySent, strx.Deref(row.SmtpMessageID), tx.Commit(ctx)
	case time.Since(row.ClaimedAt) > n.stale():
		if err := q.RenewClaim(ctx, key); err != nil {
			return 0, "", err
		}
		return Reclaimed, "", tx.Commit(ctx)
	default:
		return InFlight, "", tx.Commit(ctx)
	}
}

// MarkSent records the SMTP message id.
func (n Notifications) MarkSent(ctx context.Context, key, messageID string) error {
	return sqlc.New(n.DB).MarkNotificationSent(ctx, sqlc.MarkNotificationSentParams{DedupKey: key, SmtpMessageID: &messageID})
}

// Release deletes a claim whose email was certainly not sent, so the next delivery starts clean.
func (n Notifications) Release(ctx context.Context, key string) error {
	return sqlc.New(n.DB).ReleaseNotification(ctx, key)
}

// ByMessageID finds the sent mail a provider's message id belongs to: the tenant it was sent for (uuid.Nil when it had none) and the address it went to.
func (n Notifications) ByMessageID(ctx context.Context, messageID string) (tenantID uuid.UUID, recipient string, found bool, err error) {
	row, err := sqlc.New(n.DB).NotificationByMessageID(ctx, &messageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", false, nil
	}
	if err != nil {
		return uuid.Nil, "", false, err
	}
	if row.TenantID != nil {
		tenantID = *row.TenantID
	}
	return tenantID, row.Recipient, true, nil
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
	id := strx.Deref(msgID)
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

func (o Offsets) pg() kafkax.PGOffsets {
	return kafkax.PGOffsets{DB: o.DB, Group: o.Group, Topic: o.Topic, Table: "kafka_offsets"}
}

// Load implements kafkax.OffsetLoader.
func (o Offsets) Load(ctx context.Context, _ string, partitions []int32) (map[int32]int64, error) {
	return o.pg().Load(ctx, o.Topic, partitions)
}

// Save advances the offset of a partition (never backwards).
func (o Offsets) Save(ctx context.Context, partition int32, next int64) error {
	return o.pg().Save(ctx, partition, next)
}

// Message is one line of a tenant's message log: a mail the notifier claimed for the tenant, and what became of it.
type Message struct {
	Key       string `json:"key"`
	Kind      string `json:"kind"`
	Template  string `json:"template"`
	Recipient string `json:"recipient"`
	// State is sending (claimed, not sent yet), sent, or what the provider reported of it: delivered, delayed, bounced (for good), soft_bounced or complained.
	State     string     `json:"state"`
	ClaimTime time.Time  `json:"claim_time"`
	SendTime  *time.Time `json:"send_time"`
}

// MessagesAfter is the position after the last message of a page: the claim time and key of that message (zero: the first page).
type MessagesAfter struct {
	At  time.Time
	Key string
}

// ListForTenant returns up to pageSize of the tenant's mail, newest first, after the given position.
func (n Notifications) ListForTenant(ctx context.Context, tenantID uuid.UUID, after MessagesAfter, pageSize int32) ([]Message, error) {
	rows, err := sqlc.New(n.DB).ListTenantMessages(ctx, sqlc.ListTenantMessagesParams{TenantID: &tenantID, AfterKey: after.Key, AfterAt: after.At, PageSize: pageSize})
	if err != nil {
		return nil, err
	}
	out := make([]Message, len(rows))
	for i, r := range rows {
		state := "sent"
		switch {
		case r.Status != "SENT":
			state = "sending"
		case r.DeliveryState != "":
			state = r.DeliveryState
		}
		out[i] = Message{Key: r.DedupKey, Kind: r.Kind, Template: r.Template, Recipient: r.Recipient, State: state, ClaimTime: r.ClaimedAt, SendTime: r.SentAt}
	}
	return out, nil
}

// RecordDelivery keeps what the provider reported of a message, by its message id, for the message log. A permanent bounce or a complaint is not overwritten.
func (n Notifications) RecordDelivery(ctx context.Context, messageID, state string) error {
	_, err := sqlc.New(n.DB).RecordDelivery(ctx, sqlc.RecordDeliveryParams{MessageID: &messageID, DeliveryState: state})
	return err
}
