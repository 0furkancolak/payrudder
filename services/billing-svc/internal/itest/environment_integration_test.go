//go:build integration

package itest

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/api"
)

// testNumberSeriesIsEnforcedByTheDatabase: the sandbox series (SBX-) is not a convention the code keeps, it is a CHECK. Whatever wrote the row, a sandbox
// invoice cannot carry a real number, a live invoice cannot carry a sandbox one, an invoice cannot name no environment, and a credit note is in its invoice's series.
func testNumberSeriesIsEnforcedByTheDatabase(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	insert := func(snapshot, number string) error {
		_, err := shared.system.Exec(ctx, `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, number, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot, provider)
			VALUES ($1,$2,$3,$4,'REGULAR',nullif($5,''),$6,'USD',1,1,'open',$7::jsonb,'')`,
			e.tid, uuid.New(), uuid.New(), uuid.NewString()[:7], number, uuid.New(), snapshot)
		return err
	}
	sandbox, live := `{"environment":"sandbox","customer_external_id":"c"}`, `{"environment":"live","customer_external_id":"c"}`
	for name, tc := range map[string]struct {
		snapshot, number string
		ok               bool
	}{
		"sandbox invoice, sandbox number":   {sandbox, "SBX-2026-000001", true},
		"live invoice, real number":         {live, "INV-2026-000001", true},
		"draft (no number yet), sandbox":    {sandbox, "", true},
		"sandbox invoice, real number":      {sandbox, "INV-2026-000002", false},
		"live invoice, sandbox number":      {live, "SBX-2026-000002", false},
		"no environment in the snapshot":    {`{"customer_external_id":"c"}`, "INV-2026-000003", false},
		"an environment that is not one":    {`{"environment":"staging"}`, "", false},
		"an empty snapshot":                 {`{}`, "", false},
		"a draft that names no environment": {`{"customer_external_id":"c"}`, "", false},
	} {
		if err := insert(tc.snapshot, tc.number); (err == nil) != tc.ok {
			t.Errorf("%s: %v, want ok=%v", name, err, tc.ok)
		}
	}

	// Credit notes follow their invoice.
	sbxInv := seedOne(t, e.tid, uuid.New(), "cus_s", "paid", "USD", "SBX-2026-000010", 1000, "sandbox")
	liveInv := seedOne(t, e.tid, uuid.New(), "cus_l", "paid", "USD", "INV-2026-000010", 1000, "live")
	note := func(invoice uuid.UUID, number string) error {
		_, err := shared.system.Exec(ctx, `INSERT INTO credit_notes (tenant_id, id, customer_id, invoice_id, number, currency, total_minor) VALUES ($1,$2,$3,$4,$5,'USD',100)`,
			e.tid, uuid.New(), uuid.New(), invoice, number)
		return err
	}
	for name, tc := range map[string]struct {
		invoice uuid.UUID
		number  string
		ok      bool
	}{
		"sandbox invoice, sandbox credit note": {sbxInv, "SBX-CN-2026-000001", true},
		"live invoice, real credit note":       {liveInv, "CN-2026-000001", true},
		"sandbox invoice, real credit note":    {sbxInv, "CN-2026-000002", false},
		"live invoice, sandbox credit note":    {liveInv, "SBX-CN-2026-000002", false},
		"an invoice that does not exist":       {uuid.New(), "CN-2026-000003", false},
	} {
		if err := note(tc.invoice, tc.number); (err == nil) != tc.ok {
			t.Errorf("%s: %v, want ok=%v", name, err, tc.ok)
		}
	}
}

