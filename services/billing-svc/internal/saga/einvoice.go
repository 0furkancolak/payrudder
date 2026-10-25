package saga

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Party is a seller or buyer as frozen into an invoice snapshot, with the legal data an e-invoice needs.
type Party struct {
	Name               string `json:"name,omitempty"`
	TaxID              string `json:"tax_id,omitempty"`
	TaxOffice          string `json:"tax_office,omitempty"`
	RegistrationNo     string `json:"registration_no,omitempty"`
	TradeRegistryNo    string `json:"trade_registry_no,omitempty"`
	Line1              string `json:"line1,omitempty"`
	Line2              string `json:"line2,omitempty"`
	City               string `json:"city,omitempty"`
	Region             string `json:"region,omitempty"`
	PostalCode         string `json:"postal_code,omitempty"`
	Country            string `json:"country,omitempty"`
	Email              string `json:"email,omitempty"`
	EInvoiceRegistered bool   `json:"e_invoice_registered,omitempty"`
}

// Proto is the party as a command carries it.
func (p *Party) Proto() *commandsv1.PartySnapshot {
	if p == nil {
		return nil
	}
	return &commandsv1.PartySnapshot{Name: p.Name, TaxId: p.TaxID, TaxOffice: p.TaxOffice, RegistrationNo: p.RegistrationNo, TradeRegistryNo: p.TradeRegistryNo,
		Line1: p.Line1, Line2: p.Line2, City: p.City, Region: p.Region, PostalCode: p.PostalCode, Country: p.Country, Email: p.Email, EInvoiceRegistered: p.EInvoiceRegistered}
}

// PartyOf builds a party from a tenant's or customer's address and the legal data given.
func PartyOf(name string, a *plansv1.Address, taxID, taxOffice, registrationNo, tradeRegistryNo, email string, registered bool) *Party {
	return &Party{Name: name, TaxID: taxID, TaxOffice: taxOffice, RegistrationNo: registrationNo, TradeRegistryNo: tradeRegistryNo,
		Line1: a.GetLine1(), Line2: a.GetLine2(), City: a.GetCity(), Region: a.GetRegion(), PostalCode: a.GetPostalCode(), Country: a.GetCountry(),
		Email: email, EInvoiceRegistered: registered}
}

// EInvoice document kinds and states (the einvoice_documents table).
const (
	EInvoiceKindInvoice    = "invoice"
	EInvoiceKindCreditNote = "credit_note"

	EInvoicePending   = "pending"
	EInvoiceIssued    = "issued"
	EInvoiceFailed    = "failed"
	EInvoiceCancelled = "cancelled"
)

var einvoiceNS = uuid.MustParse("3b8e6d41-72c9-5a04-9f1d-c4e0a7b25d68")

// EInvoiceDocumentID is the deterministic id of the document of a source (an invoice or a credit note). It is the document's UUID at the integrator.
func EInvoiceDocumentID(tenantID uuid.UUID, kind string, sourceID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(einvoiceNS, fmt.Appendf(nil, "%s|%s|%s", tenantID, kind, sourceID))
}

// EInvoiceCommandID is the command id of one attempt of one action on a document.
func EInvoiceCommandID(docID uuid.UUID, action string, attempt int) uuid.UUID {
	return uuid.NewSHA1(einvoiceNS, fmt.Appendf(nil, "cmd|%s|%s|%d", docID, action, attempt))
}

// PublishEInvoiceCommand writes the outbox row of an IssueEInvoice command. The document id travels as saga id, so the reply finds its document.
func PublishEInvoiceCommand(ctx context.Context, tx pgx.Tx, now time.Time, tenantID uuid.UUID, environment string, cmd *commandsv1.IssueEInvoice, attempt int) error {
	docID, err := uuid.Parse(cmd.GetDocumentId())
	if err != nil {
		return err
	}
	id := EInvoiceCommandID(docID, cmd.GetAction(), attempt)
	env := &commandsv1.CommandEnvelope{
		CommandId: id.String(), SagaId: docID.String(), TenantId: tenantID.String(), Attempt: int32(attempt), //nolint:gosec // small
		IssuedAt: timestamppb.New(now), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
		Command: &commandsv1.CommandEnvelope_IssueEInvoice{IssueEInvoice: cmd},
	}
	if environment == "sandbox" {
		env.Environment = plansv1.Environment_ENVIRONMENT_SANDBOX
	}
	body, err := proto.Marshal(env)
	if err != nil {
		return err
	}
	return EnqueueOutbox(ctx, tx, tenantID, id, rabbitx.ExchangeCommands, rabbitx.QueueEInvoiceIssue, outbox.DestRabbitMQ, body)
}

// EnqueueEInvoice creates the document of an invoice or credit note and sends its first command, in the caller's transaction. A second call for the same
// source creates nothing (the unique index), so a replayed transition issues no second document. It reports whether a document was created.
func EnqueueEInvoice(ctx context.Context, tx pgx.Tx, now time.Time, tenantID uuid.UUID, environment, kind string, invoiceID uuid.UUID, creditNoteID *uuid.UUID, number string, cmd *commandsv1.IssueEInvoice) (bool, error) {
	src := invoiceID
	if creditNoteID != nil {
		src = *creditNoteID
	}
	docID := EInvoiceDocumentID(tenantID, kind, src)
	cmd.Action, cmd.DocumentId, cmd.Kind = "issue", docID.String(), kind
	raw, err := proto.Marshal(cmd)
	if err != nil {
		return false, err
	}
	n, err := sqlc.New(tx).InsertEInvoiceDocument(ctx, sqlc.InsertEInvoiceDocumentParams{TenantID: tenantID, ID: docID, Kind: kind, InvoiceID: invoiceID, CreditNoteID: creditNoteID,
		Number: number, Environment: environment, Request: raw, Now: now})
	if err != nil || n == 0 {
		return false, err
	}
	return true, PublishEInvoiceCommand(ctx, tx, now, tenantID, environment, cmd, 1)
}

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
	_, err = EnqueueEInvoice(ctx, tx, e.cfg.Now(), s.TenantID, s.Snap.Environment, EInvoiceKindInvoice, s.InvoiceID, nil, s.Number,
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
		Document: cn, OriginalDocumentId: EInvoiceDocumentID(tenantID, EInvoiceKindInvoice, invoiceID).String(),
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
