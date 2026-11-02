//go:build integration

package itest

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// testSupportViewer: a viewer of an account reads (docs/13-admin-ui.md, "A viewer reads"): they list and read the account's tickets, and every call that writes on the
// help desk is refused, each with the account's own "read-only" answer; an owner or an administrator of the same account does all of it.
func testSupportViewer(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ada := e.verified(t, "viewer-ada@example.com", "Viewer Co")
	other := e.signUp(t, "viewer-reader@example.com", "Reader Co")
	viewer := person{id: other.GetUser().GetId(), account: ada.account}
	if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'viewer')`, ada.account, viewer.id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: ada.id, AccountId: ada.account, Name: "Shop", Currency: "EUR", Timezone: "Europe/Berlin"}); err != nil {
		t.Fatal(err)
	}
	opened, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: ada.id, AccountId: ada.account, Subject: "Webhooks are late", Category: "bug", Body: "Since this morning."})
	if err != nil {
		t.Fatal(err)
	}
	tk, first := opened.GetTicket(), opened.GetMessage()
	solved, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: ada.id, AccountId: ada.account, Subject: "Solved already", Category: "other", Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.support.SetMySupportTicketStatus(ctx, &plansv1.SetMySupportTicketStatusRequest{UserId: ada.id, TicketId: solved.GetTicket().GetId(), Status: "solved"}); err != nil {
		t.Fatal(err)
	}

	t.Run("a viewer reads the account's tickets", func(t *testing.T) {
		list, err := e.support.ListMySupportTickets(ctx, &plansv1.ListMySupportTicketsRequest{UserId: viewer.id})
		if err != nil || len(list.GetTickets()) != 2 {
			t.Fatalf("the viewer's list: %v %v", err, list.GetTickets())
		}
		if r, err := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: viewer.id, TicketId: tk.GetId()}); err != nil || len(r.GetMessages()) != 1 {
			t.Fatalf("the viewer reads a ticket: %v %v", err, r)
		}
	})

	t.Run("a viewer cannot open, answer, change the status of, rate or attach to a ticket", func(t *testing.T) {
		const key = "identity.account.forbidden"
		_, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: viewer.id, AccountId: viewer.account, Subject: "Not allowed", Category: "other", Body: "x"})
		wantCode(t, "open", err, codes.PermissionDenied, key)
		_, err = e.support.ReplyMySupportTicket(ctx, &plansv1.ReplyMySupportTicketRequest{UserId: viewer.id, TicketId: tk.GetId(), Body: "me too"})
		wantCode(t, "reply", err, codes.PermissionDenied, key)
		_, err = e.support.SetMySupportTicketStatus(ctx, &plansv1.SetMySupportTicketStatusRequest{UserId: viewer.id, TicketId: tk.GetId(), Status: "solved"})
		wantCode(t, "close it", err, codes.PermissionDenied, key)
		_, err = e.support.RateMySupportTicket(ctx, &plansv1.RateMySupportTicketRequest{UserId: viewer.id, TicketId: solved.GetTicket().GetId(), Rating: 1, Comment: "bad"})
		wantCode(t, "rate", err, codes.PermissionDenied, key)
		_, err = e.support.AuthorizeSupportAttachment(ctxFor(viewer.id), &plansv1.AuthorizeSupportAttachmentRequest{UserId: viewer.id, TicketId: tk.GetId(), MessageId: first.GetId()})
		wantCode(t, "attach to a message", err, codes.PermissionDenied, key)
		_, err = e.support.AddSupportAttachment(ctxFor(viewer.id), &plansv1.AddSupportAttachmentRequest{UserId: viewer.id, TicketId: tk.GetId(), MessageId: first.GetId(), TenantId: uuid.NewString(),
			AssetId: uuid.NewString(), Filename: "shot.png", ContentType: "image/png", SizeBytes: 10})
		wantCode(t, "record an attachment", err, codes.PermissionDenied, key)
		// Nothing changed: still one message, still open.
		r, err := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: ada.id, TicketId: tk.GetId()})
		if err != nil || len(r.GetMessages()) != 1 || r.GetTicket().GetStatus() != "open" {
			t.Errorf("after the refusals: %v %v", err, r)
		}
	})

	t.Run("an administrator of the same account does all of it", func(t *testing.T) {
		if _, err := e.system.Exec(ctx, `UPDATE account_members SET role = 'admin' WHERE account_id = $1::uuid AND user_id = $2::uuid`, ada.account, viewer.id); err != nil {
			t.Fatal(err)
		}
		if _, err := e.support.ReplyMySupportTicket(ctx, &plansv1.ReplyMySupportTicketRequest{UserId: viewer.id, TicketId: tk.GetId(), Body: "me too"}); err != nil {
			t.Errorf("reply as an administrator: %v", err)
		}
		if _, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: viewer.id, AccountId: viewer.account, Subject: "Allowed now", Category: "other", Body: "x"}); err != nil {
			t.Errorf("open as an administrator: %v", err)
		}
		if _, err := e.support.RateMySupportTicket(ctx, &plansv1.RateMySupportTicketRequest{UserId: viewer.id, TicketId: solved.GetTicket().GetId(), Rating: 4}); err != nil {
			t.Errorf("rate as an administrator: %v", err)
		}
		// A person who wrote a message and was demoted since can no longer attach to it.
		if _, err := e.system.Exec(ctx, `UPDATE account_members SET role = 'viewer' WHERE account_id = $1::uuid AND user_id = $2::uuid`, ada.account, viewer.id); err != nil {
			t.Fatal(err)
		}
		_, err := e.support.AuthorizeSupportAttachment(ctxFor(viewer.id), &plansv1.AuthorizeSupportAttachmentRequest{UserId: viewer.id, TicketId: tk.GetId(), MessageId: first.GetId()})
		wantCode(t, "a demoted author attaching", err, codes.PermissionDenied, "identity.account.forbidden")
	})
}
