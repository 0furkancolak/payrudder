package webhook

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/0furkancolak/payrudder/pkg/brand"
	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/safehttp"
	"github.com/0furkancolak/payrudder/pkg/strx"
	"github.com/0furkancolak/payrudder/pkg/webhooksig"
)

// Name is the registry name.
const Name = "webhook"

const (
	maxResponse  = 32 << 20
	maxMessage   = 300
	defaultTimes = 30 * time.Second
)

// Options are set by the deployment, never by a tenant.
type Options struct {
	// AllowPrivate lets a tenant's settings ask for http:// and private addresses ("allow_private": true). Development and tests only.
	AllowPrivate bool
	// Timeout of one request (default 30 s).
	Timeout time.Duration
}

// Register adds the webhook provider to r with production options.
func Register(r *einvoice.Registry) { RegisterWithOptions(r, Options{}) }

// RegisterWithOptions adds the webhook provider to r.
func RegisterWithOptions(r *einvoice.Registry, o Options) {
	r.Register(Name, func(settings []byte) (einvoice.Provider, error) { return New(settings, o) })
}

type settings struct {
	URL          string `json:"url"`
	Secret       string `json:"secret"`
	AllowPrivate bool   `json:"allow_private"`
}

// Provider posts documents to a tenant's endpoint.
type Provider struct {
	url    string
	secret string
	client *http.Client
	now    func() time.Time
}

// New validates settings and returns a provider. The secret is never part of an error.
func New(raw []byte, o Options) (*Provider, error) {
	var s settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, errors.New("webhook: settings must be a JSON object")
	}
	allow := o.AllowPrivate && s.AllowPrivate
	if err := safehttp.ValidateURL(s.URL, allow); err != nil {
		return nil, fmt.Errorf("webhook: url %w", err)
	}
	if len(s.Secret) < 8 {
		return nil, errors.New("webhook: secret must be at least 8 characters")
	}
	t := o.Timeout
	if t <= 0 {
		t = defaultTimes
	}
	return &Provider{url: s.URL, secret: s.Secret, client: safehttp.NewClient(t, allow), now: time.Now}, nil
}

// Capabilities implements einvoice.Provider. What the endpoint really supports is its business; cancel and PDF are asked for and answered 501 when not.
func (*Provider) Capabilities() einvoice.Capabilities {
	return einvoice.Capabilities{Name: Name, Types: []einvoice.DocumentType{einvoice.TypeEFatura, einvoice.TypeEArsiv, einvoice.TypePeppol}, CanCancel: true, ReturnsPDF: true}
}

type answer struct {
	State       string `json:"state"`
	LegalNumber string `json:"legal_number"`
	ExternalID  string `json:"external_id"`
	PDFBase64   string `json:"pdf_base64"`
	UBLBase64   string `json:"ubl_base64"`
	Message     string `json:"message"`
}

