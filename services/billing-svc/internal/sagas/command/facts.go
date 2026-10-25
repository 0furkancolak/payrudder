package command

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
)

// Fact versions: consumers dedup on (invoice_id, version).
const (
	FactFinalized int64 = 1
	FactPaid      int64 = 2
	FactVoided    int64 = 3
)

// fact publishes the fact of version on the saga's invoice; set fills in what is particular to the version.
func (e *Engine) fact(ctx context.Context, tx pgx.Tx, s *saga, version int64, set func(*eventsv1.InvoiceFact)) error {
	f := &eventsv1.InvoiceFact{
		InvoiceId: s.InvoiceID.String(), TenantId: s.TenantID.String(), CustomerId: s.CustomerID.String(), PeriodId: s.PeriodID,
		Version: version, TotalMinor: s.TotalMinor, Currency: s.Currency, Number: s.Number, OccurredAt: timestamppb.New(e.cfg.Now()),
	}
	set(f)
	return writeFact(ctx, tx, s.TenantID, f)
}

// FinalizedFact is the fact the run publishes when it assigns an invoice's number.
func FinalizedFact(ctx context.Context, tx pgx.Tx, now time.Time, tenantID, invoiceID, customerID uuid.UUID, period, number, currency string, total int64) error {
	return writeFact(ctx, tx, tenantID, &eventsv1.InvoiceFact{
		InvoiceId: invoiceID.String(), TenantId: tenantID.String(), CustomerId: customerID.String(), PeriodId: period,
		Version: FactFinalized, TotalMinor: total, Currency: currency, Number: number, OccurredAt: timestamppb.New(now),
		Kind: &eventsv1.InvoiceFact_Finalized_{Finalized: &eventsv1.InvoiceFact_Finalized{}},
	})
}

// writeFact puts the fact into the outbox for the invoice topic; its message id is the invoice and version, so a fact is written once.
func writeFact(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, f *eventsv1.InvoiceFact) error {
	b, err := proto.Marshal(f)
	if err != nil {
		return err
	}
	return store.EnqueueOutbox(ctx, tx, tenantID, uuid.NewSHA1(factNS, fmt.Appendf(nil, "%s|%d", f.GetInvoiceId(), f.GetVersion())),
		InvoicesTopic, tenantID.String(), outbox.DestKafka, b)
}
