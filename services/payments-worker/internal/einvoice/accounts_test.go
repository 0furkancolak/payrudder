package einvoice

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	pkg "github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// stub is an integrator that answers every issue with a legal number made of its settings, so a test sees which account a document went through.
type stub struct{ tag string }

func (s stub) Capabilities() pkg.Capabilities {
	return pkg.Capabilities{Name: "stub", Types: []pkg.DocumentType{pkg.TypeEFatura, pkg.TypeEArsiv, pkg.TypePeppol}, CanCancel: true}
}
func (s stub) Issue(_ context.Context, r pkg.IssueRequest) (pkg.IssueResult, error) {
	return pkg.IssueResult{Ref: pkg.Ref{UUID: r.Doc.UUID, ExternalID: "ext"}, LegalNumber: "L-" + s.tag, State: pkg.StateIssued}, nil
}
func (s stub) Status(context.Context, pkg.Ref) (pkg.StatusResult, error) {
	return pkg.StatusResult{State: pkg.StateIssued, LegalNumber: "L-" + s.tag}, nil
}
func (s stub) Cancel(context.Context, pkg.Ref, string) error { return nil }
func (s stub) FetchPDF(context.Context, pkg.Ref) ([]byte, error) {
	return nil, pkg.ErrUnsupported
}
func (s stub) FetchUBL(context.Context, pkg.Ref) ([]byte, error) { return nil, pkg.ErrUnsupported }

// accounts is the store as the handler sees it: routing by country to a key, else the default.
type accounts struct {
	mu      sync.Mutex
	byKey   map[string]string // key -> settings (the stub's tag)
	routes  map[string]string // country -> key
	def     string
	resolve []string // the countries Resolve was asked about
}

func (a *accounts) open(key string) (store.EInvoiceAccount, error) {
	tag, ok := a.byKey[key]
	if !ok {
		return store.EInvoiceAccount{}, store.ErrNoAccount
	}
	return store.EInvoiceAccount{Key: key, Mode: "production", Settings: []byte(tag), IsDefault: key == a.def}, nil
}

func (a *accounts) Resolve(_ context.Context, _ uuid.UUID, _, country string) (store.EInvoiceAccount, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resolve = append(a.resolve, country)
	if k, ok := a.routes[country]; ok {
		return a.open(k)
	}
	return a.open(a.def)
}

func (a *accounts) Get(_ context.Context, _ uuid.UUID, _, key string) (store.EInvoiceAccount, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.open(key)
}

// issuances is the idempotency record in memory.
type issuances struct{ rows map[uuid.UUID]*store.Issuance }

func (i *issuances) Begin(_ context.Context, doc, _ uuid.UUID, env, provider, kind, docType string) (bool, error) {
	if _, ok := i.rows[doc]; ok {
		return false, nil
	}
	i.rows[doc] = &store.Issuance{DocumentID: doc, Environment: env, Provider: provider, Kind: kind, DocumentType: docType, Status: store.IssuanceStarted}
	return true, nil
}
func (i *issuances) Get(_ context.Context, doc, _ uuid.UUID) (store.Issuance, bool, error) {
	r, ok := i.rows[doc]
	if !ok {
		return store.Issuance{}, false, nil
	}
	return *r, true, nil
}
func (i *issuances) Complete(_ context.Context, doc, _ uuid.UUID, legal, external, _, _ string) error {
	r := i.rows[doc]
	r.Status, r.LegalNumber, r.ExternalID = store.IssuanceIssued, legal, external
	return nil
}
func (i *issuances) Cancel(_ context.Context, doc, _ uuid.UUID) error {
	i.rows[doc].Status = store.IssuanceCancelled
	return nil
}

func liveCommand(doc uuid.UUID, action, buyerCountry string) *commandsv1.CommandEnvelope {
	c := command()
	c.Action, c.DocumentId = action, doc.String()
	c.Document.Buyer = &commandsv1.PartySnapshot{Name: "Buyer", Country: buyerCountry, TaxId: "9876543210"}
	return &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: uuid.NewString(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
		Command: &commandsv1.CommandEnvelope_IssueEInvoice{IssueEInvoice: c}}
}

