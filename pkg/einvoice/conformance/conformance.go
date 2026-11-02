// Package conformance is the test suite every e-invoice adapter has to pass (docs/einvoice-integrator-contract.md). An adapter's tests describe the adapter and
// a fake integrator in a Harness and call Run; the suite checks the behaviour the payments worker relies on: idempotent issue, the shape of the answers, the error
// model (permanent, transient, not found, unsupported), asynchronous confirmation, cancel, retrieval, and that no error carries a credential.
//
// A fake integrator speaks the adapter's wire protocol, so each adapter brings its own; WebhookServer is the one for the generic webhook adapter and is the
// reference for the protocol of pkg/einvoice/webhook. The suite never contacts a real integrator.
package conformance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/ubl"
)

// Harness describes the adapter under test and its fake integrator. Only New is required; a nil function means the fake cannot do it and the checks that need
// it are skipped (the mock provider has no wire, so it has no Fail).
type Harness struct {
	// New returns a provider wired to a fresh fake integrator that holds no documents. It may call t.Cleanup.
	New func(t testing.TB) einvoice.Provider
	// Secret is a credential in the provider's settings. No error may contain it.
	Secret string
	// Fail makes the fake answer every request with the HTTP status, with an error body that repeats Secret, until Fail(0).
	Fail func(status int)
	// SetPending(true) makes documents issued from now on stay pending at the integrator until Settle moves them to issued.
	SetPending func(on bool)
	Settle     func()
	// Created is the number of distinct documents the fake integrator holds; Requests the number of requests it has received.
	Created  func() int
	Requests func() int
}

// Request builds a valid issue request of the type: the document and its UBL, the way the worker would send them.
func Request(t testing.TB, typ einvoice.DocumentType, docUUID string) einvoice.IssueRequest {
	t.Helper()
	doc := einvoice.Document{
		Kind: einvoice.KindInvoice, Type: typ, UUID: docUUID, Number: "INV-2026-000001", IssueDate: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC),
		Currency: "EUR", Exponent: 2, Seller: seller(), Buyer: buyer(typ),
		Lines: []einvoice.Line{
			{No: 1, Kind: einvoice.LineItem, Description: "Subscription", AmountMinor: 10000},
			{No: 2, Kind: einvoice.LineTax, Description: "VAT 20%", AmountMinor: 2000},
		},
	}
	if typ != einvoice.TypePeppol {
		doc.Currency = "TRY"
	}
	body, err := ubl.Build(doc)
	if err != nil {
		t.Fatalf("conformance: the sample document does not build: %v", err)
	}
	return einvoice.IssueRequest{Doc: doc, UBL: body}
}

func seller() einvoice.Party {
	return einvoice.Party{Name: "Acme Yazılım A.Ş.", TaxID: "1234567890", TaxOffice: "Kadıköy", Street: "Bağdat Cad. 1", District: "Kadıköy", City: "İstanbul", PostalCode: "34700",
		Country: "TR", Email: "billing@acme.example", EndpointScheme: "0088", EndpointID: "5790000435975"}
}

func buyer(typ einvoice.DocumentType) einvoice.Party {
	if typ == einvoice.TypePeppol {
		return einvoice.Party{Name: "Beispiel GmbH", TaxID: "DE123456789", Street: "Hauptstr. 1", City: "Berlin", PostalCode: "10115", Country: "DE",
			EndpointScheme: "0088", EndpointID: "4012345000009"}
	}
	return einvoice.Party{Name: "Müşteri Ltd.", TaxID: "9876543210", TaxOffice: "Çankaya", Street: "Atatürk Blv. 5", District: "Çankaya", City: "Ankara", Country: "TR",
		EInvoiceRegistered: typ == einvoice.TypeEFatura}
}

