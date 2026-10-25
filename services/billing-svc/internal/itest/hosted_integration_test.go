//go:build integration

package itest

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/api"
)

type fakePDFs struct{}

func (fakePDFs) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://files.example/" + key + "?sig=1", nil
}

func testHostedInvoiceLinks(t *testing.T) {
	e := newEnv(t, envOpts{})
	svc := api.New(shared.app, shared.system, e.mgr, e.eng).WithPDFs(fakePDFs{})
	mine := tenant.NewContext(context.Background(), tenant.Info{ID: e.tid})
	stranger := tenant.NewContext(context.Background(), tenant.Info{ID: uuid.New()})
	code := func(err error) codes.Code { return status.Code(err) }

	customer := uuid.New()
	paid := seedOne(t, e.tid, customer, "cus_h", "paid", "USD", "INV-2026-000201", 1500, "live")
	draft := seedOne(t, e.tid, customer, "cus_h", "draft", "USD", "", 700, "live")
	if _, err := shared.system.Exec(context.Background(), `UPDATE invoices SET snapshot = snapshot || '{"tenant_name":"Acme Ltd","customer_name":"Ada Lovelace"}'::jsonb WHERE id = $1`, paid); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.system.Exec(context.Background(), `INSERT INTO invoice_lines (tenant_id, invoice_id, line_no, kind, amount_minor, description) VALUES ($1,$2,1,'BASE_FEE',1500,'Base fee')`, e.tid, paid); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.CreateInvoiceLink(mine, &billingv1.CreateInvoiceLinkRequest{InvoiceId: draft.String()}); code(err) != codes.FailedPrecondition {
		t.Errorf("a draft: %v", err)
	}
	if _, err := svc.CreateInvoiceLink(stranger, &billingv1.CreateInvoiceLinkRequest{InvoiceId: paid.String()}); code(err) != codes.NotFound {
		t.Errorf("another tenant's invoice: %v", err)
	}
	if _, err := svc.CreateInvoiceLink(mine, &billingv1.CreateInvoiceLinkRequest{InvoiceId: paid.String(), ExpiresInDays: 400}); code(err) != codes.InvalidArgument {
		t.Errorf("a year and more: %v", err)
	}
	link, err := svc.CreateInvoiceLink(mine, &billingv1.CreateInvoiceLinkRequest{InvoiceId: paid.String()})
	if err != nil || len(link.GetToken()) < 40 {
		t.Fatalf("link = %v, %v", link, err)
	}
	if d := time.Until(link.GetExpireTime().AsTime()); d < 89*24*time.Hour || d > 91*24*time.Hour {
		t.Errorf("expires in %v, want about 90 days", d)
	}
	// Only a hash is stored.
	var clear int
	if err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM payment_links WHERE token_hash = $1`, []byte(link.GetToken())).Scan(&clear); err != nil || clear != 0 {
		t.Fatalf("the token is stored as it is: %d, %v", clear, err)
	}
	h := sha256.Sum256([]byte(link.GetToken()))
	var hashed int
	if err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM payment_links WHERE token_hash = $1`, h[:]).Scan(&hashed); err != nil || hashed != 1 {
		t.Fatalf("hashed rows = %d, %v", hashed, err)
	}

	// A visitor needs no tenant, only the token.
	v := context.Background()
	got, err := svc.GetHostedInvoice(v, &billingv1.GetHostedInvoiceRequest{Token: link.GetToken()})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetInvoice().GetNumber() != "INV-2026-000201" || got.GetInvoice().GetTotalMinor() != 1500 || len(got.GetInvoice().GetLines()) != 1 ||
		got.GetSellerName() != "Acme Ltd" || got.GetCustomerName() != "Ada Lovelace" {
		t.Fatalf("hosted invoice = %v", got)
	}
	if got.GetPdfAvailable() || got.GetInvoice().GetPdfObjectKey() != "" {
		t.Errorf("no PDF yet, and its storage key is never shown: %v", got)
	}
	if _, err := svc.GetHostedInvoicePdfUrl(v, &billingv1.GetHostedInvoicePdfUrlRequest{Token: link.GetToken()}); code(err) != codes.FailedPrecondition {
		t.Errorf("pdf before it is rendered: %v", err)
	}
	if _, err := shared.system.Exec(context.Background(), `UPDATE invoices SET pdf_object_key = 'acme/inv.pdf' WHERE id = $1`, paid); err != nil {
		t.Fatal(err)
	}
	if got, _ = svc.GetHostedInvoice(v, &billingv1.GetHostedInvoiceRequest{Token: link.GetToken()}); !got.GetPdfAvailable() || got.GetInvoice().GetPdfObjectKey() != "" {
		t.Errorf("pdf = %v", got)
	}
	if u, err := svc.GetHostedInvoicePdfUrl(v, &billingv1.GetHostedInvoicePdfUrlRequest{Token: link.GetToken()}); err != nil || u.GetUrl() != "https://files.example/acme/inv.pdf?sig=1" {
		t.Errorf("pdf url = %v, %v", u, err)
	}

	// Unknown, malformed, expired and revoked links all look the same.
	for name, tok := range map[string]string{"unknown": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "short": "abc", "empty": ""} {
		if _, err := svc.GetHostedInvoice(v, &billingv1.GetHostedInvoiceRequest{Token: tok}); code(err) != codes.NotFound {
			t.Errorf("%s token: %v", name, err)
		}
	}
	if err := pgxx.InTenantTx(context.Background(), shared.app, e.tid, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `UPDATE payment_links SET expires_at = now() - interval '1 second' WHERE invoice_id = $1`, paid)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetHostedInvoice(v, &billingv1.GetHostedInvoiceRequest{Token: link.GetToken()}); code(err) != codes.NotFound {
		t.Errorf("expired: %v", err)
	}
	fresh, err := svc.CreateInvoiceLink(mine, &billingv1.CreateInvoiceLinkRequest{InvoiceId: paid.String(), ExpiresInDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetHostedInvoice(v, &billingv1.GetHostedInvoiceRequest{Token: fresh.GetToken()}); err != nil {
		t.Fatalf("a new link: %v", err)
	}
	if _, err := svc.RevokeInvoiceLinks(stranger, &billingv1.RevokeInvoiceLinksRequest{InvoiceId: paid.String()}); code(err) != codes.NotFound {
		t.Errorf("another tenant revoking: %v", err)
	}
	r, err := svc.RevokeInvoiceLinks(mine, &billingv1.RevokeInvoiceLinksRequest{InvoiceId: paid.String()})
	if err != nil || r.GetRevoked() < 1 {
		t.Fatalf("revoke = %v, %v", r, err)
	}
	if _, err := svc.GetHostedInvoice(v, &billingv1.GetHostedInvoiceRequest{Token: fresh.GetToken()}); code(err) != codes.NotFound {
		t.Errorf("revoked: %v", err)
	}
}
