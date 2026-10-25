// Package nilvera is the adapter for Nilvera, a Turkish e-fatura / e-arşiv integrator (https://developer.nilvera.com).
//
// It was written from Nilvera's public API reference (read 2026-10) and has NEVER been run against a Nilvera account. Everything the reference does not
// state, or states loosely, is marked UNVERIFIED below. The tests use httptest servers that replay the documented shapes.
//
// Settings: {"api_key": "…", "mode": "test" | "production", "base_url": "https://… (optional override)"}. The key is sent as "Authorization: Bearer".
// Test and production keys are separate at Nilvera, so mode picks the host: apitest.nilvera.com or api.nilvera.com.
package nilvera

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/safehttp"
)

// Name is the registry name.
const Name = "nilvera"

const (
	testURL       = "https://apitest.nilvera.com"
	productionURL = "https://api.nilvera.com"
	maxBody       = 32 << 20
	maxMessage    = 300
)

// Options are set by the deployment, never by a tenant.
type Options struct {
	// AllowPrivate lets base_url be http:// and private (tests only).
	AllowPrivate bool
	// Timeout of one request (default 30 s).
	Timeout time.Duration
}

// Register adds the adapter to r.
func Register(r *einvoice.Registry) { RegisterWithOptions(r, Options{}) }

// RegisterWithOptions adds the adapter to r.
func RegisterWithOptions(r *einvoice.Registry, o Options) {
	r.Register(Name, func(settings []byte) (einvoice.Provider, error) { return New(settings, o) })
}

type settings struct {
	APIKey  string `json:"api_key"`
	Mode    string `json:"mode"`
	BaseURL string `json:"base_url"`
}

// Provider talks to Nilvera.
type Provider struct {
	base   string
	key    string
	client *http.Client
}

// New validates the settings. The API key is never part of an error.
func New(raw []byte, o Options) (*Provider, error) {
	var s settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, errors.New("nilvera: settings must be a JSON object")
	}
	if strings.TrimSpace(s.APIKey) == "" {
		return nil, errors.New("nilvera: api_key is required")
	}
	base := productionURL
	switch s.Mode {
	case "production", "":
	case "test":
		base = testURL
	default:
		return nil, errors.New(`nilvera: mode must be "test" or "production"`)
	}
	if s.BaseURL != "" {
		if err := safehttp.ValidateURL(s.BaseURL, o.AllowPrivate); err != nil {
			return nil, fmt.Errorf("nilvera: base_url %w", err)
		}
		base = strings.TrimRight(s.BaseURL, "/")
	}
	t := o.Timeout
	if t <= 0 {
		t = 30 * time.Second
	}
	return &Provider{base: base, key: s.APIKey, client: safehttp.NewClient(t, o.AllowPrivate)}, nil
}

// Capabilities implements einvoice.Provider. Nilvera is Turkey only; e-fatura needs to know whether the buyer is registered.
func (*Provider) Capabilities() einvoice.Capabilities {
	return einvoice.Capabilities{Name: Name, Countries: []string{"TR"}, Types: []einvoice.DocumentType{einvoice.TypeEFatura, einvoice.TypeEArsiv},
		NeedsBuyerCheck: true, CanCancel: true, ReturnsPDF: true}
}

// family is the API family of a document type: "einvoice/Sale" for e-fatura, "earchive/Invoices" for e-arşiv.
func family(t einvoice.DocumentType) (string, error) {
	switch t {
	case einvoice.TypeEFatura:
		return "einvoice/Sale", nil // UNVERIFIED: outgoing e-fatura status/pdf live under Sale/{UUID} per the reference
	case einvoice.TypeEArsiv:
		return "earchive/Invoices", nil
	}
	return "", &einvoice.PermanentError{Code: "unsupported_type", Message: "nilvera sends e-fatura and e-arsiv only"}
}

func (p *Provider) redact(s string) string {
	if p.key != "" {
		s = strings.ReplaceAll(s, p.key, "[redacted]")
	}
	return s
}

