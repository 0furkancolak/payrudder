package httpapi

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/mail"
	"strings"

	"google.golang.org/grpc/codes"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// The invoice inbox: invoices a tenant receives by mail are forwarded to invoices+<token>@<inbound domain>; the mail provider posts each mail to /inbound/invoices and
// the attachments wait, as items to review, until the console opens them and reads them in the browser. The gateway and billing-svc only keep the bytes.

// inboundBodyLimit is the largest inbound mail body: three MiB of attachments, base64 encoded, and the headers.
const inboundBodyLimit = 6 << 20

// inboxRoutes is the table for the invoice inbox and the recurring-vendor rules.
func (s *Server) inboxRoutes() []Route {
	return []Route{
		{"GET", "/v1/expense-inbox", AccessTenant, ratelimit.ClassRead, s.getInbox},
		{"POST", "/v1/expense-inbox/rotate", AccessTenant, ratelimit.ClassWrite, s.rotateInbox},
		{"GET", "/v1/expense-inbox/items", AccessTenant, ratelimit.ClassRead, s.listInboxItems},
		{"GET", "/v1/expense-inbox/items/{id}/file", AccessTenant, ratelimit.ClassRead, s.inboxItemFile},
		{"POST", "/v1/expense-inbox/items/{id}/resolve", AccessTenant, ratelimit.ClassWrite, s.resolveInboxItem},
		{"GET", "/v1/expense-rules", AccessTenant, ratelimit.ClassRead, s.listExpenseRules},
		{"POST", "/v1/expense-rules", AccessTenant, ratelimit.ClassWrite, s.createExpenseRule},
		{"PUT", "/v1/expense-rules/{id}", AccessTenant, ratelimit.ClassWrite, s.updateExpenseRule},
		{"DELETE", "/v1/expense-rules/{id}", AccessTenant, ratelimit.ClassWrite, s.deleteExpenseRule},
		{"POST", "/inbound/invoices", AccessPublic, "", s.inboundInvoices},
	}
}

// inboxAddress is the address the tenant forwards invoices to; empty when no inbound domain is configured.
func (s *Server) inboxAddress(token string) string {
	if s.d.InboundDomain == "" {
		return ""
	}
	return "invoices+" + token + "@" + s.d.InboundDomain
}

func (s *Server) inboxAnswer(w http.ResponseWriter, r *http.Request, in *billingv1.ExpenseInbox, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusOK, map[string]string{"token": in.GetToken(), "app_id": in.GetAppId(), "address": s.inboxAddress(in.GetToken())})
}

// getInbox is GET /v1/expense-inbox?app_id=: the address of the tenant's (or an app's) invoice inbox.
func (s *Server) getInbox(w http.ResponseWriter, r *http.Request) {
	in, err := s.d.Billing.GetExpenseInbox(r.Context(), &billingv1.GetExpenseInboxRequest{AppId: r.URL.Query().Get("app_id")})
	s.inboxAnswer(w, r, in, err)
}

// rotateInbox is POST /v1/expense-inbox/rotate?app_id=: a new address; mail to the old one is dropped.
func (s *Server) rotateInbox(w http.ResponseWriter, r *http.Request) {
	in, err := s.d.Billing.RotateExpenseInbox(r.Context(), &billingv1.GetExpenseInboxRequest{AppId: r.URL.Query().Get("app_id")})
	s.inboxAnswer(w, r, in, err)
}

// listInboxItems is GET /v1/expense-inbox/items?status=&page_size=.
func (s *Server) listInboxItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	size, err := rangedParam(q, "page_size", 1, 200, "must be 1 to 200")
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Billing.ListInboxItems(r.Context(), &billingv1.ListInboxItemsRequest{Status: q.Get("status"), PageSize: size})
	respond(w, r, http.StatusOK, resp, err)
}

