// Package mock is the sandbox e-invoice provider: it accepts every document and answers with a fake legal number and external id. Sandbox tenants can
// reach no other provider (docs/phases/phase-30-e-invoice.md). Its documents have no legal effect.
package mock

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/0furkancolak/quotify/pkg/einvoice"
)

// Name is the registry name.
const Name = "mock"

const maxKept = 1000

// Register adds the mock provider to r. Its settings are ignored (an empty object or none).
func Register(r *einvoice.Registry) {
	r.Register(Name, func([]byte) (einvoice.Provider, error) { return New(), nil })
}

type entry struct {
	ubl       []byte
	legal     string
	cancelled bool
}

// Provider is the mock adapter. It remembers the last documents in memory, bounded.
type Provider struct {
	mu    sync.Mutex
	docs  map[string]*entry
	order []string
}

// New returns a mock provider.
func New() *Provider { return &Provider{docs: map[string]*entry{}} }

// Capabilities implements einvoice.Provider.
func (*Provider) Capabilities() einvoice.Capabilities {
	return einvoice.Capabilities{Name: Name, Types: []einvoice.DocumentType{einvoice.TypeEFatura, einvoice.TypeEArsiv, einvoice.TypePeppol},
		CanCancel: true, ReturnsPDF: true}
}

// LegalNumber is the fake legal number of a document: MCK (SBX in a sandbox), the year, nine digits derived from the UUID, so a repeat gives the same number.
func LegalNumber(doc einvoice.Document) string {
	sum := sha256.Sum256([]byte(doc.UUID))
	prefix := "MCK"
	if doc.Sandbox {
		prefix = "SBX"
	}
	return fmt.Sprintf("%s%d%09d", prefix, doc.IssueDate.Year(), binary.BigEndian.Uint64(sum[:8])%1_000_000_000)
}

// Issue implements einvoice.Provider. It is idempotent on the document UUID.
func (p *Provider) Issue(_ context.Context, r einvoice.IssueRequest) (einvoice.IssueResult, error) {
	d := r.Doc
	if d.UUID == "" {
		return einvoice.IssueResult{}, &einvoice.PermanentError{Code: "mock.uuid", Message: "the document has no UUID"}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.docs[d.UUID]
	if !ok {
		e = &entry{ubl: append([]byte(nil), r.UBL...), legal: LegalNumber(d)}
		p.docs[d.UUID] = e
		p.order = append(p.order, d.UUID)
		if len(p.order) > maxKept {
			delete(p.docs, p.order[0])
			p.order = p.order[1:]
		}
	}
	return einvoice.IssueResult{Ref: einvoice.Ref{ExternalID: "mock-" + d.UUID, UUID: d.UUID, Type: d.Type}, LegalNumber: e.legal, State: state(e), PDF: PlaceholderPDF(e.legal)}, nil
}

func state(e *entry) einvoice.State {
	if e.cancelled {
		return einvoice.StateCancelled
	}
	return einvoice.StateIssued
}

func (p *Provider) get(ref einvoice.Ref) (*entry, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.docs[ref.UUID]; ok {
		return e, nil
	}
	return nil, einvoice.ErrNotFound
}

// Status implements einvoice.Provider.
func (p *Provider) Status(_ context.Context, ref einvoice.Ref) (einvoice.StatusResult, error) {
	e, err := p.get(ref)
	if err != nil {
		return einvoice.StatusResult{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return einvoice.StatusResult{State: state(e), LegalNumber: e.legal, Detail: "mock"}, nil
}

// Cancel implements einvoice.Provider.
func (p *Provider) Cancel(_ context.Context, ref einvoice.Ref, _ string) error {
	e, err := p.get(ref)
	if err != nil {
		return err
	}
	p.mu.Lock()
	e.cancelled = true
	p.mu.Unlock()
	return nil
}

// FetchPDF implements einvoice.Provider.
func (p *Provider) FetchPDF(_ context.Context, ref einvoice.Ref) ([]byte, error) {
	e, err := p.get(ref)
	if err != nil {
		return nil, err
	}
	return PlaceholderPDF(e.legal), nil
}

// FetchUBL implements einvoice.Provider.
func (p *Provider) FetchUBL(_ context.Context, ref einvoice.Ref) ([]byte, error) {
	e, err := p.get(ref)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), e.ubl...), nil
}

// PlaceholderPDF is a tiny one-page PDF that says it is a mock document.
func PlaceholderPDF(legal string) []byte {
	stream := fmt.Sprintf("BT /F1 18 Tf 72 720 Td (MOCK e-invoice %s - no legal effect) Tj ET", legal)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	out := []byte("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = len(out)
		out = append(out, fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, o)...)
	}
	xref := len(out)
	out = append(out, fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)...)
	for _, off := range offsets {
		out = append(out, fmt.Sprintf("%010d 00000 n \n", off)...)
	}
	out = append(out, fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)...)
	return out
}
