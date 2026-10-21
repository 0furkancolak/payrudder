package einvoice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/blob"
	pkg "github.com/0furkancolak/quotify/pkg/einvoice"
	"github.com/0furkancolak/quotify/pkg/einvoice/mock"
	"github.com/0furkancolak/quotify/pkg/einvoice/ubl"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/workerx"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/store"
)

// Accounts gives the sealed settings of a tenant's integrator account (store.EInvoiceAccounts).
type Accounts interface {
	Settings(ctx context.Context, tenant uuid.UUID, env string) (provider string, settings []byte, err error)
}

// Issuances is the idempotency record of documents (store.EInvoiceIssuances).
type Issuances interface {
	Begin(ctx context.Context, doc, tenant uuid.UUID, env, provider, kind, docType string) (bool, error)
	Get(ctx context.Context, doc, tenant uuid.UUID) (store.Issuance, bool, error)
	Complete(ctx context.Context, doc, tenant uuid.UUID, legal, external, xmlKey, pdfKey string) error
	Cancel(ctx context.Context, doc, tenant uuid.UUID) error
}

// Blobs is the object storage for the UBL and PDF (pkg/blob.Store satisfies it).
type Blobs interface {
	Put(ctx context.Context, key string, body []byte, o blob.PutOptions) error
}

// Error keys carried by a failed EInvoiceResult (docs/error-keys.md).
var (
	KeyAccountMissing = errkey.Key("einvoice.account.missing")
	KeyAccountInvalid = errkey.Key("einvoice.account.invalid")
	KeySellerData     = errkey.Key("einvoice.seller.incomplete")
	KeyBuyerData      = errkey.Key("einvoice.buyer.incomplete")
	KeyRejected       = errkey.Key("einvoice.provider.rejected")
	KeyUnavailable    = errkey.Key("einvoice.provider.unavailable")
	KeyCancel         = errkey.Key("einvoice.cancel.unsupported")
)

// Handler executes IssueEInvoice commands.
type Handler struct {
	Registry  *pkg.Registry
	Accounts  Accounts
	Issuances Issuances
	Blobs     Blobs
	Log       *slog.Logger
	// Demo makes every tenant, live ones too, use the mock: a demo stack can never reach a real integrator. It also allows a live account to be the mock.
	Demo bool

	mockOnce sync.Once
	mock     *mock.Provider
}

// mockProvider is one instance for the process: the mock remembers its documents in memory.
func (h *Handler) mockProvider() pkg.Provider {
	h.mockOnce.Do(func() { h.mock = mock.New() })
	return h.mock
}

func failed(key, msg string, retryable bool) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_EInvoiceResult{EInvoiceResult: &commandsv1.EInvoiceResult{
		State: "failed", Retryable: retryable, ErrorKey: key, ErrorMessage: truncate(msg, 400)}}}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func issued(i store.Issuance) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_EInvoiceResult{EInvoiceResult: &commandsv1.EInvoiceResult{
		State: "issued", LegalNumber: i.LegalNumber, ExternalId: i.ExternalID, DocumentType: i.DocumentType, Provider: i.Provider, XmlObjectKey: i.XMLKey, PdfObjectKey: i.PDFKey}}}
}

// Handle implements workerx.Handler. Business outcomes, including refusals by the integrator, are replies; an error is returned only when the command cannot
// be executed now at all (the database is down), and the queue retries it.
func (h *Handler) Handle(ctx context.Context, cmd *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error) {
	c := cmd.GetIssueEInvoice()
	doc, err := uuid.Parse(c.GetDocumentId())
	tenantID, err2 := uuid.Parse(cmd.GetTenantId())
	if c == nil || err != nil || err2 != nil {
		return nil, fmt.Errorf("%w: not an e-invoice command", workerx.ErrPoison)
	}
	sandbox := cmd.GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX
	env := "live"
	if sandbox {
		env = "sandbox"
	}
	switch c.GetAction() {
	case "issue":
		if c.GetDocument() == nil {
			return nil, fmt.Errorf("%w: no document", workerx.ErrPoison)
		}
		return h.issue(ctx, c, doc, tenantID, env, sandbox)
	case "cancel":
		return h.cancel(ctx, c, doc, tenantID, env, sandbox)
	}
	return nil, fmt.Errorf("%w: unknown action %q", workerx.ErrPoison, c.GetAction())
}

