package conformance

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/mock"
)

// stub is the mock provider with one defect at a time: the suite has to see each of them, or it is not testing the contract.
type stub struct {
	*mock.Provider
	name         string
	issue        func(einvoice.IssueResult, int) einvoice.IssueResult
	issueErr     func(status int) error
	statusErr    error
	ctxRefusal   bool // a cancelled context is reported as a refusal
	calls, fails atomic.Int64

	// created counts the documents the stub's integrator holds (distinct UUIDs), when track is set; doubleIssue makes it count every call, as an adapter does that
	// issues twice when two calls for one UUID arrive together.
	track, doubleIssue bool
	created            atomic.Int64
	seen               sync.Map
	// creditIsOriginal answers a credit note with the invoice it corrects; cancelNothing lets Cancel succeed for a document nobody issued.
	creditIsOriginal, cancelNothing bool
	lastInvoice                     einvoice.Ref
	mu                              sync.Mutex
}

func (s *stub) Capabilities() einvoice.Capabilities {
	c := s.Provider.Capabilities()
	c.Name = s.name
	return c
}

func (s *stub) Issue(ctx context.Context, r einvoice.IssueRequest) (einvoice.IssueResult, error) {
	if s.ctxRefusal && ctx.Err() != nil {
		return einvoice.IssueResult{}, &einvoice.PermanentError{Code: "ctx", Message: ctx.Err().Error()}
	}
	if st := int(s.fails.Load()); st != 0 && s.issueErr != nil {
		return einvoice.IssueResult{}, s.issueErr(st)
	}
	res, err := s.Provider.Issue(ctx, r)
	if s.track {
		if _, dup := s.seen.LoadOrStore(r.Doc.UUID, true); !dup || s.doubleIssue {
			s.created.Add(1)
		}
	}
	s.mu.Lock()
	if r.Doc.Kind == einvoice.KindCreditNote && s.creditIsOriginal {
		res.Ref = s.lastInvoice
	} else if r.Doc.Kind == einvoice.KindInvoice {
		s.lastInvoice = res.Ref
	}
	s.mu.Unlock()
	if s.issue != nil {
		res = s.issue(res, int(s.calls.Add(1)))
	}
	return res, err
}

func (s *stub) Cancel(ctx context.Context, ref einvoice.Ref, reason string) error {
	if s.cancelNothing {
		return nil
	}
	return s.Provider.Cancel(ctx, ref, reason)
}

func (s *stub) Status(ctx context.Context, ref einvoice.Ref) (einvoice.StatusResult, error) {
	if s.statusErr != nil {
		return einvoice.StatusResult{}, s.statusErr
	}
	return s.Provider.Status(ctx, ref)
}

// failedChecks runs the suite on s and returns the messages of the checks that failed. wire says whether s has a fake wire to inject failures into.
func failedChecks(t *testing.T, s *stub, secret string, wire bool) map[string][]string {
	t.Helper()
	h := Harness{New: func(testing.TB) einvoice.Provider {
		if s.track { // each check starts with an integrator that holds nothing
			s.created.Store(0)
			s.seen = sync.Map{}
		}
		return s
	}, Secret: secret}
	if wire {
		h.Fail = func(st int) { s.fails.Store(int64(st)) }
	}
	if s.track {
		h.Created = func() int { return int(s.created.Load()) }
	}
	out := map[string][]string{}
	for _, r := range execute(t, h) {
		if len(r.msgs) > 0 {
			out[r.name] = r.msgs
		}
	}
	return out
}

func TestTheReferenceAdapterPasses(t *testing.T) {
	if got := failedChecks(t, &stub{Provider: mock.New(), name: "mock"}, "", false); len(got) != 0 {
		t.Fatalf("the mock provider fails its own contract: %v", got)
	}
}

func TestTheSuiteSeesEachDefect(t *testing.T) {
	permanent := func(int) error { return &einvoice.PermanentError{Code: "x", Message: "no"} }
	for name, c := range map[string]struct {
		s    *stub
		want string
	}{
		"a repeat with another legal number": {&stub{Provider: mock.New(), name: "mock", issue: func(r einvoice.IssueResult, n int) einvoice.IssueResult {
			r.LegalNumber += string(rune('A' + n))
			return r
		}}, "issue-peppol"},
		"an unknown document that is not ErrNotFound": {&stub{Provider: mock.New(), name: "mock", statusErr: errors.New("boom")}, "status-unknown-is-not-found"},
		"every failure permanent":                     {&stub{Provider: mock.New(), name: "mock", issueErr: permanent}, "errors-permanent-and-transient"},
		"the credential in an error": {&stub{Provider: mock.New(), name: "mock", issueErr: func(int) error {
			return &einvoice.PermanentError{Code: "x", Message: "bad key s3cret-key"}
		}}, "errors-never-leak-credentials"},
		"a sandbox document accepted by a real adapter":     {&stub{Provider: mock.New(), name: "real"}, "sandbox-documents-refused"},
		"a cancelled context taken for a refusal":           {&stub{Provider: mock.New(), name: "mock", issueErr: permanent}, "cancelled-context"},
		"two callers racing on one UUID make two documents": {&stub{Provider: mock.New(), name: "mock", track: true, doubleIssue: true}, "issue-concurrent-is-one-document"},
		"a credit note that is the invoice again":           {&stub{Provider: mock.New(), name: "mock", creditIsOriginal: true}, "credit-note"},
		"a cancel of a document nobody issued succeeds":     {&stub{Provider: mock.New(), name: "mock", cancelNothing: true}, "cancel-unknown-is-not-found"},
	} {
		t.Run(name, func(t *testing.T) {
			got := failedChecks(t, c.s, "s3cret-key", true)
			if len(got[c.want]) == 0 {
				t.Errorf("check %q found nothing; failed checks: %v", c.want, got)
			}
		})
	}
	if msgs := failedChecks(t, &stub{Provider: mock.New(), name: "real"}, "", false)["sandbox-documents-refused"]; len(msgs) == 0 || !strings.Contains(msgs[0], "sandbox") {
		t.Errorf("sandbox message: %v", msgs)
	}
}

// The three checks added for plan 09's checklist hold for an adapter that tracks its documents and is idempotent, and the reference adapter still passes them.
func TestAnIdempotentAdapterPassesTheConcurrencyAndCreditNoteChecks(t *testing.T) {
	got := failedChecks(t, &stub{Provider: mock.New(), name: "mock", track: true}, "", false)
	if len(got) != 0 {
		t.Fatalf("an idempotent adapter fails: %v", got)
	}
}