// CreditNoteRequest builds a valid issue request for a credit note of the type that corrects the invoice issued under originalNumber, the way the worker would send
// it: Kind credit note, a Reference to the original's legal number, the credited lines.
func CreditNoteRequest(t testing.TB, typ einvoice.DocumentType, docUUID, originalNumber string) einvoice.IssueRequest {
	t.Helper()
	req := Request(t, typ, docUUID)
	doc := req.Doc
	doc.Kind, doc.Number, doc.Reason = einvoice.KindCreditNote, "CN-2026-000001", "conformance: a refund"
	doc.Reference = &einvoice.Reference{Number: originalNumber, IssueDate: doc.IssueDate}
	body, err := ubl.Build(doc)
	if err != nil {
		t.Fatalf("conformance: the sample credit note does not build: %v", err)
	}
	return einvoice.IssueRequest{Doc: doc, UBL: body}
}

// failure is the unwinding signal of check.Fatalf.
type failure struct{}

// check collects the failures of one named check.
type check struct {
	tb   testing.TB
	h    Harness
	msgs []string
}

func (c *check) Errorf(format string, a ...any) { c.msgs = append(c.msgs, fmt.Sprintf(format, a...)) }
func (c *check) Fatalf(format string, a ...any) { c.Errorf(format, a...); panic(failure{}) }

type result struct {
	name string
	msgs []string
}

// checks lists the checks in the order they run. A check that needs a part of the harness that is nil returns early.
func checks(caps einvoice.Capabilities) []struct {
	name string
	fn   func(*check)
} {
	type entry = struct {
		name string
		fn   func(*check)
	}
	out := []entry{{"capabilities", capabilitiesCheck}}
	for _, typ := range caps.Types {
		out = append(out, entry{"issue-" + string(typ), func(c *check) { issueCheck(c, typ) }})
	}
	return append(out,
		entry{"issue-concurrent-is-one-document", concurrentIssueCheck},
		entry{"credit-note", creditNoteCheck},
		entry{"cancel-unknown-is-not-found", cancelUnknownCheck},
		entry{"issue-unsupported-type", unsupportedTypeCheck},
		entry{"status-unknown-is-not-found", unknownCheck},
		entry{"async-confirmation", asyncCheck},
		entry{"cancel", cancelCheck},
		entry{"retrieve", retrieveCheck},
		entry{"errors-permanent-and-transient", errorMappingCheck},
		entry{"errors-never-leak-credentials", leakCheck},
		entry{"sandbox-documents-refused", sandboxCheck},
		entry{"cancelled-context", contextCheck},
	)
}

// execute runs every check and returns what each found. It is what Run reports and what the suite's own tests assert on.
func execute(tb testing.TB, h Harness) []result {
	caps := h.New(tb).Capabilities()
	var out []result
	for _, e := range checks(caps) {
		c := &check{tb: tb, h: h}
		func() {
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(failure); !ok {
						panic(r)
					}
				}
			}()
			e.fn(c)
		}()
		out = append(out, result{e.name, c.msgs})
	}
	return out
}

// Run executes the suite as one subtest per check.
func Run(t *testing.T, h Harness) {
	t.Helper()
	for _, r := range execute(t, h) {
		t.Run(r.name, func(t *testing.T) {
			for _, m := range r.msgs {
				t.Error(m)
			}
		})
	}
}

var ctxBg = context.Background

// newUUID is a stable UUID per name, so a check's documents are the same on every run and never collide with another check's.
func newUUID(_ *check, name string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("einvoice-conformance/"+name)).String()
}

func capabilitiesCheck(c *check) {
	caps := c.h.New(c.tb).Capabilities()
	if caps.Name == "" || len(caps.Types) == 0 {
		c.Errorf("Capabilities need a name and at least one document type: %+v", caps)
	}
	for _, typ := range caps.Types {
		if !typ.Valid() || !caps.Supports(typ) {
			c.Errorf("type %q is not a valid document type that Supports accepts", typ)
		}
	}
}

