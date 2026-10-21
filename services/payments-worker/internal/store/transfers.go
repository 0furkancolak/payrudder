package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/store/sqlc"
)

// Transfers is the bank transfer side of the ledger: the pending row a charge leaves, the receipts an operator confirms, and the row that closes a transfer.
type Transfers struct{ DB *pgxpool.Pool }

// ChargeAttempt is the latest charge attempt of an invoice: what was asked of the provider.
type ChargeAttempt struct {
	IdempotencyKey string
	Provider       string
	Environment    string
	AmountMinor    int64
	Currency       string
	InvoiceNumber  string
	CreatedAt      time.Time
}

// LatestCharge returns the invoice's most recent charge attempt.
func (t Transfers) LatestCharge(ctx context.Context, tenant, invoice uuid.UUID) (ChargeAttempt, bool, error) {
	r, ok, err := found(sqlc.New(t.DB).GetLatestChargeAttempt(ctx, sqlc.GetLatestChargeAttemptParams{TenantID: tenant, InvoiceID: invoice}))
	if !ok {
		return ChargeAttempt{}, false, err
	}
	return ChargeAttempt{IdempotencyKey: r.IdempotencyKey, Provider: r.Provider, Environment: r.Environment, AmountMinor: r.AmountMinor, Currency: r.Currency,
		InvoiceNumber: r.InvoiceNumber, CreatedAt: r.CreatedAt}, true, nil
}

// Pending is the pending ledger row of a charge: when the customer was told to pay, and the reference they were given.
type Pending struct {
	Reference string
	Since     time.Time
}

// Pending returns the pending row a charge left, if it left one.
func (t Transfers) Pending(ctx context.Context, provider, key string) (Pending, bool, error) {
	r, ok, err := found(sqlc.New(t.DB).GetPendingTransfer(ctx, sqlc.GetPendingTransferParams{Provider: provider, IdempotencyKey: &key}))
	if !ok {
		return Pending{}, false, err
	}
	return Pending{Reference: r.Reference, Since: r.OccurredAt}, true, nil
}

// Closure is the row that gave up on a charge: rejected by the operator or expired. Detail is "code: reason".
type Closure struct {
	Detail string
	At     time.Time
}

// Closure returns the declined row of a charge, if there is one.
func (t Transfers) Closure(ctx context.Context, provider, key string) (Closure, bool, error) {
	r, ok, err := found(sqlc.New(t.DB).GetTransferClosure(ctx, sqlc.GetTransferClosureParams{Provider: provider, IdempotencyKey: &key}))
	if !ok {
		return Closure{}, false, err
	}
	return Closure{Detail: r.Detail, At: r.OccurredAt}, true, nil
}

// Close writes the declined row of a charge. It reports false when the charge had been closed before (nothing is written twice).
func (t Transfers) Close(ctx context.Context, e ClosureEntry) (bool, error) {
	key := e.IdempotencyKey
	n, err := sqlc.New(t.DB).CloseTransfer(ctx, sqlc.CloseTransferParams{TenantID: e.TenantID, InvoiceID: &e.InvoiceID, Provider: e.Provider, Environment: e.Environment,
		AmountMinor: e.AmountMinor, Currency: e.Currency, ProviderRef: &key, IdempotencyKey: &key, Source: e.Source, Detail: &e.Detail})
	return n == 1, err
}

// ClosureEntry describes the row that closes a charge.
type ClosureEntry struct {
	TenantID       uuid.UUID
	InvoiceID      uuid.UUID
	Provider       string
	Environment    string
	AmountMinor    int64
	Currency       string
	IdempotencyKey string
	Source         string
	Detail         string
}

// Received is the sum of the confirmed receipts of an invoice and the reference of the latest one.
type Received struct {
	Minor   int64
	LastRef string
}

// Received sums the manual receipts of an invoice.
func (t Transfers) Received(ctx context.Context, tenant, invoice uuid.UUID, provider string) (Received, error) {
	r, err := sqlc.New(t.DB).SumManualReceipts(ctx, sqlc.SumManualReceiptsParams{TenantID: tenant, InvoiceID: &invoice, Provider: provider})
	return Received{Minor: r.Received, LastRef: r.LastRef}, err
}

// Receipt is one confirmed transfer.
type Receipt struct {
	ID            int64     `json:"id"`
	AmountMinor   int64     `json:"amount_minor"`
	Currency      string    `json:"currency"`
	ReceivedAt    time.Time `json:"received_at"`
	BankReference string    `json:"bank_reference"`
	Note          string    `json:"note,omitempty"`
	RecordedBy    string    `json:"recorded_by"`
	RecordedAt    time.Time `json:"recorded_at"`
}

