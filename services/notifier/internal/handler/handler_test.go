package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/mailcategory"
	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/mailx/fakenafru"
	"github.com/0furkancolak/payrudder/pkg/tenantinfo"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/notifier/internal/delivery"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
)

type stubSwitch struct{ off map[string]bool }

func (s stubSwitch) Enabled(_ context.Context, _ uuid.UUID, category string) (bool, error) {
	return !s.off[category], nil
}

// A command for a switched-off category is answered as done and suppressed, and nothing else happens: the Mailer has no store and no sender, so any
// attempt to claim or send would panic.
func TestHandleDropsDisabledCategory(t *testing.T) {
	m := &delivery.Mailer{Switch: stubSwitch{off: map[string]bool{mailcategory.PaymentReceipts: true}}}
	cmd := &commandsv1.CommandEnvelope{
		CommandId: uuid.NewString(), TenantId: uuid.NewString(),
		Command: &commandsv1.CommandEnvelope_SendNotification{SendNotification: &commandsv1.SendNotification{Template: "invoice_paid.v1", To: "a@example.com"}},
	}
	reply, err := (&Handler{Mailer: m}).Handle(context.Background(), cmd)
	if err != nil {
		t.Fatalf("a dropped mail is not an error: %v", err)
	}
	if got := reply.GetNotificationSent(); got == nil || !got.GetSuppressed() || got.GetMessageId() != "" {
		t.Fatalf("reply = %v, want a suppressed NotificationSent", reply)
	}
}

// A tenant id that is not a UUID must not turn into "no tenant" and skip the tenant's mail switch: the command is poison.
func TestHandleRefusesMalformedTenantID(t *testing.T) {
	m := &delivery.Mailer{Switch: stubSwitch{off: map[string]bool{mailcategory.PaymentReceipts: true}}}
	cmd := &commandsv1.CommandEnvelope{
		CommandId: uuid.NewString(), TenantId: "not-a-uuid",
		Command: &commandsv1.CommandEnvelope_SendNotification{SendNotification: &commandsv1.SendNotification{Template: "invoice_paid.v1", To: "a@example.com"}},
	}
	if _, err := (&Handler{Mailer: m}).Handle(context.Background(), cmd); !errors.Is(err, workerx.ErrPoison) {
		t.Fatalf("err = %v, want ErrPoison", err)
	}
}

type tenantStatus plansv1.TenantStatus

func (s tenantStatus) Tenant(context.Context, uuid.UUID) (tenantinfo.Settings, error) {
	return tenantinfo.Settings{Status: plansv1.TenantStatus(s)}, nil
}

// A notification for a tenant the operator has banned is answered as done and suppressed, so the saga goes on, and nothing is sent: the mailer has no store, so
// reaching Deliver would panic.
func TestNotificationOfATenantOnHoldIsSuppressed(t *testing.T) {
	h := &Handler{Mailer: &delivery.Mailer{Tenants: tenantStatus(plansv1.TenantStatus_TENANT_STATUS_BANNED)}}
	cmd := &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: uuid.NewString(), Command: &commandsv1.CommandEnvelope_SendNotification{
		SendNotification: &commandsv1.SendNotification{To: "customer@example.com", Template: "invoice_paid.v1"}}}
	reply, err := h.Handle(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !reply.GetNotificationSent().GetSuppressed() {
		t.Fatalf("reply = %v, want suppressed", reply)
	}
}

type claimOnly struct{ released bool }

func (c *claimOnly) Claim(context.Context, string, *uuid.UUID, uuid.UUID, string, string, string) (store.Claim, string, error) {
	return store.Claimed, "", nil
}
func (c *claimOnly) MarkSent(context.Context, string, string) error { return nil }
func (c *claimOnly) Release(context.Context, string) error          { c.released = true; return nil }

type everyoneSuppressed struct{}

func (everyoneSuppressed) IsSuppressed(context.Context, uuid.UUID, string) (bool, error) {
	return true, nil
}

// A command to mail a suppressed address is answered as done and suppressed, like one for a switched-off category, so the saga goes on; nothing is sent.
func TestNotificationToASuppressedAddressIsAnsweredSuppressed(t *testing.T) {
	f := fakenafru.New()
	defer f.Close()
	notes := &claimOnly{}
	h := &Handler{Mailer: &delivery.Mailer{Default: mailx.Nafru{Resend: mailx.Resend{BaseURL: f.URL, APIKey: "k"}}, From: "a@b.test", Notes: notes, Suppress: everyoneSuppressed{}}}
	cmd := &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: uuid.NewString(), Command: &commandsv1.CommandEnvelope_SendNotification{
		SendNotification: &commandsv1.SendNotification{To: "gone@example.com", Template: "invoice_paid.v1"}}}
	reply, err := h.Handle(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if got := reply.GetNotificationSent(); !got.GetSuppressed() || got.GetMessageId() != "" || len(f.Sent()) != 0 || !notes.released {
		t.Fatalf("reply = %v, sent %d, claim released %v", reply, len(f.Sent()), notes.released)
	}
}