// issueCheck: the answer to an issue has the shape the worker needs, a repeat under the same UUID is the same document (never a second one), and Status agrees.
func issueCheck(c *check, typ einvoice.DocumentType) {
	p := c.h.New(c.tb)
	req := Request(c.tb, typ, newUUID(c, "issue-"+string(typ)))
	res, err := p.Issue(ctxBg(), req)
	if err != nil {
		c.Fatalf("Issue: %v", err)
	}
	if res.UUID != req.Doc.UUID || res.Type != typ || res.ExternalID == "" {
		c.Errorf("the reference must carry the document's UUID and type and the integrator's id: %+v", res.Ref)
	}
	if res.State != einvoice.StatePending && res.State != einvoice.StateIssued {
		c.Errorf("a successful Issue is pending or issued, not %q (a refusal is a *PermanentError)", res.State)
	}
	if res.State == einvoice.StateIssued && res.LegalNumber == "" {
		c.Errorf("an issued document has a legal number")
	}
	again, err := p.Issue(ctxBg(), req)
	if err != nil {
		c.Fatalf("repeating Issue: %v", err)
	}
	if again.UUID != res.UUID {
		c.Errorf("the repeat answers for another document: %q", again.UUID)
	}
	if res.LegalNumber != "" && again.LegalNumber != "" && res.LegalNumber != again.LegalNumber {
		c.Errorf("the repeat has another legal number: %q then %q", res.LegalNumber, again.LegalNumber)
	}
	if res.State == einvoice.StateIssued && again.State != einvoice.StateIssued {
		c.Errorf("an issued document went back to %q on the repeat", again.State)
	}
	if c.h.Created != nil && c.h.Created() != 1 {
		c.Errorf("the repeat created a second document at the integrator: %d documents", c.h.Created())
	}
	st, err := p.Status(ctxBg(), res.Ref)
	if err != nil {
		c.Fatalf("Status: %v", err)
	}
	if st.State != einvoice.StatePending && st.State != einvoice.StateIssued {
		c.Errorf("Status of a document just issued is %q", st.State)
	}
	if st.LegalNumber != "" && res.LegalNumber != "" && st.LegalNumber != res.LegalNumber {
		c.Errorf("Status has another legal number: %q, issue said %q", st.LegalNumber, res.LegalNumber)
	}
}

func unsupportedTypeCheck(c *check) {
	p := c.h.New(c.tb)
	caps := p.Capabilities()
	for _, typ := range []einvoice.DocumentType{einvoice.TypeEFatura, einvoice.TypeEArsiv, einvoice.TypePeppol} {
		if caps.Supports(typ) {
			continue
		}
		_, err := p.Issue(ctxBg(), Request(c.tb, typ, newUUID(c, "unsupported-"+string(typ))))
		var perm *einvoice.PermanentError
		if !errors.As(err, &perm) {
			c.Errorf("a %s document, which the adapter does not send, must be refused with a *PermanentError, got %v", typ, err)
		}
	}
}

func unknownCheck(c *check) {
	p := c.h.New(c.tb)
	typ := p.Capabilities().Types[0]
	_, err := p.Status(ctxBg(), einvoice.Ref{UUID: newUUID(c, "unknown"), ExternalID: "unknown", Type: typ})
	if !errors.Is(err, einvoice.ErrNotFound) {
		c.Errorf("Status of a document the integrator does not know must be ErrNotFound, got %v", err)
	}
	if _, err := p.FetchPDF(ctxBg(), einvoice.Ref{UUID: newUUID(c, "unknown"), Type: typ}); !errors.Is(err, einvoice.ErrNotFound) && !errors.Is(err, einvoice.ErrUnsupported) {
		c.Errorf("FetchPDF of an unknown document must be ErrNotFound (or ErrUnsupported), got %v", err)
	}
}

