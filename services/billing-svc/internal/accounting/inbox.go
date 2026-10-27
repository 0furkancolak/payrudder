package accounting

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/accounting/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// The invoice inbox is dumb storage: a forwarded mail's attachments are kept as items to review, and the console reads them in the browser (the text layer of a PDF,
// OCR of an image) and makes the expense. Nothing here reads a document.
const (
	// MaxMailBytes is the most attachment bytes one mail may add (a gRPC message is 4 MiB).
	MaxMailBytes = 3 << 20
	// maxMailFiles is the most attachments of one mail that are kept.
	maxMailFiles = 10
	// maxToReview stops an inbox that nobody reads from growing for ever.
	maxToReview = 500
	// maxRules is the most recurring-vendor rules of a tenant.
	maxRules = 200
	maxName  = 200
)

var tokenEnc = base32.StdEncoding.WithPadding(base32.NoPadding)

// newToken is the secret part of an inbox address: 160 random bits, lower case letters and digits.
func newToken() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return strings.ToLower(tokenEnc.EncodeToString(b))
}

// AttachmentType returns the type of an attachment from its first bytes (a PDF, PNG or JPEG), or "": the type the sender claims is not trusted.
func AttachmentType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return "application/pdf"
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	}
	return ""
}

// cleanName keeps the last element of a file name, without control characters, within the column's limit.
func cleanName(s, fallback string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	s = path.Base(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
	s = strings.TrimSpace(s)
	if s == "." || s == "/" || s == "" {
		return fallback
	}
	for utf8.RuneCountInString(s) > maxName {
		_, n := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-n]
	}
	return s
}

func cut(s string, n int) string {
	s = strings.TrimSpace(s)
	for utf8.RuneCountInString(s) > n {
		_, k := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-k]
	}
	return s
}

func scopeOf(appID string) (string, error) {
	if appID == "" {
		return "", nil
	}
	id, err := uuid.Parse(appID)
	if err != nil {
		return "", errkey.New(codes.InvalidArgument, "billing.accounting.bad_app", "app_id is not valid")
	}
	return id.String(), nil
}

// GetExpenseInbox implements billingv1.BillingServiceServer.
func (h *Handler) GetExpenseInbox(ctx context.Context, req *billingv1.GetExpenseInboxRequest) (*billingv1.ExpenseInbox, error) {
	return h.inbox(ctx, req.GetAppId(), false)
}

// RotateExpenseInbox implements billingv1.BillingServiceServer.
func (h *Handler) RotateExpenseInbox(ctx context.Context, req *billingv1.GetExpenseInboxRequest) (*billingv1.ExpenseInbox, error) {
	return h.inbox(ctx, req.GetAppId(), true)
}