// inboxItemFile is GET /v1/expense-inbox/items/{id}/file: the received attachment, as it came.
func (s *Server) inboxItemFile(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.GetInboxItemFile(r.Context(), &billingv1.GetInboxItemFileRequest{Id: r.PathValue("id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", resp.GetContentType())
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(resp.GetFilename())+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(resp.GetData())
}

// resolveInboxItem is POST /v1/expense-inbox/items/{id}/resolve {"status": "accepted"|"dismissed", "expense_id": "..."}.
func (s *Server) resolveInboxItem(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.ResolveInboxItemRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Id = r.PathValue("id")
	resp, err := s.d.Billing.ResolveInboxItem(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetItem(), err)
}

// listExpenseRules is GET /v1/expense-rules.
func (s *Server) listExpenseRules(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.ListExpenseRules(r.Context(), &billingv1.ListExpenseRulesRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) saveExpenseRule(w http.ResponseWriter, r *http.Request, id string, code int) {
	rule := &billingv1.ExpenseRule{}
	if err := decode(r, rule); err != nil {
		writeError(w, r, err)
		return
	}
	rule.Id = id
	resp, err := s.d.Billing.SaveExpenseRule(r.Context(), &billingv1.SaveExpenseRuleRequest{Rule: rule})
	respond(w, r, code, resp.GetRule(), err)
}

// createExpenseRule is POST /v1/expense-rules.
func (s *Server) createExpenseRule(w http.ResponseWriter, r *http.Request) {
	s.saveExpenseRule(w, r, "", http.StatusCreated)
}

// updateExpenseRule is PUT /v1/expense-rules/{id}: the body replaces the rule.
func (s *Server) updateExpenseRule(w http.ResponseWriter, r *http.Request) {
	s.saveExpenseRule(w, r, r.PathValue("id"), http.StatusOK)
}

// deleteExpenseRule is DELETE /v1/expense-rules/{id}.
func (s *Server) deleteExpenseRule(w http.ResponseWriter, r *http.Request) {
	_, err := s.d.Billing.DeleteExpenseRule(r.Context(), &billingv1.DeleteExpenseRuleRequest{Id: r.PathValue("id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// inboundMail is the part of a mail webhook this gateway reads. It takes the shape of the inbound mail webhooks of Resend and Nafru: the mail is the body, or the
// "data" of an {"type": "email.received", "data": {...}} envelope; attachments carry their bytes base64 encoded in "content".
type inboundMail struct {
	From        string `json:"from"`
	To          any    `json:"to"`
	Subject     string `json:"subject"`
	Attachments []struct {
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		Content     string `json:"content"`
	} `json:"attachments"`
	Data *inboundMail `json:"data"`
}

// recipients lists the addresses of the "to" field, which a provider sends as a string, a list of strings or a list of {"email": ...} objects.
func recipients(to any) []string {
	var out []string
	add := func(s string) {
		if a, err := mail.ParseAddress(s); err == nil {
			out = append(out, a.Address)
		} else if strings.Contains(s, "@") {
			out = append(out, strings.TrimSpace(s))
		}
	}
	switch v := to.(type) {
	case string:
		for _, p := range strings.Split(v, ",") {
			add(p)
		}
	case []any:
		for _, e := range v {
			switch x := e.(type) {
			case string:
				add(x)
			case map[string]any:
				if s, ok := x["email"].(string); ok {
					add(s)
				} else if s, ok := x["address"].(string); ok {
					add(s)
				}
			}
		}
	}
	return out
}

// inboxTokens returns the tokens in invoices+<token>@domain addresses.
func inboxTokens(addrs []string) []string {
	var out []string
	for _, a := range addrs {
		local, _, ok := strings.Cut(strings.ToLower(a), "@")
		if !ok {
			continue
		}
		if tok, ok := strings.CutPrefix(local, "invoices+"); ok && tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

// inboundAuthorized checks the shared secret the mail provider's webhook is configured with: "Authorization: Bearer <secret>", "X-Inbound-Secret" or ?secret=.
func (s *Server) inboundAuthorized(r *http.Request) bool {
	want := s.d.InboundSecret
	if want == "" {
		return false
	}
	got := r.URL.Query().Get("secret")
	if h := r.Header.Get("X-Inbound-Secret"); h != "" {
		got = h
	}
	if h, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		got = h
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// inboundInvoices is POST /inbound/invoices: a forwarded mail from the provider. 404 until PAYRUDDER_INBOUND_SECRET is set, 401 for a wrong secret. A mail to an unknown
// address answers 200 like any other, so the endpoint tells nothing about which addresses exist.
func (s *Server) inboundInvoices(w http.ResponseWriter, r *http.Request) {
	if s.d.InboundSecret == "" {
		http.NotFound(w, r)
		return
	}
	if !s.hostedAllowed(w, r) {
		return
	}
	if !s.inboundAuthorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var m inboundMail
	if err := decodeBody(r, &m, errkey.New(codes.InvalidArgument, "gateway.request.malformed_json", "the body must be the JSON of a received mail")); err != nil {
		writeError(w, r, err)
		return
	}
	if m.Data != nil {
		m = *m.Data
	}
	req := &billingv1.ReceiveInboxMailRequest{From: m.From, Subject: m.Subject}
	for _, a := range m.Attachments {
		data, err := base64.StdEncoding.DecodeString(a.Content)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(a.Content)
		}
		if err != nil || len(data) == 0 {
			continue
		}
		req.Attachments = append(req.Attachments, &billingv1.InboxAttachment{Filename: a.Filename, ContentType: a.ContentType, Data: data})
	}
	stored := int32(0)
	seen := map[string]bool{}
	for _, tok := range inboxTokens(recipients(m.To)) {
		if seen[tok] {
			continue
		}
		seen[tok] = true
		req.Token = tok
		resp, err := s.d.Billing.ReceiveInboxMail(r.Context(), req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		stored += resp.GetStored()
	}
	writeJSONValue(w, http.StatusOK, map[string]int32{"stored": stored})
}