// asyncCheck: an integrator that confirms later answers pending, Status stays pending, and after the confirmation says issued. The worker needs the legal number from
// Status then (or from the repeated Issue), because it will not complete a document without one.
func asyncCheck(c *check) {
	if c.h.SetPending == nil || c.h.Settle == nil {
		return
	}
	p := c.h.New(c.tb)
	typ := p.Capabilities().Types[0]
	c.h.SetPending(true)
	req := Request(c.tb, typ, newUUID(c, "async"))
	res, err := p.Issue(ctxBg(), req)
	if err != nil {
		c.Fatalf("Issue: %v", err)
	}
	if res.State != einvoice.StatePending {
		c.Errorf("an integrator that has not confirmed yet is answered with %q, want pending", res.State)
	}
	if st, err := p.Status(ctxBg(), res.Ref); err != nil || st.State != einvoice.StatePending {
		c.Errorf("Status before the confirmation: %+v, %v; want pending", st, err)
	}
	c.h.Settle()
	st, err := p.Status(ctxBg(), res.Ref)
	switch {
	case err != nil:
		c.Errorf("Status after the confirmation: %v", err)
	case st.State != einvoice.StateIssued:
		c.Errorf("Status after the confirmation is %q, want issued", st.State)
	case st.LegalNumber == "":
		c.Errorf("Status after the confirmation has no legal number, so the worker can never complete the document")
	}
}

// cancelCheck: an adapter that cannot cancel says ErrUnsupported; one that can either cancels or says ErrUnsupported for the types where the law does not allow it,
// and a cancelled document reads cancelled.
func cancelCheck(c *check) {
	p := c.h.New(c.tb)
	caps := p.Capabilities()
	for _, typ := range caps.Types {
		req := Request(c.tb, typ, newUUID(c, "cancel-"+string(typ)))
		res, err := p.Issue(ctxBg(), req)
		if err != nil {
			c.Fatalf("Issue %s: %v", typ, err)
		}
		err = p.Cancel(ctxBg(), res.Ref, "conformance")
		switch {
		case errors.Is(err, einvoice.ErrUnsupported):
		case !caps.CanCancel:
			c.Errorf("%s: CanCancel is false, Cancel must be ErrUnsupported, got %v", typ, err)
		case err != nil:
			c.Errorf("%s: Cancel: %v", typ, err)
		default:
			if st, serr := p.Status(ctxBg(), res.Ref); serr != nil || st.State != einvoice.StateCancelled {
				c.Errorf("%s: after Cancel Status is %+v, %v; want cancelled", typ, st, serr)
			}
		}
	}
}

// retrieveCheck: the PDF is a PDF and the UBL is XML when the adapter says it returns them; otherwise ErrUnsupported is the only acceptable refusal.
func retrieveCheck(c *check) {
	p := c.h.New(c.tb)
	caps := p.Capabilities()
	typ := caps.Types[0]
	res, err := p.Issue(ctxBg(), Request(c.tb, typ, newUUID(c, "retrieve")))
	if err != nil {
		c.Fatalf("Issue: %v", err)
	}
	pdf, err := p.FetchPDF(ctxBg(), res.Ref)
	switch {
	case caps.ReturnsPDF && err != nil:
		c.Errorf("ReturnsPDF is true but FetchPDF failed: %v", err)
	case caps.ReturnsPDF && !strings.HasPrefix(string(pdf), "%PDF-"):
		c.Errorf("FetchPDF did not return a PDF")
	case !caps.ReturnsPDF && err != nil && !errors.Is(err, einvoice.ErrUnsupported):
		c.Errorf("FetchPDF of an adapter without PDFs must be ErrUnsupported, got %v", err)
	}
	xml, err := p.FetchUBL(ctxBg(), res.Ref)
	if err != nil && !errors.Is(err, einvoice.ErrUnsupported) {
		c.Errorf("FetchUBL: %v", err)
	}
	if err == nil && !strings.Contains(string(xml), "<") {
		c.Errorf("FetchUBL did not return XML")
	}
}