// Receipts lists the confirmed transfers of an invoice, oldest first.
func (t Transfers) Receipts(ctx context.Context, tenant, invoice uuid.UUID) ([]Receipt, error) {
	rows, err := sqlc.New(t.DB).ListManualReceipts(ctx, sqlc.ListManualReceiptsParams{TenantID: tenant, InvoiceID: invoice})
	if err != nil {
		return nil, err
	}
	out := make([]Receipt, len(rows))
	for i, r := range rows {
		out[i] = Receipt{ID: r.LedgerID, AmountMinor: r.AmountMinor, Currency: r.Currency, ReceivedAt: r.ReceivedAt.UTC(), BankReference: r.BankReference, Note: r.Note,
			RecordedBy: r.RecordedBy, RecordedAt: r.RecordedAt.UTC()}
	}
	return out, nil
}

// NewReceipt is a transfer to record.
type NewReceipt struct {
	TenantID      uuid.UUID
	InvoiceID     uuid.UUID
	Provider      string
	Environment   string
	AmountMinor   int64
	Currency      string
	BankReference string
	ReceivedAt    time.Time
	Note          string
	RecordedBy    string
}

// ErrReceiptAmount: the bank reference was recorded before with another amount.
var ErrReceiptAmount = errors.New("store: that bank reference was recorded with another amount")

// ErrOverpayment: the receipt would take the invoice past its total. Outstanding is what was left.
type ErrOverpayment struct{ Outstanding int64 }

func (e ErrOverpayment) Error() string { return "the receipt is more than is outstanding" }

// CheckReceipt applies the rules for a transfer of amount to an invoice of total that has received so far: partial amounts are fine, and the invoice is paid when
// they add up to the total; an amount that would take the sum past the total is ErrOverpayment (carrying what is outstanding); a non-positive amount is refused.
func CheckReceipt(received, amount, total int64) (paid bool, err error) {
	switch {
	case amount <= 0:
		return false, errors.New("store: the amount must be positive")
	case received+amount > total:
		return false, ErrOverpayment{Outstanding: max(total-received, 0)}
	}
	return received+amount == total, nil
}

// RecordResult is the outcome of Record.
type RecordResult struct {
	Receipt   Receipt
	Duplicate bool
	// Received is the sum of the receipts including this one.
	Received int64
}

// Record writes a receipt: the ledger row and the receipt row, in one transaction under a lock on the invoice, so two operators confirming at once cannot
// together overpay. total is what the invoice's charge asked for. A repeat of the same (invoice, bank reference) with the same amount is Duplicate; with
// another amount it is ErrReceiptAmount; an amount past the total is ErrOverpayment.
func (t Transfers) Record(ctx context.Context, n NewReceipt, total int64) (RecordResult, error) {
	var res RecordResult
	err := pgxx.InTx(ctx, t.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := q.LockInvoiceReceipts(ctx, n.InvoiceID.String()); err != nil {
			return err
		}
		sum, err := q.SumManualReceipts(ctx, sqlc.SumManualReceiptsParams{TenantID: n.TenantID, InvoiceID: &n.InvoiceID, Provider: n.Provider})
		if err != nil {
			return err
		}
		existing, err := q.ListManualReceipts(ctx, sqlc.ListManualReceiptsParams{TenantID: n.TenantID, InvoiceID: n.InvoiceID})
		if err != nil {
			return err
		}
		for _, r := range existing {
			if r.BankReference != n.BankReference {
				continue
			}
			if r.AmountMinor != n.AmountMinor {
				return ErrReceiptAmount
			}
			res = RecordResult{Duplicate: true, Received: sum.Received, Receipt: Receipt{ID: r.LedgerID, AmountMinor: r.AmountMinor, Currency: r.Currency, ReceivedAt: r.ReceivedAt.UTC(),
				BankReference: r.BankReference, Note: r.Note, RecordedBy: r.RecordedBy, RecordedAt: r.RecordedAt.UTC()}}
			return nil
		}
		if _, err := CheckReceipt(sum.Received, n.AmountMinor, total); err != nil {
			return err
		}
		ref := n.InvoiceID.String() + ":" + n.BankReference
		detail := "bank transfer " + n.BankReference
		id, err := q.AppendManualLedger(ctx, sqlc.AppendManualLedgerParams{TenantID: n.TenantID, InvoiceID: &n.InvoiceID, Provider: n.Provider, Environment: n.Environment,
			AmountMinor: n.AmountMinor, Currency: n.Currency, ProviderRef: &ref, Detail: &detail, OccurredAt: n.ReceivedAt})
		if err != nil {
			return err
		}
		note := n.Note
		if err := q.InsertManualReceipt(ctx, sqlc.InsertManualReceiptParams{TenantID: n.TenantID, InvoiceID: n.InvoiceID, LedgerID: id, BankReference: n.BankReference,
			AmountMinor: n.AmountMinor, Currency: n.Currency, ReceivedAt: n.ReceivedAt, Note: &note, RecordedBy: n.RecordedBy}); err != nil {
			return err
		}
		res = RecordResult{Received: sum.Received + n.AmountMinor, Receipt: Receipt{ID: id, AmountMinor: n.AmountMinor, Currency: n.Currency, ReceivedAt: n.ReceivedAt,
			BankReference: n.BankReference, Note: n.Note, RecordedBy: n.RecordedBy, RecordedAt: time.Now().UTC()}}
		return nil
	})
	return res, err
}