// testSandboxIdsAreNotFoundInLive: an invoice or credit note id of one environment's tenant, sent as the other's, is "not found", for every family billing keeps,
// both ways round, and an operator naming the wrong tenant for an id finds nothing either.
func testEnvironmentIdsAreNotFoundInTheOtherEnvironment(t *testing.T) {
	e := newEnv(t, envOpts{})
	svc := api.New(shared.app, shared.system, e.mgr, e.eng)
	liveTenant, sandboxTenant := uuid.New(), uuid.New()
	asLive := tenant.NewContext(context.Background(), tenant.Info{ID: liveTenant, Environment: tenant.EnvLive})
	asSandbox := tenant.NewContext(context.Background(), tenant.Info{ID: sandboxTenant, Environment: tenant.EnvSandbox})
	op := tenant.WithOperator(context.Background())

	liveInv := seedOne(t, liveTenant, uuid.New(), "cus_l", "paid", "USD", "INV-2026-000001", 1000, "live")
	sbxInv := seedOne(t, sandboxTenant, uuid.New(), "cus_s", "paid", "USD", "SBX-2026-000001", 1000, "sandbox")
	var liveNote, sbxNote uuid.UUID
	for _, n := range []struct {
		tid, invoice uuid.UUID
		number       string
		out          *uuid.UUID
	}{{liveTenant, liveInv, "CN-2026-000001", &liveNote}, {sandboxTenant, sbxInv, "SBX-CN-2026-000001", &sbxNote}} {
		*n.out = uuid.New()
		if _, err := shared.system.Exec(context.Background(), `INSERT INTO credit_notes (tenant_id, id, customer_id, invoice_id, number, currency, total_minor) VALUES ($1,$2,$3,$4,$5,'USD',100)`,
			n.tid, *n.out, uuid.New(), n.invoice, n.number); err != nil {
			t.Fatal(err)
		}
	}

	type probe struct {
		name string
		call func(ctx context.Context, invoice, note uuid.UUID) error
	}
	probes := []probe{
		{"GetInvoice", func(c context.Context, inv, _ uuid.UUID) error {
			_, err := svc.GetInvoice(c, &billingv1.GetInvoiceRequest{InvoiceId: inv.String()})
			return err
		}},
		{"CreateInvoiceLink", func(c context.Context, inv, _ uuid.UUID) error {
			_, err := svc.CreateInvoiceLink(c, &billingv1.CreateInvoiceLinkRequest{InvoiceId: inv.String()})
			return err
		}},
		{"GetCreditNote", func(c context.Context, _, note uuid.UUID) error {
			_, err := svc.GetCreditNote(c, &billingv1.GetCreditNoteRequest{CreditNoteId: note.String()})
			return err
		}},
	}
	for _, p := range probes {
		for _, side := range []struct {
			who                 string
			ctx                 context.Context
			otherInv, otherNote uuid.UUID
		}{
			{"sandbox asking for live ids", asSandbox, liveInv, liveNote},
			{"live asking for sandbox ids", asLive, sbxInv, sbxNote},
		} {
			if err := p.call(side.ctx, side.otherInv, side.otherNote); status.Code(err) != codes.NotFound {
				t.Errorf("%s, %s: %v, want NotFound", p.name, side.who, err)
			}
		}
	}
	// The families are served at all (a NotFound everywhere would pass the loop above for the wrong reason).
	if _, err := svc.GetInvoice(asLive, &billingv1.GetInvoiceRequest{InvoiceId: liveInv.String()}); err != nil {
		t.Errorf("live cannot read its own invoice: %v", err)
	}
	if _, err := svc.GetCreditNote(asSandbox, &billingv1.GetCreditNoteRequest{CreditNoteId: sbxNote.String()}); err != nil {
		t.Errorf("sandbox cannot read its own credit note: %v", err)
	}
	// Lists show only the caller's own.
	if r, err := svc.ListInvoices(asLive, &billingv1.ListInvoicesRequest{}); err != nil || len(r.GetInvoices()) != 1 || r.GetInvoices()[0].GetId() != liveInv.String() {
		t.Errorf("live invoices = %v, %v", r, err)
	}
	if r, err := svc.ListCreditNotes(asSandbox, &billingv1.ListCreditNotesRequest{}); err != nil || len(r.GetCreditNotes()) != 1 || r.GetCreditNotes()[0].GetId() != sbxNote.String() {
		t.Errorf("sandbox credit notes = %v, %v", r, err)
	}
	// An operator who names the wrong tenant for an invoice finds nothing, and changes nothing.
	if _, err := svc.CreateCreditNote(op, &billingv1.CreateCreditNoteRequest{TenantId: sandboxTenant.String(), InvoiceId: liveInv.String(), AmountMinor: 100}); status.Code(err) != codes.NotFound {
		t.Errorf("a credit note for a live invoice under the sandbox tenant: %v, want NotFound", err)
	}
	if _, err := svc.VoidInvoice(op, &billingv1.VoidInvoiceRequest{TenantId: liveTenant.String(), InvoiceId: sbxInv.String()}); status.Code(err) != codes.NotFound {
		t.Errorf("voiding a sandbox invoice under the live tenant: %v, want NotFound", err)
	}
	if _, err := svc.GetInvoiceSaga(op, &billingv1.GetInvoiceSagaRequest{TenantId: liveTenant.String(), InvoiceId: sbxInv.String()}); status.Code(err) != codes.NotFound {
		t.Errorf("the saga of a sandbox invoice under the live tenant: %v, want NotFound", err)
	}
}