// errorMappingCheck: what retrying cannot change is a *PermanentError and what it might change is a *TransientError. The worker retries the second kind with backoff
// and stops at the first.
func errorMappingCheck(c *check) {
	if c.h.Fail == nil {
		return
	}
	p := c.h.New(c.tb)
	typ := p.Capabilities().Types[0]
	defer c.h.Fail(0)
	for status, permanent := range map[int]bool{400: true, 401: true, 403: true, 422: true, 408: false, 429: false, 500: false, 502: false, 503: false} {
		c.h.Fail(status)
		_, err := p.Issue(ctxBg(), Request(c.tb, typ, newUUID(c, fmt.Sprint("errors-", status))))
		var perm *einvoice.PermanentError
		var trans *einvoice.TransientError
		switch {
		case err == nil:
			c.Errorf("HTTP %d: Issue succeeded", status)
		case permanent && !errors.As(err, &perm):
			c.Errorf("HTTP %d must be a *PermanentError, got %T: %v", status, err, err)
		case !permanent && !errors.As(err, &trans):
			c.Errorf("HTTP %d must be a *TransientError, got %T: %v", status, err, err)
		}
	}
}

// leakCheck: a refusal is shown to the operator and logged; the integrator's own text may repeat the key it was sent, and the adapter must not pass it on.
func leakCheck(c *check) {
	if c.h.Fail == nil || c.h.Secret == "" {
		return
	}
	p := c.h.New(c.tb)
	typ := p.Capabilities().Types[0]
	defer c.h.Fail(0)
	ref := einvoice.Ref{UUID: newUUID(c, "leak"), ExternalID: "leak", Type: typ}
	for _, status := range []int{400, 401, 422, 500} {
		c.h.Fail(status)
		_, e1 := p.Issue(ctxBg(), Request(c.tb, typ, newUUID(c, fmt.Sprint("leak-", status))))
		_, e2 := p.Status(ctxBg(), ref)
		e3 := p.Cancel(ctxBg(), ref, "x")
		for _, err := range []error{e1, e2, e3} {
			if err != nil && strings.Contains(err.Error(), c.h.Secret) {
				c.Errorf("HTTP %d: an error carries the credential: %v", status, err)
			}
		}
	}
}

// sandboxCheck: only the mock provider accepts a sandbox document, and the others do not even contact the integrator with one.
func sandboxCheck(c *check) {
	p := c.h.New(c.tb)
	caps := p.Capabilities()
	req := Request(c.tb, caps.Types[0], newUUID(c, "sandbox"))
	req.Doc.Sandbox = true
	before := 0
	if c.h.Requests != nil {
		before = c.h.Requests()
	}
	_, err := p.Issue(ctxBg(), req)
	if caps.Name == "mock" {
		if err != nil {
			c.Errorf("the mock provider must accept sandbox documents: %v", err)
		}
		return
	}
	var perm *einvoice.PermanentError
	if !errors.As(err, &perm) {
		c.Errorf("a sandbox document must be refused with a *PermanentError, got %v", err)
	}
	if c.h.Requests != nil && c.h.Requests() != before {
		c.Errorf("a sandbox document reached the integrator")
	}
}