func newHandler(a *accounts, iss *issuances) *Handler {
	reg := pkg.NewRegistry()
	reg.Register("stub", func(settings []byte) (pkg.Provider, error) { return stub{tag: string(settings)}, nil })
	return &Handler{Registry: reg, Accounts: a, Issuances: iss, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func result(t *testing.T, h *Handler, cmd *commandsv1.CommandEnvelope) *commandsv1.EInvoiceResult {
	t.Helper()
	r, err := h.Handle(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	return r.GetEInvoiceResult()
}

// A document goes through the account that routing names for its buyer's country, and through the default for any other; the issuance records the account's key.
func TestDocumentsGoThroughTheAccountOfTheBuyersCountry(t *testing.T) {
	a := &accounts{byKey: map[string]string{"stub": "main", "stub~de": "german"}, routes: map[string]string{"DE": "stub~de"}, def: "stub"}
	iss := &issuances{rows: map[uuid.UUID]*store.Issuance{}}
	h := newHandler(a, iss)
	tr, de := uuid.New(), uuid.New()
	if r := result(t, h, liveCommand(tr, "issue", "TR")); r.GetState() != "issued" || r.GetLegalNumber() != "L-main" || r.GetProvider() != "stub" {
		t.Errorf("a Turkish buyer = %v", r)
	}
	if r := result(t, h, liveCommand(de, "issue", "DE")); r.GetState() != "issued" || r.GetLegalNumber() != "L-german" || r.GetProvider() != "stub~de" {
		t.Errorf("a German buyer = %v", r)
	}
	if iss.rows[de].Provider != "stub~de" {
		t.Errorf("the issuance records %q", iss.rows[de].Provider)
	}
}

// A repeat of a document that was begun goes through the account it began with even if routing has moved since, and so does its cancel: a second integrator
// must never issue a second legal document under one UUID.
func TestARepeatAndACancelKeepTheAccountTheDocumentBeganWith(t *testing.T) {
	a := &accounts{byKey: map[string]string{"stub": "main", "stub~de": "german"}, routes: map[string]string{"DE": "stub~de"}, def: "stub"}
	iss := &issuances{rows: map[uuid.UUID]*store.Issuance{}}
	h := newHandler(a, iss)
	doc := uuid.New()
	iss.rows[doc] = &store.Issuance{DocumentID: doc, Environment: "live", Provider: "stub", Kind: "invoice", DocumentType: "e-arsiv", Status: store.IssuanceStarted}
	// Routing now says the German account for DE, but the document began at the main one.
	if r := result(t, h, liveCommand(doc, "issue", "DE")); r.GetLegalNumber() != "L-main" || r.GetProvider() != "stub" {
		t.Errorf("a repeat went through %v", r)
	}
	issued := uuid.New()
	iss.rows[issued] = &store.Issuance{DocumentID: issued, Environment: "live", Provider: "stub~de", Kind: "invoice", DocumentType: "e-arsiv", Status: store.IssuanceIssued, LegalNumber: "L-german"}
	before := len(a.resolve)
	if r := result(t, h, liveCommand(issued, "cancel", "TR")); r.GetState() != "cancelled" {
		t.Errorf("cancel = %v", r)
	}
	if len(a.resolve) != before {
		t.Error("a cancel looked the account up by country instead of by the key it was issued through")
	}
	// The account it was issued through is gone: the cancel says so and does not use another one.
	delete(a.byKey, "stub~de")
	other := uuid.New()
	iss.rows[other] = &store.Issuance{DocumentID: other, Environment: "live", Provider: "stub~de", Kind: "invoice", DocumentType: "e-arsiv", Status: store.IssuanceIssued, LegalNumber: "L-x"}
	if r := result(t, h, liveCommand(other, "cancel", "TR")); r.GetState() != "failed" || r.GetErrorKey() != "einvoice.account.invalid" || r.GetRetryable() {
		t.Errorf("cancel through a removed account = %v", r)
	}
}

// With no default and no rule for the buyer's country the document waits for the operator, with the key that says an account is missing.
func TestNoDefaultAccountIsAMissingAccount(t *testing.T) {
	a := &accounts{byKey: map[string]string{"stub~de": "german"}, routes: map[string]string{"DE": "stub~de"}}
	h := newHandler(a, &issuances{rows: map[uuid.UUID]*store.Issuance{}})
	if r := result(t, h, liveCommand(uuid.New(), "issue", "FR")); r.GetState() != "failed" || r.GetErrorKey() != "einvoice.account.missing" || r.GetRetryable() {
		t.Errorf("no account for FR = %v", r)
	}
	if r := result(t, h, liveCommand(uuid.New(), "issue", "DE")); r.GetState() != "issued" {
		t.Errorf("DE = %v", r)
	}
}

// A document that was issued and then cancelled is answered as cancelled when its issue command comes again (a late redelivery): "issued" would make billing mark a cancelled
// legal document issued again.
func TestAnIssueRepeatedAfterACancelIsAnsweredCancelled(t *testing.T) {
	a := &accounts{byKey: map[string]string{"stub": "main"}, def: "stub"}
	iss := &issuances{rows: map[uuid.UUID]*store.Issuance{}}
	h := newHandler(a, iss)
	doc := uuid.New()
	if r := result(t, h, liveCommand(doc, "issue", "TR")); r.GetState() != "issued" {
		t.Fatalf("issue = %v", r)
	}
	if r := result(t, h, liveCommand(doc, "cancel", "TR")); r.GetState() != "cancelled" {
		t.Fatalf("cancel = %v", r)
	}
	if r := result(t, h, liveCommand(doc, "issue", "TR")); r.GetState() != "cancelled" || r.GetLegalNumber() != "" {
		t.Errorf("an issue after the cancel = %v, want cancelled", r)
	}
}