// do sends a request and maps failures: it returns the body of a 2xx, errDuplicate for 409, a PermanentError or a TransientError otherwise.
func (p *Provider) do(ctx context.Context, method, path string, query url.Values, contentType string, body []byte) ([]byte, error) {
	u := p.base + "/" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return nil, &einvoice.PermanentError{Code: "request", Message: "could not build the request"}
	}
	req.Header.Set("Authorization", "Bearer "+p.key)
	req.Header.Set("Accept", "application/json, application/pdf, application/xml, */*")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, &einvoice.TransientError{Err: errors.New("nilvera could not be reached")}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, &einvoice.TransientError{Err: errors.New("reading the answer failed")}
	}
	switch c := resp.StatusCode; {
	case c >= 200 && c <= 299:
		return raw, nil
	case c == http.StatusConflict:
		return nil, errDuplicate
	case c == http.StatusNotFound && method == http.MethodGet:
		return nil, einvoice.ErrNotFound
	case c == http.StatusRequestTimeout, c == http.StatusTooManyRequests, c >= 500:
		return nil, &einvoice.TransientError{Err: fmt.Errorf("nilvera answered %d", c)}
	default:
		// 400, 401, 403, 404, 422 and the rest: retrying changes nothing. UNVERIFIED: the error body's shape is not documented, so the text is taken as it comes.
		return nil, &einvoice.PermanentError{Code: fmt.Sprintf("http_%d", c), Message: p.message(raw, c)}
	}
}

var errDuplicate = errors.New("nilvera: the document already exists")

// message extracts a short human text from an error body without ever returning the key.
func (p *Provider) message(raw []byte, code int) string {
	var m map[string]any
	text := ""
	if json.Unmarshal(raw, &m) == nil {
		for _, k := range []string{"Message", "message", "Detail", "detail", "Title", "title", "error", "Error"} {
			if s, ok := m[k].(string); ok && s != "" {
				text = s
				break
			}
		}
	}
	if text == "" {
		text = strings.TrimSpace(string(raw))
	}
	if text == "" {
		text = fmt.Sprintf("nilvera answered %d", code)
	}
	text = p.redact(text)
	if len(text) > maxMessage {
		text = strings.ToValidUTF8(text[:maxMessage], "")
	}
	return text
}

type sendAnswer struct {
	UUID          string  `json:"UUID"`
	InvoiceNumber *string `json:"InvoiceNumber"`
}

// Issue implements einvoice.Provider: upload the UBL as the "file" part. A 409 (the UUID exists) is answered by the document's status, never a second send.
func (p *Provider) Issue(ctx context.Context, r einvoice.IssueRequest) (einvoice.IssueResult, error) {
	d := r.Doc
	_, err := family(d.Type)
	if err != nil {
		return einvoice.IssueResult{}, err
	}
	if d.Sandbox {
		return einvoice.IssueResult{}, &einvoice.PermanentError{Code: "sandbox", Message: "sandbox documents are issued by the mock provider only"}
	}
	path := "einvoice/Send/Xml"
	query := url.Values{}
	if d.Type == einvoice.TypeEArsiv {
		path = "earchive/Send/Xml"
	} else if d.Buyer.Alias != "" {
		query.Set("Alias", d.Buyer.Alias)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s.xml"`, d.UUID))
	h.Set("Content-Type", "application/xml")
	part, _ := mw.CreatePart(h)
	_, _ = part.Write(r.UBL)
	_ = mw.Close()

	ref := einvoice.Ref{UUID: d.UUID, ExternalID: d.UUID, Type: d.Type}
	raw, err := p.do(ctx, http.MethodPost, path, query, mw.FormDataContentType(), buf.Bytes())
	if errors.Is(err, errDuplicate) {
		st, serr := p.Status(ctx, ref)
		if serr != nil {
			return einvoice.IssueResult{}, serr
		}
		// UNVERIFIED: the status endpoints do not return the invoice number, so a repeat after a lost answer has none. The worker must have recorded the first answer.
		return einvoice.IssueResult{Ref: ref, LegalNumber: st.LegalNumber, State: st.State}, nil
	}
	if err != nil {
		return einvoice.IssueResult{}, err
	}
	var a sendAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		return einvoice.IssueResult{}, &einvoice.PermanentError{Code: "bad_response", Message: "nilvera answered with something that is not JSON"}
	}
	res := einvoice.IssueResult{Ref: ref, State: einvoice.StatePending}
	if a.UUID != "" {
		res.ExternalID = a.UUID
	}
	if a.InvoiceNumber != nil && *a.InvoiceNumber != "" {
		// UNVERIFIED: that InvoiceNumber is the legal number (series + year + sequence) assigned at send time, and that "sent" equals "issued" for us.
		res.LegalNumber, res.State = *a.InvoiceNumber, einvoice.StateIssued
	}
	return res, nil
}

type efaturaStatus struct {
	Answer struct {
		AnswerCode string `json:"AnswerCode"`
	} `json:"Answer"`
	InvoiceStatus struct {
		Code              string  `json:"Code"`
		Description       *string `json:"Description"`
		DetailDescription *string `json:"DetailDescription"`
	} `json:"InvoiceStatus"`
}

