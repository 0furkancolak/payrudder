package command

import (
	"context"
	"math/big"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	einvoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/einvoices/command"
	einvoicemodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/einvoices/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// invoiceEInvoice sends the e-invoice of an invoice when the tenant's setting asks for it at this trigger ("finalized" or "paid").
func (e *Engine) invoiceEInvoice(ctx context.Context, tx pgx.Tx, s *saga, trigger string) error {
	if s.Snap.EInvoiceOn != trigger || s.Snap.Seller == nil {
		return nil
	}
	lines, err := e.snapshotLines(ctx, tx, s)
	if err != nil {
		return err
	}
	snap := e.invoiceSnapshot(s, lines)
	_, err = einvoicecmd.Enqueue(ctx, tx, e.cfg.Now(), s.TenantID, s.Snap.Environment, einvoicemodel.KindInvoice, s.InvoiceID, nil, s.Number,
		&commandsv1.IssueEInvoice{Document: snap})
	return err
}

// EInvoiceOnFinalize is called by the run after the saga started: with e_invoice_on = finalized the invoice becomes an e-invoice now.
func (e *Engine) EInvoiceOnFinalize(ctx context.Context, tx pgx.Tx, tenantID, sagaID uuid.UUID) error {
	s, err := e.load(ctx, tx, tenantID, sagaID)
	if err != nil {
		return err
	}
	return e.invoiceEInvoice(ctx, tx, s, "finalized")
}

// CreditNoteSnapshot is what a credit note's e-invoice carries: the invoice's snapshot with the credit note's number and id, the invoice's lines scaled to
// the credited amount (without the credit balance the invoice applied), and the original's number and date. eInvoiceOn is the setting frozen on the invoice.
func (e *Engine) CreditNoteSnapshot(ctx context.Context, tx pgx.Tx, tenantID, invoiceID, creditNoteID uuid.UUID, number string, credited int64, at time.Time) (snap *commandsv1.IssueEInvoice, eInvoiceOn string, err error) {
	sagaID, err := sqlc.New(tx).GetSagaIDByInvoice(ctx, sqlc.GetSagaIDByInvoiceParams{TenantID: tenantID, InvoiceID: invoiceID})
	if err != nil {
		return nil, "", err
	}
	s, err := e.load(ctx, tx, tenantID, sagaID)
	if err != nil {
		return nil, "", err
	}
	lines, err := e.snapshotLines(ctx, tx, s)
	if err != nil {
		return nil, "", err
	}
	inv := e.invoiceSnapshot(s, lines)
	cn := proto.Clone(inv).(*commandsv1.InvoiceSnapshot)
	cn.InvoiceId, cn.Number, cn.Version, cn.IssuedAt = creditNoteID.String(), number, 1, timestamppb.New(at)
	cn.Lines = ProrateLines(inv.GetLines(), credited)
	var sub int64
	for _, l := range cn.Lines {
		if l.GetKind() != "TAX" {
			sub += l.GetAmountMinor()
		}
	}
	cn.SubtotalMinor, cn.TotalMinor = sub, credited
	return &commandsv1.IssueEInvoice{
		Document: cn, OriginalDocumentId: einvoicecmd.DocumentID(tenantID, einvoicemodel.KindInvoice, invoiceID).String(),
		OriginalNumber: inv.GetNumber(), OriginalIssuedAt: inv.GetIssuedAt(),
	}, s.Snap.EInvoiceOn, nil
}

// ProrateLines scales the invoice lines (all but the credit balance) so that they add up to target, by largest remainder: every line keeps its sign and the
// sum is exact. Line numbers are renumbered from 1. A zero base returns nothing.
func ProrateLines(lines []*commandsv1.InvoiceLineSnapshot, target int64) []*commandsv1.InvoiceLineSnapshot {
	var keep []*commandsv1.InvoiceLineSnapshot
	var base int64
	for _, l := range lines {
		if l.GetKind() == "CREDIT" {
			continue
		}
		keep = append(keep, l)
		base += l.GetAmountMinor()
	}
	if base == 0 || len(keep) == 0 {
		return nil
	}
	type part struct {
		rem *big.Int
		cut int64
	}
	parts := make([]part, len(keep))
	var assigned int64
	for i, l := range keep {
		num := new(big.Int).Mul(big.NewInt(l.GetAmountMinor()), big.NewInt(target))
		q, r := new(big.Int).QuoRem(num, big.NewInt(base), new(big.Int))
		parts[i] = part{rem: r, cut: q.Int64()}
		assigned += q.Int64()
	}
	// QuoRem truncates toward zero; the leftover goes, one minor unit each, to the lines with the largest remainder in the direction of the shortfall.
	left := target - assigned
	order := make([]int, len(parts))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ra, rb := new(big.Int).Abs(parts[order[a]].rem), new(big.Int).Abs(parts[order[b]].rem)
		return ra.Cmp(rb) > 0
	})
	step := int64(1)
	if left < 0 {
		step, left = -1, -left
	}
	for i := 0; left > 0 && len(order) > 0; i, left = (i+1)%len(order), left-1 {
		parts[order[i]].cut += step
	}
	out := make([]*commandsv1.InvoiceLineSnapshot, 0, len(keep))
	for i, l := range keep {
		if parts[i].cut == 0 && l.GetAmountMinor() != 0 {
			continue
		}
		c := proto.Clone(l).(*commandsv1.InvoiceLineSnapshot)
		c.LineNo, c.AmountMinor, c.Quantity, c.UnitLabel = int32(len(out)+1), parts[i].cut, "", "" //nolint:gosec // small
		out = append(out, c)
	}
	return out
}