// provider resolves the adapter for a tenant and environment: sandbox (and demo) always the mock, live the tenant's sealed account.
// A non-nil reply is the answer when there is none.
func (h *Handler) provider(ctx context.Context, tenantID uuid.UUID, env string) (pkg.Provider, string, *commandsv1.ReplyEnvelope, error) {
	if env == "sandbox" || h.Demo {
		return h.mockProvider(), mock.Name, nil, nil
	}
	if h.Accounts == nil {
		return nil, "", failed(KeyAccountMissing, "e-invoice accounts are disabled on this deployment (QUOTIFY_SECRETS_KEY)", false), nil
	}
	name, settings, err := h.Accounts.Settings(ctx, tenantID, "live")
	if errors.Is(err, store.ErrNoAccount) {
		return nil, "", failed(KeyAccountMissing, "no e-invoice account is configured for the live environment", false), nil
	}
	if err != nil {
		return nil, "", nil, err
	}
	if name == mock.Name {
		return nil, "", failed(KeyAccountInvalid, "the mock provider is not allowed in the live environment", false), nil
	}
	p, err := h.Registry.Build(name, settings)
	if err != nil {
		return nil, "", failed(KeyAccountInvalid, "the e-invoice account cannot be used: "+err.Error(), false), nil
	}
	return p, name, nil, nil
}

// classify turns a provider error into a failed reply: a refusal waits for the operator, anything else is retried with backoff by billing-svc.
func classify(err error) *commandsv1.ReplyEnvelope {
	var perm *pkg.PermanentError
	if errors.As(err, &perm) {
		return failed(KeyRejected, perm.Code+": "+perm.Message, false)
	}
	return failed(KeyUnavailable, err.Error(), true)
}

func (h *Handler) issue(ctx context.Context, c *commandsv1.IssueEInvoice, docID, tenantID uuid.UUID, env string, sandbox bool) (*commandsv1.ReplyEnvelope, error) {
	prior, found, err := h.Issuances.Get(ctx, docID, tenantID)
	if err != nil {
		return nil, err
	}
	if found && (prior.Status == "issued" || prior.Status == "cancelled") {
		return issued(prior), nil // answered before; the integrator is not asked again
	}

	prov, name, reply, err := h.provider(ctx, tenantID, env)
	if reply != nil || err != nil {
		return reply, err
	}
	d := Document(c, sandbox)
	if sandbox || h.Demo {
		ApplySandboxDefaults(&d)
	}
	if d.Kind == pkg.KindCreditNote {
		orig, ok, err := h.Issuances.Get(ctx, uuid.MustParse(orDefault(c.GetOriginalDocumentId(), uuid.Nil.String())), tenantID)
		if err != nil {
			return nil, err
		}
		if !ok || orig.Status != "issued" || orig.LegalNumber == "" {
			return failed(KeyUnavailable, "the invoice this credit note corrects has not been issued yet", true), nil
		}
		d.Reference.Number = orig.LegalNumber
	}
	if reply, err := h.decideType(ctx, prov, &d); reply != nil || err != nil {
		return reply, err
	}
	if !prov.Capabilities().Supports(d.Type) {
		return failed(KeyAccountInvalid, fmt.Sprintf("%s cannot send %s documents", name, d.Type), false), nil
	}
	if prov.Capabilities().Name != mock.Name && sandbox {
		return failed(KeyAccountInvalid, "a sandbox document can only reach the mock provider", false), nil
	}
	if d.Type == pkg.TypeEArsiv && d.Buyer.TaxID == "" {
		d.Buyer.TaxID = consumerTaxID
	}
	body, err := ubl.Build(d)
	if err != nil {
		var inv *ubl.InvalidError
		if errors.As(err, &inv) {
			key := KeyRejected
			switch {
			case strings.HasPrefix(inv.Field, "seller"):
				key = KeySellerData
			case strings.HasPrefix(inv.Field, "buyer"):
				key = KeyBuyerData
			}
			return failed(key, inv.Field+": "+inv.Msg, false), nil
		}
		return failed(KeyRejected, err.Error(), false), nil
	}

	if _, err := h.Issuances.Begin(ctx, docID, tenantID, env, name, string(d.Kind), string(d.Type)); err != nil {
		return nil, err
	}
	res, err := prov.Issue(ctx, pkg.IssueRequest{Doc: d, UBL: body})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return classify(err), nil
	}
	ref := pkg.Ref{ExternalID: res.ExternalID, UUID: d.UUID, Type: d.Type}
	if res.State == pkg.StatePending || res.LegalNumber == "" {
		// Accepted but not confirmed (or confirmed without a number): ask once. If it is still not issued billing-svc asks again after its backoff, and the
		// repeat of Issue under the same UUID finds the same document.
		st, serr := prov.Status(ctx, ref)
		if serr == nil && st.State == pkg.StateIssued && st.LegalNumber != "" {
			res.State, res.LegalNumber = pkg.StateIssued, st.LegalNumber
		}
	}
	switch {
	case res.State == pkg.StateRejected:
		return failed(KeyRejected, "the integrator rejected the document", false), nil
	case res.State != pkg.StateIssued || res.LegalNumber == "":
		return failed(KeyUnavailable, "the integrator has not confirmed the document yet", true), nil
	}

	xmlKey, pdfKey := h.store(ctx, tenantID, docID, env, prov, ref, body, res.PDF)
	if err := h.Issuances.Complete(ctx, docID, tenantID, res.LegalNumber, res.ExternalID, xmlKey, pdfKey); err != nil {
		return nil, err // the integrator has it; the repeat finds it by UUID and records the same number
	}
	return issued(store.Issuance{LegalNumber: res.LegalNumber, ExternalID: res.ExternalID, DocumentType: string(d.Type), Provider: name, XMLKey: xmlKey, PDFKey: pdfKey}), nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// decideType picks the legal channel and, when the adapter can tell and the buyer is not known as registered, asks the integrator.