func (h *Handler) inbox(ctx context.Context, appID string, rotate bool) (*billingv1.ExpenseInbox, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := scopeOf(appID)
	if err != nil {
		return nil, err
	}
	var token string
	err = pgxx.InTenantTx(ctx, h.app, ti.ID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		var err error
		switch {
		case rotate:
			if _, err = q.InsertInbox(ctx, sqlc.InsertInboxParams{TenantID: ti.ID, Scope: scope, Token: newToken()}); err != nil {
				return err
			}
			token, err = q.RotateInbox(ctx, sqlc.RotateInboxParams{TenantID: ti.ID, Scope: scope, Token: newToken()})
		default:
			token, err = q.InsertInbox(ctx, sqlc.InsertInboxParams{TenantID: ti.ID, Scope: scope, Token: newToken()})
		}
		return err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return &billingv1.ExpenseInbox{Token: token, AppId: scope}, nil
}

// ReceiveInboxMail implements billingv1.BillingServiceServer. It is public: the token is the address. A mail to a token that does not exist stores nothing and is not
// an error, so the sender of junk learns nothing about which addresses exist.
func (h *Handler) ReceiveInboxMail(ctx context.Context, req *billingv1.ReceiveInboxMailRequest) (*billingv1.ReceiveInboxMailResponse, error) {
	out := &billingv1.ReceiveInboxMailResponse{}
	token := strings.ToLower(strings.TrimSpace(req.GetToken()))
	if len(token) < 16 || len(token) > 64 {
		out.Skipped = int32(len(req.GetAttachments()))
		return out, nil
	}
	var target sqlc.ResolveInboxTokenRow
	found, err := h.system.Find(ctx, func(ctx context.Context, q *sqlc.Queries) (bool, error) {
		t, err := q.ResolveInboxToken(ctx, token)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		target = t
		return err == nil, err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	if !found {
		out.Skipped = int32(len(req.GetAttachments()))
		return out, nil
	}
	var appID *uuid.UUID
	if target.Scope != "" {
		if id, perr := uuid.Parse(target.Scope); perr == nil {
			appID = &id
		}
	}
	err = h.system.InTx(ctx, target.TenantID, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		pending, err := q.CountInboxToReview(ctx, target.TenantID)
		if err != nil {
			return err
		}
		var total int
		for i, a := range req.GetAttachments() {
			typ := AttachmentType(a.GetData())
			size := len(a.GetData())
			if typ == "" || size == 0 || i >= maxMailFiles || total+size > MaxMailBytes || pending >= maxToReview {
				out.Skipped++
				continue
			}
			total += size
			pending++
			ext := map[string]string{"application/pdf": ".pdf", "image/png": ".png", "image/jpeg": ".jpg"}[typ]
			if err := q.InsertInboxItem(ctx, sqlc.InsertInboxItemParams{TenantID: target.TenantID, ID: uuid.New(), AppID: appID, FromAddr: cut(req.GetFrom(), 320), Subject: cut(req.GetSubject(), 300),
				Filename: cleanName(a.GetFilename(), "attachment"+ext), ContentType: typ, SizeBytes: int64(size), Content: a.GetData()}); err != nil {
				return err
			}
			out.Stored++
		}
		return nil
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return out, nil
}

func inboxItemProto(id uuid.UUID, app *uuid.UUID, from, subject, filename, ctype string, size int64, status string, expense *uuid.UUID, at time.Time) *billingv1.InboxItem {
	it := &billingv1.InboxItem{Id: id.String(), From: from, Subject: subject, Filename: filename, ContentType: ctype, SizeBytes: size, Status: status, ReceiveTime: timestamppb.New(at)}
	if app != nil {
		it.AppId = app.String()
	}
	if expense != nil {
		it.ExpenseId = expense.String()
	}
	return it
}

// ListInboxItems implements billingv1.BillingServiceServer.
func (h *Handler) ListInboxItems(ctx context.Context, req *billingv1.ListInboxItemsRequest) (*billingv1.ListInboxItemsResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	status := req.GetStatus()
	if status == "" {
		status = "to_review"
	}
	if status != "to_review" && status != "accepted" && status != "dismissed" {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_expense", "status is to_review, accepted or dismissed")
	}
	size := req.GetPageSize()
	if size == 0 {
		size = 50
	}
	if size < 1 || size > 200 {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_page_size", "page_size must be 1..200")
	}
	var rows []sqlc.ListInboxItemsRow
	err = pgxx.InTenantTx(ctx, h.app, ti.ID, func(tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).ListInboxItems(ctx, sqlc.ListInboxItemsParams{TenantID: ti.ID, Status: status, RowLimit: size})
		return err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	out := &billingv1.ListInboxItemsResponse{Items: make([]*billingv1.InboxItem, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, inboxItemProto(r.ID, r.AppID, r.FromAddr, r.Subject, r.Filename, r.ContentType, r.SizeBytes, r.Status, r.ExpenseID, r.ReceivedAt))
	}
	return out, nil
}

// GetInboxItemFile implements billingv1.BillingServiceServer.
func (h *Handler) GetInboxItemFile(ctx context.Context, req *billingv1.GetInboxItemFileRequest) (*billingv1.GetInboxItemFileResponse, error) {
	ti, id, err := h.itemScope(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	var row sqlc.GetInboxItemFileRow
	err = pgxx.InTenantTx(ctx, h.app, ti.ID, func(tx pgx.Tx) error {
		var err error
		row, err = sqlc.New(tx).GetInboxItemFile(ctx, sqlc.GetInboxItemFileParams{TenantID: ti.ID, ID: id})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errkey.New(codes.NotFound, "billing.accounting.inbox_item_not_found", "no such inbox item")
	}
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return &billingv1.GetInboxItemFileResponse{Filename: row.Filename, ContentType: row.ContentType, Data: row.Content}, nil
}

// ResolveInboxItem implements billingv1.BillingServiceServer.
func (h *Handler) ResolveInboxItem(ctx context.Context, req *billingv1.ResolveInboxItemRequest) (*billingv1.ResolveInboxItemResponse, error) {
	ti, id, err := h.itemScope(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if req.GetStatus() != "accepted" && req.GetStatus() != "dismissed" {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_expense", "status is accepted or dismissed")
	}
	var expense *uuid.UUID
	if req.GetExpenseId() != "" {
		e, perr := uuid.Parse(req.GetExpenseId())
		if perr != nil {
			return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_expense", "expense_id is not valid")
		}
		expense = &e
	}
	var row sqlc.ResolveInboxItemRow
	err = pgxx.InTenantTx(ctx, h.app, ti.ID, func(tx pgx.Tx) error {
		var err error
		row, err = sqlc.New(tx).ResolveInboxItem(ctx, sqlc.ResolveInboxItemParams{TenantID: ti.ID, ID: id, Status: req.GetStatus(), ExpenseID: expense})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errkey.New(codes.NotFound, "billing.accounting.inbox_item_not_found", "no such inbox item waiting for review")
	}
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return &billingv1.ResolveInboxItemResponse{Item: inboxItemProto(row.ID, row.AppID, row.FromAddr, row.Subject, row.Filename, row.ContentType, row.SizeBytes, row.Status, row.ExpenseID, row.ReceivedAt)}, nil
}

func (h *Handler) itemScope(ctx context.Context, itemID string) (tenant.Info, uuid.UUID, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return tenant.Info{}, uuid.Nil, err
	}
	id, err := uuid.Parse(itemID)
	if err != nil {
		return tenant.Info{}, uuid.Nil, errkey.New(codes.NotFound, "billing.accounting.inbox_item_not_found", "no such inbox item")
	}
	return ti, id, nil
}

// --- recurring-vendor rules

func ruleProto(r sqlc.ListExpenseRulesRow) *billingv1.ExpenseRule {
	out := &billingv1.ExpenseRule{Id: r.ID.String(), VendorMatch: r.VendorMatch, Category: r.Category, Currency: r.Currency, TaxBps: r.TaxBps, ExpectedAmountMinor: r.ExpectedAmountMinor,
		ToleranceBps: r.ToleranceBps, AutoApprove: r.AutoApprove}
	if r.AppID != nil {
		out.AppId = r.AppID.String()
	}
	return out
}

// ListExpenseRules implements billingv1.BillingServiceServer.
func (h *Handler) ListExpenseRules(ctx context.Context, _ *billingv1.ListExpenseRulesRequest) (*billingv1.ListExpenseRulesResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var rows []sqlc.ListExpenseRulesRow
	err = pgxx.InTenantTx(ctx, h.app, ti.ID, func(tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).ListExpenseRules(ctx, ti.ID)
		return err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	out := &billingv1.ListExpenseRulesResponse{Rules: make([]*billingv1.ExpenseRule, 0, len(rows))}
	for _, r := range rows {
		out.Rules = append(out.Rules, ruleProto(r))
	}
	return out, nil
}

// SaveExpenseRule implements billingv1.BillingServiceServer.
func (h *Handler) SaveExpenseRule(ctx context.Context, req *billingv1.SaveExpenseRuleRequest) (*billingv1.SaveExpenseRuleResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	r := req.GetRule()
	bad := func(msg string) error { return errkey.New(codes.InvalidArgument, "billing.accounting.bad_rule", msg) }
	match := strings.TrimSpace(r.GetVendorMatch())
	switch {
	case utf8.RuneCountInString(match) < 2 || utf8.RuneCountInString(match) > 120:
		return nil, bad("the vendor text is 2 to 120 characters")
	case !model.ValidCategory(r.GetCategory()):
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_category", "the category is not one of the known categories")
	case r.GetTaxBps() < 0 || r.GetTaxBps() > 10000 || r.GetToleranceBps() < 0 || r.GetToleranceBps() > 10000:
		return nil, bad("tax and tolerance are 0 to 10000 basis points")
	case r.GetExpectedAmountMinor() < 0 || r.GetExpectedAmountMinor() > 1_000_000_000_000_000:
		return nil, bad("the expected amount is not valid")
	case r.GetAutoApprove() && r.GetExpectedAmountMinor() == 0:
		return nil, bad("automatic approval needs an expected amount")
	}
	if _, ok := money.LookupCurrency(r.GetCurrency()); !ok {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.unknown_currency", "the currency is not supported")
	}
	app, err := scopeOf(r.GetAppId())
	if err != nil {
		return nil, err
	}
	var appID *uuid.UUID
	if app != "" {
		id := uuid.MustParse(app)
		appID = &id
	}
	id := uuid.New()
	if r.GetId() != "" {
		if id, err = uuid.Parse(r.GetId()); err != nil {
			return nil, errkey.New(codes.NotFound, "billing.accounting.rule_not_found", "no such rule")
		}
	}
	err = pgxx.InTenantTx(ctx, h.app, ti.ID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if r.GetId() != "" {
			_, err := q.UpdateExpenseRule(ctx, sqlc.UpdateExpenseRuleParams{TenantID: ti.ID, ID: id, AppID: appID, VendorMatch: match, Category: r.GetCategory(), Currency: r.GetCurrency(),
				TaxBps: r.GetTaxBps(), ExpectedAmountMinor: r.GetExpectedAmountMinor(), ToleranceBps: r.GetToleranceBps(), AutoApprove: r.GetAutoApprove()})
			return err
		}
		n, err := q.CountExpenseRules(ctx, ti.ID)
		if err != nil {
			return err
		}
		if n >= maxRules {
			return errkey.New(codes.FailedPrecondition, "billing.accounting.too_many_rules", "a tenant may have at most 200 rules")
		}
		return q.InsertExpenseRule(ctx, sqlc.InsertExpenseRuleParams{TenantID: ti.ID, ID: id, AppID: appID, VendorMatch: match, Category: r.GetCategory(), Currency: r.GetCurrency(),
			TaxBps: r.GetTaxBps(), ExpectedAmountMinor: r.GetExpectedAmountMinor(), ToleranceBps: r.GetToleranceBps(), AutoApprove: r.GetAutoApprove()})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, errkey.New(codes.NotFound, "billing.accounting.rule_not_found", "no such rule")
	case errkey.Of(err) != "":
		return nil, err
	case err != nil:
		return nil, apierr.Internal(ctx, err)
	}
	return &billingv1.SaveExpenseRuleResponse{Rule: &billingv1.ExpenseRule{Id: id.String(), VendorMatch: match, Category: r.GetCategory(), Currency: r.GetCurrency(), TaxBps: r.GetTaxBps(),
		ExpectedAmountMinor: r.GetExpectedAmountMinor(), ToleranceBps: r.GetToleranceBps(), AutoApprove: r.GetAutoApprove(), AppId: app}}, nil
}

// DeleteExpenseRule implements billingv1.BillingServiceServer.
func (h *Handler) DeleteExpenseRule(ctx context.Context, req *billingv1.DeleteExpenseRuleRequest) (*billingv1.DeleteExpenseRuleResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, errkey.New(codes.NotFound, "billing.accounting.rule_not_found", "no such rule")
	}
	var n int64
	err = pgxx.InTenantTx(ctx, h.app, ti.ID, func(tx pgx.Tx) error {
		var err error
		n, err = sqlc.New(tx).DeleteExpenseRule(ctx, sqlc.DeleteExpenseRuleParams{TenantID: ti.ID, ID: id})
		return err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	if n == 0 {
		return nil, errkey.New(codes.NotFound, "billing.accounting.rule_not_found", "no such rule")
	}
	return &billingv1.DeleteExpenseRuleResponse{}, nil
}