type earsivStatus struct {
	StatusDetail *string `json:"StatusDetail"`
	StatusCode   string  `json:"StatusCode"`
	CancelStatus bool    `json:"CancelStatus"`
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Status implements einvoice.Provider.
func (p *Provider) Status(ctx context.Context, ref einvoice.Ref) (einvoice.StatusResult, error) {
	fam, err := family(ref.Type)
	if err != nil {
		return einvoice.StatusResult{}, err
	}
	raw, err := p.do(ctx, http.MethodGet, fam+"/"+url.PathEscape(ref.UUID)+"/Status", nil, "", nil)
	if err != nil {
		return einvoice.StatusResult{}, err
	}
	if ref.Type == einvoice.TypeEArsiv {
		var s earsivStatus
		if json.Unmarshal(raw, &s) != nil {
			return einvoice.StatusResult{}, &einvoice.PermanentError{Code: "bad_response", Message: "unreadable status"}
		}
		st := mapCode(s.StatusCode)
		if s.CancelStatus {
			st = einvoice.StateCancelled
		}
		return einvoice.StatusResult{State: st, Detail: p.redact(deref(s.StatusDetail))}, nil
	}
	var s efaturaStatus
	if json.Unmarshal(raw, &s) != nil {
		return einvoice.StatusResult{}, &einvoice.PermanentError{Code: "bad_response", Message: "unreadable status"}
	}
	st := mapCode(s.InvoiceStatus.Code)
	if s.Answer.AnswerCode == "rejected" { // the buyer refused it
		st = einvoice.StateRejected
	}
	detail := deref(s.InvoiceStatus.DetailDescription)
	if detail == "" {
		detail = deref(s.InvoiceStatus.Description)
	}
	return einvoice.StatusResult{State: st, Detail: p.redact(detail)}, nil
}

func mapCode(c string) einvoice.State {
	switch c {
	case "succeed":
		return einvoice.StateIssued
	case "error":
		return einvoice.StateRejected
	}
	return einvoice.StatePending // waiting, unknown
}

// Cancel implements einvoice.Provider. Only e-arşiv can be cancelled by the issuer; an e-fatura is reversed by a credit note.
func (p *Provider) Cancel(ctx context.Context, ref einvoice.Ref, _ string) error {
	if ref.Type != einvoice.TypeEArsiv {
		return einvoice.ErrUnsupported
	}
	// The cancel call takes no body, so the reason stays with Payrudder.
	_, err := p.do(ctx, http.MethodPut, "earchive/Invoices/"+url.PathEscape(ref.UUID)+"/Cancel", nil, "", nil)
	return err
}

// FetchPDF implements einvoice.Provider.
func (p *Provider) FetchPDF(ctx context.Context, ref einvoice.Ref) ([]byte, error) {
	fam, err := family(ref.Type)
	if err != nil {
		return nil, err
	}
	// UNVERIFIED: the e-fatura reference says "downloads the PDF" without a content type (raw bytes assumed); the e-arşiv path is assumed to mirror it.
	return p.do(ctx, http.MethodGet, fam+"/"+url.PathEscape(ref.UUID)+"/pdf", nil, "", nil)
}

// FetchUBL implements einvoice.Provider.
func (p *Provider) FetchUBL(ctx context.Context, ref einvoice.Ref) ([]byte, error) {
	fam, err := family(ref.Type)
	if err != nil {
		return nil, err
	}
	// UNVERIFIED: the XML download path (/xml) was not read in the reference; it is assumed to mirror /pdf.
	return p.do(ctx, http.MethodGet, fam+"/"+url.PathEscape(ref.UUID)+"/xml", nil, "", nil)
}

type company struct {
	TaxNumber string `json:"TaxNumber"`
	Name      string `json:"Name"`
	Title     string `json:"Title"`
	Type      string `json:"Type"`
}

// IsEInvoiceUser implements einvoice.BuyerChecker with GET /general/GlobalCompany/Check/TaxNumber/{vkn}. An empty array means the tax id is not a registered
// e-fatura user.
func (p *Provider) IsEInvoiceUser(ctx context.Context, taxID string) (bool, string, error) {
	q := url.Values{"globalUserType": {"Invoice"}}
	raw, err := p.do(ctx, http.MethodGet, "general/GlobalCompany/Check/TaxNumber/"+url.PathEscape(taxID), q, "", nil)
	if errors.Is(err, einvoice.ErrNotFound) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	var cs []company
	if json.Unmarshal(raw, &cs) != nil {
		return false, "", &einvoice.PermanentError{Code: "bad_response", Message: "unreadable answer"}
	}
	if len(cs) == 0 {
		return false, "", nil
	}
	// UNVERIFIED: that "Name" holds the mailbox alias (urn:mail:…) the reference does not say; "Title" is the company's name.
	return true, cs[0].Name, nil
}
