package command

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// NoticeTopic is the Kafka topic of the CustomerBlockNotice fact (docs/04-messaging.md), written to the outbox and relayed like subscription.trial-will-end.v1.
const NoticeTopic = "customer.block-notice.v1"

// queueNotice writes the fact the notifier turns into the customer's mail "you were blocked" or "the block is lifted", in the transaction of the block or the lift. It is
// written whether or not the app has switched the mail on: the notifier asks the app's mail switch (category customer_notices, off until switched on), so a seller who
// switches it on later is not sent the notices of blocks that went before (the offset is past them). The fact carries what the mail says and none of the seller's
// reasons: not the category, not the note.
func queueNotice(ctx context.Context, q *sqlc.Queries, tenantID, customerID uuid.UUID, kind string, serviceSuspended bool, until *time.Time, at time.Time) error {
	ev := &eventsv1.CustomerBlockNotice{
		EventId:  idgen.Deterministic("customer_block_notice", tenantID.String(), customerID.String(), kind, at.UTC().Format(time.RFC3339Nano)).String(),
		TenantId: tenantID.String(), CustomerId: customerID.String(), Kind: kind, ServiceSuspended: serviceSuspended, OccurredAt: timestamppb.New(at),
	}
	if until != nil {
		ev.BlockedUntil = timestamppb.New(*until)
	}
	payload, err := proto.Marshal(ev)
	if err != nil {
		return err
	}
	if err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{Topic: NoticeTopic, PartitionKey: tenantID.String() + "/" + customerID.String(), Payload: payload}); err != nil {
		return err
	}
	return q.NotifyOutbox(ctx)
}