// contextCheck: an adapter that does I/O stops with the context and never reports a cancelled call as a refusal (which would mark the document failed for good).
func contextCheck(c *check) {
	if c.h.Fail == nil { // no wire, nothing to cancel
		return
	}
	p := c.h.New(c.tb)
	ctx, cancel := context.WithTimeout(ctxBg(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	_, err := p.Issue(ctx, Request(c.tb, p.Capabilities().Types[0], newUUID(c, "ctx")))
	var perm *einvoice.PermanentError
	if err == nil || errors.As(err, &perm) {
		c.Errorf("an issue with a cancelled context must fail but not as a *PermanentError, got %v", err)
	}
}

// concurrentIssueCheck (contract 2): the worker may deliver the same command twice at the same moment (a redelivery, two replicas). Issuing one UUID from several
// goroutines at once must leave one document at the integrator, and every caller must be told about the same one: a second legal document under one UUID is the failure
// this contract exists to prevent. It needs Created (the fake's count of documents).
func concurrentIssueCheck(c *check) {
	if c.h.Created == nil {
		return
	}
	p := c.h.New(c.tb)
	typ := p.Capabilities().Types[0]
	req := Request(c.tb, typ, newUUID(c, "concurrent"))
	const callers = 8
	results := make([]einvoice.IssueResult, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = p.Issue(ctxBg(), req)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			c.Fatalf("a concurrent Issue (caller %d): %v", i, err)
		}
	}
	if n := c.h.Created(); n != 1 {
		c.Errorf("%d callers issuing one UUID at once left %d documents at the integrator, want 1", callers, n)
	}
	for i, r := range results {
		if r.UUID != results[0].UUID || r.ExternalID != results[0].ExternalID || (r.LegalNumber != "" && results[0].LegalNumber != "" && r.LegalNumber != results[0].LegalNumber) {
			c.Errorf("caller %d was told about another document than caller 0: %+v and %+v", i, r.Ref, results[0].Ref)
		}
	}
}

// creditNoteCheck (contract 5): a credit note is issued like an invoice, under a UUID of its own, with a Reference to the legal number of the original, for every type the
// adapter sends; it is a second document at the integrator, never the original again, and it has the shape of any answer.
func creditNoteCheck(c *check) {
	p := c.h.New(c.tb)
	for _, typ := range p.Capabilities().Types {
		inv, err := p.Issue(ctxBg(), Request(c.tb, typ, newUUID(c, "credit-original-"+string(typ))))
		if err != nil {
			c.Fatalf("Issue %s: %v", typ, err)
		}
		before := 0
		if c.h.Created != nil {
			before = c.h.Created()
		}
		original := inv.LegalNumber
		if original == "" {
			original = "INV-2026-000001" // pending: the worker would not send the credit note yet, the reference is Payrudder's own number then
		}
		req := CreditNoteRequest(c.tb, typ, newUUID(c, "credit-note-"+string(typ)), original)
		cn, err := p.Issue(ctxBg(), req)
		if err != nil {
			c.Errorf("%s: Issue of a credit note: %v", typ, err)
			continue
		}
		if cn.UUID != req.Doc.UUID || cn.UUID == inv.UUID || cn.Type != typ || cn.ExternalID == "" || cn.ExternalID == inv.ExternalID {
			c.Errorf("%s: a credit note is a document of its own: %+v, the invoice %+v", typ, cn.Ref, inv.Ref)
		}
		if cn.State != einvoice.StatePending && cn.State != einvoice.StateIssued {
			c.Errorf("%s: a credit note's answer is pending or issued, not %q", typ, cn.State)
		}
		if cn.State == einvoice.StateIssued && cn.LegalNumber == "" {
			c.Errorf("%s: an issued credit note has a legal number", typ)
		}
		if cn.LegalNumber != "" && cn.LegalNumber == inv.LegalNumber {
			c.Errorf("%s: the credit note has the invoice's legal number %q", typ, cn.LegalNumber)
		}
		if c.h.Created != nil && c.h.Created() != before+1 {
			c.Errorf("%s: a credit note is one more document at the integrator: %d before, %d after", typ, before, c.h.Created())
		}
	}
}

// cancelUnknownCheck (contract 1 and 5): cancelling a document the integrator does not know is ErrNotFound (or ErrUnsupported where the adapter cannot cancel at all),
// never a success, which would record a cancel of nothing, and never a permanent failure of the call.
func cancelUnknownCheck(c *check) {
	p := c.h.New(c.tb)
	caps := p.Capabilities()
	err := p.Cancel(ctxBg(), einvoice.Ref{UUID: newUUID(c, "cancel-unknown"), ExternalID: "unknown", Type: caps.Types[0]}, "conformance")
	if !errors.Is(err, einvoice.ErrNotFound) && !errors.Is(err, einvoice.ErrUnsupported) {
		c.Errorf("Cancel of a document nobody issued must be ErrNotFound (or ErrUnsupported), got %v", err)
	}
	if !caps.CanCancel && !errors.Is(err, einvoice.ErrUnsupported) {
		c.Errorf("CanCancel is false: Cancel must be ErrUnsupported even for an unknown document, got %v", err)
	}
}