func (h *Handler) decideType(ctx context.Context, prov pkg.Provider, d *pkg.Document) (*commandsv1.ReplyEnvelope, error) {
	if d.Seller.Country == "" {
		return failed(KeySellerData, "seller.country is required", false), nil
	}
	if d.Seller.Country == "TR" && !d.Buyer.EInvoiceRegistered && prov.Capabilities().NeedsBuyerCheck && d.Buyer.TaxID != "" && d.Buyer.TaxID != consumerTaxID {
		if bc, ok := prov.(pkg.BuyerChecker); ok {
			registered, alias, err := bc.IsEInvoiceUser(ctx, d.Buyer.TaxID)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return classify(err), nil
			}
			d.Buyer.EInvoiceRegistered = registered
			if alias != "" {
				d.Buyer.Alias = alias
			}
		}
	}
	d.Type = ChooseType(*d)
	return nil, nil
}

// store keeps the UBL (the integrator's sealed copy when it gives one, else ours) and the PDF, and returns their keys. A failure to store is not a failure to
// issue: the document exists at the integrator, so the keys stay empty and the console shows no download rather than the document failing.
func (h *Handler) store(ctx context.Context, tenantID, docID uuid.UUID, env string, prov pkg.Provider, ref pkg.Ref, ours, pdf []byte) (xmlKey, pdfKey string) {
	if h.Blobs == nil {
		return "", ""
	}
	tags := map[string]string{"environment": env}
	x := ours
	if sealed, err := prov.FetchUBL(ctx, ref); err == nil && len(sealed) > 0 {
		x = sealed
	}
	key := fmt.Sprintf("%s/einvoice/%s/document.xml", tenantID, docID)
	if err := h.Blobs.Put(ctx, key, x, blob.PutOptions{ContentType: "application/xml", Tags: tags}); err == nil {
		xmlKey = key
	} else {
		h.warn(ctx, "could not store the e-invoice UBL", err)
	}
	if len(pdf) == 0 {
		if b, err := prov.FetchPDF(ctx, ref); err == nil {
			pdf = b
		}
	}
	if len(pdf) > 0 {
		key := fmt.Sprintf("%s/einvoice/%s/document.pdf", tenantID, docID)
		if err := h.Blobs.Put(ctx, key, pdf, blob.PutOptions{ContentType: "application/pdf", Tags: tags}); err == nil {
			pdfKey = key
		} else {
			h.warn(ctx, "could not store the e-invoice PDF", err)
		}
	}
	return xmlKey, pdfKey
}

func (h *Handler) warn(ctx context.Context, msg string, err error) {
	if h.Log != nil {
		h.Log.WarnContext(ctx, msg, "err", err)
	}
}

func (h *Handler) cancel(ctx context.Context, c *commandsv1.IssueEInvoice, docID, tenantID uuid.UUID, env string, sandbox bool) (*commandsv1.ReplyEnvelope, error) {
	rec, found, err := h.Issuances.Get(ctx, docID, tenantID)
	if err != nil {
		return nil, err
	}
	switch {
	case !found || rec.Status == "started":
		return failed(KeyCancel, "the document has not been issued", false), nil
	case rec.Status == "cancelled":
		return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_EInvoiceResult{EInvoiceResult: &commandsv1.EInvoiceResult{State: "cancelled"}}}, nil
	}
	prov, name, reply, err := h.provider(ctx, tenantID, env)
	if reply != nil || err != nil {
		return reply, err
	}
	if name != rec.Provider {
		return failed(KeyAccountInvalid, "the e-invoice account is not the one the document was issued through", false), nil
	}
	if !prov.Capabilities().CanCancel {
		return failed(KeyCancel, name+" cannot cancel documents", false), nil
	}
	err = prov.Cancel(ctx, pkg.Ref{ExternalID: rec.ExternalID, UUID: docID.String(), Type: pkg.DocumentType(rec.DocumentType)}, c.GetReason())
	switch {
	case errors.Is(err, pkg.ErrUnsupported):
		return failed(KeyCancel, "this kind of document cannot be cancelled", false), nil
	case errors.Is(err, pkg.ErrNotFound) && name == mock.Name:
		// The mock forgets documents when the worker restarts; there is nothing to cancel at an integrator that does not exist.
	case err != nil:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r := classify(err)
		r.GetEInvoiceResult().Retryable = false // the operator decides whether to try again
		return r, nil
	}
	if err := h.Issuances.Cancel(ctx, docID, tenantID); err != nil {
		return nil, err
	}
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_EInvoiceResult{EInvoiceResult: &commandsv1.EInvoiceResult{State: "cancelled"}}}, nil
}
