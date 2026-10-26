//go:build integration

package itest

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/accounting"
)

// testInbox: the invoice inbox keeps what a mail brought and nothing else, one tenant never sees another's items, the rules are validated and stored, and an expense keeps
// its tax and the receipt it was entered from.
func testInbox(t *testing.T) {
	infra(t)
	svc := accounting.New(shared.app, shared.system)
	tid, other := uuid.New(), uuid.New()
	ctx := tenant.NewContext(context.Background(), tenant.Info{ID: tid})
	otherCtx := tenant.NewContext(context.Background(), tenant.Info{ID: other})
	pdf := []byte("%PDF-1.4\nreceipt")

	in, err := svc.GetExpenseInbox(ctx, &billingv1.GetExpenseInboxRequest{})
	if err != nil || len(in.GetToken()) < 16 {
		t.Fatalf("inbox: %v %v", in, err)
	}
	again, _ := svc.GetExpenseInbox(ctx, &billingv1.GetExpenseInboxRequest{})
	if again.GetToken() != in.GetToken() {
		t.Fatal("the inbox address must stay the same until it is rotated")
	}
	appInbox, _ := svc.GetExpenseInbox(ctx, &billingv1.GetExpenseInboxRequest{AppId: uuid.NewString()})
	if appInbox.GetToken() == in.GetToken() {
		t.Fatal("an app has its own inbox")
	}

	// A mail: a PDF and an executable (judged by bytes, not by what the sender says) and a PNG named like a path.
	png := append([]byte("\x89PNG\r\n\x1a\n"), 1, 2, 3)
	res, err := svc.ReceiveInboxMail(context.Background(), &billingv1.ReceiveInboxMailRequest{Token: strings.ToUpper(in.GetToken()), From: "billing@vendor.test", Subject: "Invoice 7", Attachments: []*billingv1.InboxAttachment{
		{Filename: "inv.pdf", ContentType: "application/pdf", Data: pdf},
		{Filename: "evil.pdf", ContentType: "application/pdf", Data: []byte("MZ\x90\x00 not a pdf")},
		{Filename: "../../tmp/photo.png", ContentType: "image/png", Data: png},
	}})
	if err != nil || res.GetStored() != 2 || res.GetSkipped() != 1 {
		t.Fatalf("receive: %v %v", res, err)
	}
	// An unknown address stores nothing and is not an error.
	if res, err = svc.ReceiveInboxMail(context.Background(), &billingv1.ReceiveInboxMailRequest{Token: "doesnotexistdoesnotexist", Attachments: []*billingv1.InboxAttachment{{Filename: "a.pdf", Data: pdf}}}); err != nil || res.GetStored() != 0 {
		t.Fatalf("unknown token: %v %v", res, err)
	}
	// More than a mail may bring.
	big := append([]byte("%PDF-"), make([]byte, accounting.MaxMailBytes)...)
	if res, _ = svc.ReceiveInboxMail(context.Background(), &billingv1.ReceiveInboxMailRequest{Token: in.GetToken(), Attachments: []*billingv1.InboxAttachment{{Filename: "big.pdf", Data: big}}}); res.GetStored() != 0 {
		t.Fatal("an oversize attachment must be skipped")
	}

	list, err := svc.ListInboxItems(ctx, &billingv1.ListInboxItemsRequest{})
	if err != nil || len(list.GetItems()) != 2 {
		t.Fatalf("list: %v %v", list, err)
	}
	names := map[string]string{}
	for _, it := range list.GetItems() {
		names[it.GetFilename()] = it.GetContentType()
		if it.GetStatus() != "to_review" || it.GetFrom() != "billing@vendor.test" || it.GetSubject() != "Invoice 7" {
			t.Fatalf("item: %v", it)
		}
	}
	if names["inv.pdf"] != "application/pdf" || names["photo.png"] != "image/png" {
		t.Fatalf("names and types: %v", names)
	}
	if o, _ := svc.ListInboxItems(otherCtx, &billingv1.ListInboxItemsRequest{}); len(o.GetItems()) != 0 {
		t.Fatal("another tenant's inbox must be empty")
	}
	var pdfItem *billingv1.InboxItem
	for _, it := range list.GetItems() {
		if it.GetFilename() == "inv.pdf" {
			pdfItem = it
		}
	}
	file, err := svc.GetInboxItemFile(ctx, &billingv1.GetInboxItemFileRequest{Id: pdfItem.GetId()})
	if err != nil || string(file.GetData()) != string(pdf) {
		t.Fatalf("file: %v %v", file, err)
	}
	_, err = svc.GetInboxItemFile(otherCtx, &billingv1.GetInboxItemFileRequest{Id: pdfItem.GetId()})
	wantKey(t, err, codes.NotFound, "billing.accounting.inbox_item_not_found")

	// Accepting records the expense; an item is resolved once.
	exp, err := svc.CreateExpense(ctx, &billingv1.CreateExpenseRequest{Vendor: "Vendor", Category: "software", AmountMinor: 12000, Currency: "EUR", IncurredOn: "2026-10-01", TaxMinor: 2000, AttachmentAssetId: uuid.NewString()})
	if err != nil || exp.GetExpense().GetTaxMinor() != 2000 || exp.GetExpense().GetAttachmentAssetId() == "" {
		t.Fatalf("expense with tax and attachment: %v %v", exp, err)
	}
	if _, err = svc.CreateExpense(ctx, &billingv1.CreateExpenseRequest{Vendor: "Vendor", Category: "software", AmountMinor: 100, Currency: "EUR", IncurredOn: "2026-10-01", TaxMinor: 101}); err == nil {
		t.Fatal("tax above the amount must be refused")
	}
	if _, err = svc.ResolveInboxItem(ctx, &billingv1.ResolveInboxItemRequest{Id: pdfItem.GetId(), Status: "accepted", ExpenseId: exp.GetExpense().GetId()}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.ResolveInboxItem(ctx, &billingv1.ResolveInboxItemRequest{Id: pdfItem.GetId(), Status: "dismissed"})
	wantKey(t, err, codes.NotFound, "billing.accounting.inbox_item_not_found")
	if left, _ := svc.ListInboxItems(ctx, &billingv1.ListInboxItemsRequest{}); len(left.GetItems()) != 1 {
		t.Fatalf("one item should be left to review: %v", left)
	}
	if acc, _ := svc.ListInboxItems(ctx, &billingv1.ListInboxItemsRequest{Status: "accepted"}); len(acc.GetItems()) != 1 || acc.GetItems()[0].GetExpenseId() != exp.GetExpense().GetId() {
		t.Fatalf("accepted: %v", acc)
	}

	// Rotating drops the old address.
	rot, err := svc.RotateExpenseInbox(ctx, &billingv1.GetExpenseInboxRequest{})
	if err != nil || rot.GetToken() == in.GetToken() {
		t.Fatalf("rotate: %v %v", rot, err)
	}
	if res, _ = svc.ReceiveInboxMail(context.Background(), &billingv1.ReceiveInboxMailRequest{Token: in.GetToken(), Attachments: []*billingv1.InboxAttachment{{Filename: "a.pdf", Data: pdf}}}); res.GetStored() != 0 {
		t.Fatal("mail to the old address must be dropped")
	}

	// Rules.
	rule := &billingv1.ExpenseRule{VendorMatch: "aws", Category: "infrastructure", Currency: "USD", TaxBps: 0, ExpectedAmountMinor: 4900, ToleranceBps: 500, AutoApprove: true}
	saved, err := svc.SaveExpenseRule(ctx, &billingv1.SaveExpenseRuleRequest{Rule: rule})
	if err != nil || saved.GetRule().GetId() == "" {
		t.Fatalf("save rule: %v %v", saved, err)
	}
	rule.Id, rule.ToleranceBps = saved.GetRule().GetId(), 1000
	if _, err = svc.SaveExpenseRule(ctx, &billingv1.SaveExpenseRuleRequest{Rule: rule}); err != nil {
		t.Fatal(err)
	}
	rules, _ := svc.ListExpenseRules(ctx, &billingv1.ListExpenseRulesRequest{})
	if len(rules.GetRules()) != 1 || rules.GetRules()[0].GetToleranceBps() != 1000 {
		t.Fatalf("rules: %v", rules)
	}
	if o, _ := svc.ListExpenseRules(otherCtx, &billingv1.ListExpenseRulesRequest{}); len(o.GetRules()) != 0 {
		t.Fatal("rules are per tenant")
	}
	_, err = svc.SaveExpenseRule(ctx, &billingv1.SaveExpenseRuleRequest{Rule: &billingv1.ExpenseRule{VendorMatch: "x", Category: "software", Currency: "USD"}})
	wantKey(t, err, codes.InvalidArgument, "billing.accounting.bad_rule")
	_, err = svc.SaveExpenseRule(ctx, &billingv1.SaveExpenseRuleRequest{Rule: &billingv1.ExpenseRule{VendorMatch: "Figma", Category: "software", Currency: "USD", AutoApprove: true}})
	wantKey(t, err, codes.InvalidArgument, "billing.accounting.bad_rule")
	_, err = svc.SaveExpenseRule(otherCtx, &billingv1.SaveExpenseRuleRequest{Rule: &billingv1.ExpenseRule{Id: rule.Id, VendorMatch: "aws", Category: "software", Currency: "USD"}})
	wantKey(t, err, codes.NotFound, "billing.accounting.rule_not_found")
	_, err = svc.DeleteExpenseRule(otherCtx, &billingv1.DeleteExpenseRuleRequest{Id: rule.Id})
	wantKey(t, err, codes.NotFound, "billing.accounting.rule_not_found")
	if _, err = svc.DeleteExpenseRule(ctx, &billingv1.DeleteExpenseRuleRequest{Id: rule.Id}); err != nil {
		t.Fatal(err)
	}
}
