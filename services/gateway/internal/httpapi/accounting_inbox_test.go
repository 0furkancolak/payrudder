package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
)

var (
	inboxMu   sync.Mutex
	inboxMail []*billingv1.ReceiveInboxMailRequest
)

func (fakeBilling) ReceiveInboxMail(_ context.Context, req *billingv1.ReceiveInboxMailRequest) (*billingv1.ReceiveInboxMailResponse, error) {
	inboxMu.Lock()
	defer inboxMu.Unlock()
	inboxMail = append(inboxMail, req)
	if req.GetToken() == "unknowntokenunknowntoken" {
		return &billingv1.ReceiveInboxMailResponse{Skipped: int32(len(req.GetAttachments()))}, nil
	}
	return &billingv1.ReceiveInboxMailResponse{Stored: int32(len(req.GetAttachments()))}, nil
}

func (fakeBilling) GetExpenseInbox(_ context.Context, req *billingv1.GetExpenseInboxRequest) (*billingv1.ExpenseInbox, error) {
	return &billingv1.ExpenseInbox{Token: "abcdefghijklmnopqrstuvwxyz234567", AppId: req.GetAppId()}, nil
}

func (fakeBilling) ListExpenseRules(context.Context, *billingv1.ListExpenseRulesRequest) (*billingv1.ListExpenseRulesResponse, error) {
	return &billingv1.ListExpenseRulesResponse{Rules: []*billingv1.ExpenseRule{{Id: "r1", VendorMatch: "AWS", Category: "infrastructure", Currency: "USD", ExpectedAmountMinor: 4900, ToleranceBps: 500, AutoApprove: true}}}, nil
}

func (fakeBilling) SaveExpenseRule(_ context.Context, req *billingv1.SaveExpenseRuleRequest) (*billingv1.SaveExpenseRuleResponse, error) {
	r := req.GetRule()
	if r.GetId() == "" {
		r.Id = "new-rule"
	}
	return &billingv1.SaveExpenseRuleResponse{Rule: r}, nil
}

func inbound(t *testing.T, ts *httptest.Server, hdr map[string]string, query, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/inbound/invoices"+query, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp, m
}

func TestInboundInvoices(t *testing.T) {
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 invoice"))
	mail := `{"type":"email.received","data":{"from":"Billing <billing@vendor.test>","to":["Invoices <invoices+abcdefghijklmnopqrstuvwxyz234567@in.example.test>"],"subject":"Your invoice","attachments":[{"filename":"inv.pdf","content_type":"application/pdf","content":"` + pdf + `"}]}}`

	// Without a configured secret the endpoint does not exist.
	off, _, _, _ := newTestServerWith(t, nil)
	if resp, _ := inbound(t, off, nil, "", mail); resp.StatusCode != 404 {
		t.Fatalf("no secret configured: %d", resp.StatusCode)
	}

	ts, _, _, _ := newTestServerWith(t, func(d *Deps) { d.InboundSecret, d.InboundDomain = "s3cret", "in.example.test" })
	if resp, _ := inbound(t, ts, nil, "", mail); resp.StatusCode != 401 {
		t.Fatalf("no secret: %d", resp.StatusCode)
	}
	if resp, _ := inbound(t, ts, map[string]string{"Authorization": "Bearer wrong"}, "", mail); resp.StatusCode != 401 {
		t.Fatalf("wrong secret: %d", resp.StatusCode)
	}
	inboxMu.Lock()
	inboxMail = nil
	inboxMu.Unlock()
	resp, m := inbound(t, ts, map[string]string{"Authorization": "Bearer s3cret"}, "", mail)
	if resp.StatusCode != 200 || m["stored"] != float64(1) {
		t.Fatalf("mail: %d %v", resp.StatusCode, m)
	}
	inboxMu.Lock()
	got := inboxMail
	inboxMu.Unlock()
	if len(got) != 1 || got[0].GetToken() != "abcdefghijklmnopqrstuvwxyz234567" || got[0].GetSubject() != "Your invoice" ||
		string(got[0].GetAttachments()[0].GetData()) != "%PDF-1.4 invoice" || got[0].GetAttachments()[0].GetFilename() != "inv.pdf" {
		t.Fatalf("what billing received: %v", got)
	}

	// The flat shape, a string recipient, the secret in the query, and an unknown address (still 200).
	flat := `{"from":"a@b.test","to":"invoices+unknowntokenunknowntoken@in.example.test","subject":"x","attachments":[{"filename":"a.pdf","content":"` + pdf + `"}]}`
	resp, m = inbound(t, ts, nil, "?secret=s3cret", flat)
	if resp.StatusCode != 200 || m["stored"] != float64(0) {
		t.Fatalf("unknown address: %d %v", resp.StatusCode, m)
	}
	// A mail with no inbox address stores nothing and asks billing nothing.
	inboxMu.Lock()
	inboxMail = nil
	inboxMu.Unlock()
	resp, m = inbound(t, ts, map[string]string{"X-Inbound-Secret": "s3cret"}, "", `{"to":["someone@elsewhere.test"],"attachments":[{"filename":"a.pdf","content":"`+pdf+`"}]}`)
	inboxMu.Lock()
	n := len(inboxMail)
	inboxMu.Unlock()
	if resp.StatusCode != 200 || m["stored"] != float64(0) || n != 0 {
		t.Fatalf("foreign mail: %d %v calls=%d", resp.StatusCode, m, n)
	}
	if resp, _ := inbound(t, ts, map[string]string{"Authorization": "Bearer s3cret"}, "", `not json`); resp.StatusCode != 400 {
		t.Fatalf("malformed: %d", resp.StatusCode)
	}
}

func TestExpenseInboxAndRulesRoutes(t *testing.T) {
	ts, _, _, _ := newTestServerWith(t, func(d *Deps) { d.InboundDomain = "in.example.test" })
	resp, m := do(t, ts, "GET", "/v1/expense-inbox", "live-key", "")
	if resp.StatusCode != 200 || m["address"] != "invoices+abcdefghijklmnopqrstuvwxyz234567@in.example.test" {
		t.Fatalf("inbox: %d %v", resp.StatusCode, m)
	}
	resp, m = do(t, ts, "GET", "/v1/expense-rules", "live-key", "")
	rules, _ := m["rules"].([]any)
	if resp.StatusCode != 200 || len(rules) != 1 {
		t.Fatalf("rules: %d %v", resp.StatusCode, m)
	}
	// The id of a rule comes from the path, whatever the body says.
	resp, m = do(t, ts, "PUT", "/v1/expense-rules/r9", "live-key", `{"id":"other","vendor_match":"AWS","category":"infrastructure","currency":"USD"}`)
	if resp.StatusCode != 200 || m["id"] != "r9" {
		t.Fatalf("update rule: %d %v", resp.StatusCode, m)
	}
	resp, m = do(t, ts, "POST", "/v1/expense-rules", "live-key", `{"id":"x","vendor_match":"AWS","category":"infrastructure","currency":"USD"}`)
	if resp.StatusCode != 201 || m["id"] != "new-rule" {
		t.Fatalf("create rule: %d %v", resp.StatusCode, m)
	}
}
