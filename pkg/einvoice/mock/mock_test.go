package mock_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0furkancolak/quotify/pkg/einvoice"
	"github.com/0furkancolak/quotify/pkg/einvoice/mock"
)

func doc(uuid string, sandbox bool) einvoice.Document {
	return einvoice.Document{UUID: uuid, Type: einvoice.TypeEArsiv, IssueDate: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), Sandbox: sandbox}
}

func TestIssueIsIdempotentAndDeterministic(t *testing.T) {
	r := einvoice.NewRegistry()
	mock.Register(r)
	p, err := r.Build("mock", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a, err := p.Issue(ctx, einvoice.IssueRequest{Doc: doc("u-1", false), UBL: []byte("<x/>")})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := p.Issue(ctx, einvoice.IssueRequest{Doc: doc("u-1", false)})
	c, _ := p.Issue(ctx, einvoice.IssueRequest{Doc: doc("u-2", false)})
	if a.LegalNumber != b.LegalNumber || a.LegalNumber == c.LegalNumber {
		t.Errorf("numbers %s %s %s", a.LegalNumber, b.LegalNumber, c.LegalNumber)
	}
	if !strings.HasPrefix(a.LegalNumber, "MCK2026") || len(a.LegalNumber) != 16 || a.ExternalID != "mock-u-1" || a.State != einvoice.StateIssued {
		t.Errorf("result %+v", a)
	}
	s, _ := p.Issue(ctx, einvoice.IssueRequest{Doc: doc("u-3", true)})
	if !strings.HasPrefix(s.LegalNumber, "SBX2026") {
		t.Error(s.LegalNumber)
	}
	if !bytes.HasPrefix(a.PDF, []byte("%PDF-")) {
		t.Error("no pdf")
	}
	u, _ := p.FetchUBL(ctx, a.Ref)
	if string(u) != "<x/>" {
		t.Error("ubl lost")
	}
}

func TestCancelStatusAndUnknown(t *testing.T) {
	p := mock.New()
	ctx := context.Background()
	res, _ := p.Issue(ctx, einvoice.IssueRequest{Doc: doc("u-1", false)})
	if err := p.Cancel(ctx, res.Ref, "x"); err != nil {
		t.Fatal(err)
	}
	if st, _ := p.Status(ctx, res.Ref); st.State != einvoice.StateCancelled {
		t.Error(st)
	}
	if _, err := p.Status(ctx, einvoice.Ref{UUID: "nope"}); !errors.Is(err, einvoice.ErrNotFound) {
		t.Error(err)
	}
	if caps := p.Capabilities(); !caps.CanCancel || !caps.Supports(einvoice.TypePeppol) {
		t.Error(caps)
	}
}

func TestBounded(t *testing.T) {
	p := mock.New()
	ctx := context.Background()
	for i := 0; i < 1100; i++ {
		_, _ = p.Issue(ctx, einvoice.IssueRequest{Doc: doc("u-"+strconv.Itoa(i), false)})
	}
	if _, err := p.Status(ctx, einvoice.Ref{UUID: "u-0"}); !errors.Is(err, einvoice.ErrNotFound) {
		t.Error("oldest not evicted")
	}
	if _, err := p.Status(ctx, einvoice.Ref{UUID: "u-1099"}); err != nil {
		t.Error(err)
	}
}
