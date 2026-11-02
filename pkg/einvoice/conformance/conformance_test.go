package conformance

import (
	"context"
	"errors"
	"strings"
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
	if s.issue != nil {
		res = s.issue(res, int(s.calls.Add(1)))
	}
	return res, err
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
	h := Harness{New: func(testing.TB) einvoice.Provider { return s }, Secret: secret}
	if wire {
		h.Fail = func(st int) { s.fails.Store(int64(st)) }
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
		"a sandbox document accepted by a real adapter": {&stub{Provider: mock.New(), name: "real"}, "sandbox-documents-refused"},
		"a cancelled context taken for a refusal":       {&stub{Provider: mock.New(), name: "mock", issueErr: permanent}, "cancelled-context"},
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