type actionBody struct {
	Action     string `json:"action"`
	UUID       string `json:"uuid"`
	ExternalID string `json:"external_id,omitempty"`
	Type       string `json:"type,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

func (p *Provider) post(ctx context.Context, action, contentType string, body []byte, hdr map[string]string) (answer, error) {
	u, err := url.Parse(p.url)
	if err != nil {
		return answer{}, &einvoice.PermanentError{Code: "webhook.url", Message: "invalid url"}
	}
	q := u.Query()
	q.Set("action", action)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return answer{}, &einvoice.PermanentError{Code: "webhook.url", Message: "invalid url"}
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	req.Header.Set(brand.Header("Action"), action)
	for k, v := range hdr {
		req.Header.Set(brand.Header(k), v)
	}
	req.Header.Set(webhooksig.Header(), webhooksig.Sign(p.now(), body, p.secret))
	resp, err := p.client.Do(req)
	if err != nil {
		// The error of net/http carries the URL, which may carry a token in its query: report only the kind of failure.
		return answer{}, &einvoice.TransientError{Err: errors.New("the endpoint could not be reached")}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return answer{}, &einvoice.TransientError{Err: errors.New("reading the answer failed")}
	}
	var a answer
	jsonOK := json.Unmarshal(raw, &a) == nil
	msg := truncate(a.Message)
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return answer{}, einvoice.ErrNotFound
	case resp.StatusCode == http.StatusNotImplemented:
		return answer{}, einvoice.ErrUnsupported
	case resp.StatusCode == http.StatusRequestTimeout, resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500:
		return answer{}, &einvoice.TransientError{Err: fmt.Errorf("the endpoint answered %d", resp.StatusCode)}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return answer{}, &einvoice.PermanentError{Code: fmt.Sprintf("http_%d", resp.StatusCode), Message: cmp.Or(msg, fmt.Sprintf("the endpoint answered %d", resp.StatusCode))}
	case !jsonOK:
		return answer{}, &einvoice.PermanentError{Code: "bad_response", Message: "the endpoint did not answer with JSON"}
	}
	a.Message = msg
	return a, nil
}

func truncate(s string) string {
	return strx.Truncate(strings.TrimSpace(s), maxMessage)
}

func toState(s string) (einvoice.State, bool) {
	switch s {
	case "issued":
		return einvoice.StateIssued, true
	case "pending":
		return einvoice.StatePending, true
	case "rejected":
		return einvoice.StateRejected, true
	case "cancelled":
		return einvoice.StateCancelled, true
	}
	return "", false
}

// Issue implements einvoice.Provider.
func (p *Provider) Issue(ctx context.Context, r einvoice.IssueRequest) (einvoice.IssueResult, error) {
	d := r.Doc
	a, err := p.post(ctx, "issue", "application/xml", r.UBL, map[string]string{
		"Document-Uuid": d.UUID, "Document-Kind": string(d.Kind), "Document-Type": string(d.Type), "Document-Number": d.Number})
	if err != nil {
		return einvoice.IssueResult{}, err
	}
	st, ok := toState(a.State)
	if !ok || st == einvoice.StateCancelled {
		return einvoice.IssueResult{}, &einvoice.PermanentError{Code: "bad_response", Message: "the endpoint answered with an unknown state"}
	}
	if st == einvoice.StateRejected {
		return einvoice.IssueResult{}, &einvoice.PermanentError{Code: "rejected", Message: cmp.Or(a.Message, "the endpoint rejected the document")}
	}
	if st == einvoice.StateIssued && a.LegalNumber == "" {
		return einvoice.IssueResult{}, &einvoice.PermanentError{Code: "bad_response", Message: "an issued document needs a legal_number"}
	}
	res := einvoice.IssueResult{Ref: einvoice.Ref{ExternalID: a.ExternalID, UUID: d.UUID, Type: d.Type}, LegalNumber: a.LegalNumber, State: st}
	if a.PDFBase64 != "" {
		if res.PDF, err = base64.StdEncoding.DecodeString(a.PDFBase64); err != nil {
			return einvoice.IssueResult{}, &einvoice.PermanentError{Code: "bad_response", Message: "pdf_base64 is not base64"}
		}
	}
	return res, nil
}

func (p *Provider) action(ctx context.Context, action string, ref einvoice.Ref, reason string) (answer, error) {
	b, _ := json.Marshal(actionBody{Action: action, UUID: ref.UUID, ExternalID: ref.ExternalID, Type: string(ref.Type), Reason: reason})
	return p.post(ctx, action, "application/json", b, map[string]string{"Document-Uuid": ref.UUID, "Document-Type": string(ref.Type)})
}

// Status implements einvoice.Provider.
func (p *Provider) Status(ctx context.Context, ref einvoice.Ref) (einvoice.StatusResult, error) {
	a, err := p.action(ctx, "status", ref, "")
	if err != nil {
		return einvoice.StatusResult{}, err
	}
	st, ok := toState(a.State)
	if !ok {
		return einvoice.StatusResult{}, &einvoice.PermanentError{Code: "bad_response", Message: "the endpoint answered with an unknown state"}
	}
	return einvoice.StatusResult{State: st, LegalNumber: a.LegalNumber, Detail: a.Message}, nil
}

// Cancel implements einvoice.Provider.
func (p *Provider) Cancel(ctx context.Context, ref einvoice.Ref, reason string) error {
	a, err := p.action(ctx, "cancel", ref, reason)
	if err != nil {
		return err
	}
	if a.State != "cancelled" {
		return &einvoice.PermanentError{Code: "not_cancelled", Message: cmp.Or(a.Message, "the endpoint did not cancel the document")}
	}
	return nil
}

func (p *Provider) blob(ctx context.Context, action string, ref einvoice.Ref, pick func(answer) string) ([]byte, error) {
	a, err := p.action(ctx, action, ref, "")
	if err != nil {
		return nil, err
	}
	s := pick(a)
	if s == "" {
		return nil, einvoice.ErrUnsupported
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, &einvoice.PermanentError{Code: "bad_response", Message: "the answer is not base64"}
	}
	return b, nil
}

// FetchPDF implements einvoice.Provider.
func (p *Provider) FetchPDF(ctx context.Context, ref einvoice.Ref) ([]byte, error) {
	return p.blob(ctx, "pdf", ref, func(a answer) string { return a.PDFBase64 })
}

// FetchUBL implements einvoice.Provider.
func (p *Provider) FetchUBL(ctx context.Context, ref einvoice.Ref) ([]byte, error) {
	return p.blob(ctx, "ubl", ref, func(a answer) string { return a.UBLBase64 })
}
