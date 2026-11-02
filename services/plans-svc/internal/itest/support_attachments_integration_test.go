//go:build integration

package itest

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
)

// testSupportAttachments covers the files on the help desk's messages (plan 12): who may attach to which message, where the file is stored, the limits per ticket
// and per account, and which files each side may fetch. The bytes themselves are assets-svc's (its own integration test); here the record and the rules.
func testSupportAttachments(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ada := e.verified(t, "attach-ada@example.com", "Attach Co")
	mate := e.signUp(t, "attach-mate@example.com", "Mate Co")
	if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'admin')`, ada.account, mate.GetUser().GetId()); err != nil {
		t.Fatal(err)
	}
	stranger := e.verified(t, "attach-stranger@example.com", "Stranger Co")
	if _, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: stranger.id, AccountId: stranger.account, Name: "Elsewhere", Currency: "EUR", Timezone: "Europe/Berlin"}); err != nil {
		t.Fatal(err)
	}
	open := func(p person, subject string) (*plansv1.SupportTicket, *plansv1.SupportMessage) {
		t.Helper()
		r, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: p.id, AccountId: p.account, Subject: subject, Category: "bug", Body: "See the screenshot."})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetTicket(), r.GetMessage()
	}
	tk, first := open(ada, "Checkout fails")
	if first.GetId() == "" || first.GetAuthorKind() != "customer" {
		t.Fatalf("the first message is returned so a file can be attached to it: %+v", first)
	}
	authorize := func(user, ticket, message string) (*plansv1.SupportAttachmentTarget, error) {
		r, err := e.support.AuthorizeSupportAttachment(ctxFor(user), &plansv1.AuthorizeSupportAttachmentRequest{UserId: user, TicketId: ticket, MessageId: message})
		return r.GetTarget(), err
	}
	add := func(user, ticket, message, tenantID string, size int64) (*plansv1.SupportAttachment, error) {
		r, err := e.support.AddSupportAttachment(ctxFor(user), &plansv1.AddSupportAttachmentRequest{UserId: user, TicketId: ticket, MessageId: message, TenantId: tenantID, AssetId: uuid.NewString(),
			Filename: "shot.png", ContentType: "image/png", SizeBytes: size})
		return r.GetAttachment(), err
	}

	t.Run("an organisation without an app has nowhere to store a file", func(t *testing.T) {
		_, err := authorize(ada.id, tk.GetId(), first.GetId())
		wantCode(t, "no app yet", err, codes.FailedPrecondition, "support.attachment.no_app")
	})

	if _, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: ada.id, AccountId: ada.account, Name: "Shop", Currency: "EUR", Timezone: "Europe/Berlin"}); err != nil {
		t.Fatal(err)
	}
	var liveTenant string
	if err := e.system.QueryRow(ctx, `SELECT id::text FROM tenants WHERE account_id = $1::uuid AND environment = 'live' ORDER BY created_at LIMIT 1`, ada.account).Scan(&liveTenant); err != nil {
		t.Fatal(err)
	}
	var strangerTenant string
	if err := e.system.QueryRow(ctx, `SELECT id::text FROM tenants WHERE account_id = $1::uuid LIMIT 1`, stranger.account).Scan(&strangerTenant); err != nil {
		t.Fatal(err)
	}

	t.Run("the file goes under the account's first app's live tenant", func(t *testing.T) {
		target, err := authorize(ada.id, tk.GetId(), first.GetId())
		if err != nil || target.GetTenantId() != liveTenant || target.GetEnvironment() != "live" || target.GetTimezone() != "Europe/Berlin" || target.GetTier() == "" {
			t.Fatalf("target: %v %+v", err, target)
		}
	})

	t.Run("only the person who wrote the message attaches to it, and the operator only to the operator's own", func(t *testing.T) {
		_, err := authorize(mate.GetUser().GetId(), tk.GetId(), first.GetId())
		wantCode(t, "a teammate on another's message", err, codes.NotFound, "support.ticket.not_found")
		_, err = authorize(stranger.id, tk.GetId(), first.GetId())
		wantCode(t, "a stranger", err, codes.NotFound, "support.ticket.not_found")
		_, err = authorize(ada.id, tk.GetId(), uuid.NewString())
		wantCode(t, "a message that is not there", err, codes.NotFound, "support.ticket.not_found")
		other, _ := open(ada, "Another ticket")
		_, err = authorize(ada.id, other.GetId(), first.GetId())
		wantCode(t, "a message of another ticket", err, codes.NotFound, "support.ticket.not_found")
		// The operator context is required when no user is named, and it may not attach to the person's message.
		_, err = e.support.AuthorizeSupportAttachment(ctx, &plansv1.AuthorizeSupportAttachmentRequest{TicketId: tk.GetId(), MessageId: first.GetId()})
		if err == nil {
			t.Fatal("a call with neither a user nor the operator context was served")
		}
		_, err = e.support.AuthorizeSupportAttachment(operator(), &plansv1.AuthorizeSupportAttachmentRequest{TicketId: tk.GetId(), MessageId: first.GetId()})
		wantCode(t, "the operator on the person's message", err, codes.NotFound, "support.ticket.not_found")
	})

	t.Run("a recorded file is on the message for both sides; a stranger's tenant or a bad file is refused", func(t *testing.T) {
		_, err := add(ada.id, tk.GetId(), first.GetId(), strangerTenant, 100)
		wantCode(t, "another account's tenant", err, codes.InvalidArgument, "support.attachment.rejected")
		for _, bad := range []struct {
			name, ct string
			size     int64
		}{{"a zip", "application/zip", 100}, {"an empty file", "image/png", 0}, {"6 MiB", "image/png", 6 << 20}} {
			_, err := e.support.AddSupportAttachment(ctx, &plansv1.AddSupportAttachmentRequest{UserId: ada.id, TicketId: tk.GetId(), MessageId: first.GetId(), TenantId: liveTenant, AssetId: uuid.NewString(),
				Filename: "x", ContentType: bad.ct, SizeBytes: bad.size})
			wantCode(t, bad.name, err, codes.InvalidArgument, "support.attachment.rejected")
		}
		a, err := add(ada.id, tk.GetId(), first.GetId(), liveTenant, 2048)
		if err != nil || a.GetId() == "" || a.GetFilename() != "shot.png" {
			t.Fatalf("add: %v %+v", err, a)
		}
		mine, err := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: ada.id, TicketId: tk.GetId()})
		if err != nil || len(mine.GetMessages()[0].GetAttachments()) != 1 || mine.GetMessages()[0].GetAttachments()[0].GetId() != a.GetId() || mine.GetMessages()[0].GetAttachments()[0].GetSizeBytes() != 2048 {
			t.Fatalf("the person's thread: %v %+v", err, mine.GetMessages())
		}
		all, err := e.support.GetSupportTicket(operator(), &plansv1.GetSupportTicketRequest{TicketId: tk.GetId()})
		if err != nil || len(all.GetMessages()[0].GetAttachments()) != 1 {
			t.Fatalf("the operator's thread: %v %+v", err, all.GetMessages())
		}
		got, err := e.support.GetSupportAttachment(ctx, &plansv1.GetSupportAttachmentRequest{UserId: mate.GetUser().GetId(), TicketId: tk.GetId(), AttachmentId: a.GetId()})
		if err != nil || got.GetTarget().GetTenantId() != liveTenant || got.GetTarget().GetAssetId() == "" || got.GetTarget().GetFilename() != "shot.png" {
			t.Fatalf("a teammate downloads: %v %+v", err, got)
		}
		_, err = e.support.GetSupportAttachment(ctx, &plansv1.GetSupportAttachmentRequest{UserId: stranger.id, TicketId: tk.GetId(), AttachmentId: a.GetId()})
		wantCode(t, "a stranger downloads", err, codes.NotFound, "support.attachment.not_found")
		_, err = e.support.GetSupportAttachment(ctx, &plansv1.GetSupportAttachmentRequest{UserId: ada.id, TicketId: tk.GetId(), AttachmentId: uuid.NewString()})
		wantCode(t, "a file that is not there", err, codes.NotFound, "support.attachment.not_found")
		if _, err := e.system.Exec(ctx, `UPDATE support_attachments SET filename = 'x'`); err == nil {
			t.Error("the record can be edited")
		}
		if _, err := e.system.Exec(ctx, `DELETE FROM support_attachments`); err == nil {
			t.Error("the record can be deleted")
		}
	})

	t.Run("a file on the operator's internal note is the operator's alone", func(t *testing.T) {
		note, err := e.support.ReplySupportTicket(operator(), &plansv1.ReplySupportTicketRequest{TicketId: tk.GetId(), Body: "Log of the failing call.", Internal: true, AuthorName: "Sam"})
		if err != nil || note.GetMessage().GetId() == "" || !note.GetMessage().GetInternal() {
			t.Fatalf("note: %v %+v", err, note)
		}
		a, err := e.support.AddSupportAttachment(operator(), &plansv1.AddSupportAttachmentRequest{TicketId: tk.GetId(), MessageId: note.GetMessage().GetId(), TenantId: liveTenant, AssetId: uuid.NewString(),
			Filename: "trace.txt", ContentType: "text/plain", SizeBytes: 900})
		if err != nil {
			t.Fatal(err)
		}
		mine, err := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: ada.id, TicketId: tk.GetId()})
		if err != nil || len(mine.GetMessages()) != 1 {
			t.Fatalf("the person's thread: %v %+v", err, mine.GetMessages())
		}
		_, err = e.support.GetSupportAttachment(ctx, &plansv1.GetSupportAttachmentRequest{UserId: ada.id, TicketId: tk.GetId(), AttachmentId: a.GetAttachment().GetId()})
		wantCode(t, "the person fetches a note's file", err, codes.NotFound, "support.attachment.not_found")
		all, err := e.support.GetSupportAttachment(operator(), &plansv1.GetSupportAttachmentRequest{TicketId: tk.GetId(), AttachmentId: a.GetAttachment().GetId()})
		if err != nil || all.GetTarget().GetFilename() != "trace.txt" {
			t.Fatalf("the operator fetches it: %v %+v", err, all)
		}
		// A public answer takes a file too, and the person sees it.
		pub, err := e.support.ReplySupportTicket(operator(), &plansv1.ReplySupportTicketRequest{TicketId: tk.GetId(), Body: "Try this build.", AuthorName: "Sam"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.support.AddSupportAttachment(operator(), &plansv1.AddSupportAttachmentRequest{TicketId: tk.GetId(), MessageId: pub.GetMessage().GetId(), TenantId: liveTenant, AssetId: uuid.NewString(),
			Filename: "fix.pdf", ContentType: "application/pdf", SizeBytes: 5000}); err != nil {
			t.Fatal(err)
		}
		mine, _ = e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: ada.id, TicketId: tk.GetId()})
		if len(mine.GetMessages()) != 2 || len(mine.GetMessages()[1].GetAttachments()) != 1 || mine.GetMessages()[1].GetAttachments()[0].GetFilename() != "fix.pdf" {
			t.Fatalf("the person sees the public answer's file: %+v", mine.GetMessages())
		}
	})

	t.Run("a ticket holds ten files, also when they arrive at once, and an organisation keeps 50 MiB", func(t *testing.T) {
		busy := e.verified(t, "attach-busy@example.com", "Busy Co")
		if _, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: busy.id, AccountId: busy.account, Name: "Busy", Currency: "EUR", Timezone: "UTC"}); err != nil {
			t.Fatal(err)
		}
		var tenantID string
		if err := e.system.QueryRow(ctx, `SELECT id::text FROM tenants WHERE account_id = $1::uuid AND environment = 'live'`, busy.account).Scan(&tenantID); err != nil {
			t.Fatal(err)
		}
		bt, bm := open(busy, "Many files")
		for i := 0; i < domain.SupportMaxAttachments; i++ {
			if _, err := add(busy.id, bt.GetId(), bm.GetId(), tenantID, domain.SupportMaxAttachmentBytes); err != nil {
				t.Fatalf("file %d: %v", i+1, err)
			}
		}
		_, err := authorize(busy.id, bt.GetId(), bm.GetId())
		wantCode(t, "the 11th file (authorize)", err, codes.ResourceExhausted, "support.attachment.too_many")
		_, err = add(busy.id, bt.GetId(), bm.GetId(), tenantID, 10)
		wantCode(t, "the 11th file (add)", err, codes.ResourceExhausted, "support.attachment.too_many")
		// Ten files of 5 MiB are 50 MiB: that is the account's whole share, so a second ticket gets none.
		bt2, bm2 := open(busy, "More files")
		_, err = add(busy.id, bt2.GetId(), bm2.GetId(), tenantID, 1)
		wantCode(t, "over the account's share", err, codes.ResourceExhausted, "support.attachment.account_full")

		// The count and the insert are one step under the ticket's lock: twelve files at once leave exactly ten.
		race := e.verified(t, "attach-race@example.com", "Race Co")
		if _, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: race.id, AccountId: race.account, Name: "Race", Currency: "EUR", Timezone: "UTC"}); err != nil {
			t.Fatal(err)
		}
		if err := e.system.QueryRow(ctx, `SELECT id::text FROM tenants WHERE account_id = $1::uuid AND environment = 'live'`, race.account).Scan(&tenantID); err != nil {
			t.Fatal(err)
		}
		rt, rm := open(race, "At the same moment")
		done := make(chan error, 12)
		for i := 0; i < 12; i++ {
			go func() { _, err := add(race.id, rt.GetId(), rm.GetId(), tenantID, 100); done <- err }()
		}
		refused := 0
		for i := 0; i < 12; i++ {
			if err := <-done; err != nil {
				refused++
			}
		}
		var n int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM support_attachments WHERE ticket_id = $1::uuid`, rt.GetId()).Scan(&n); err != nil || n != domain.SupportMaxAttachments || refused != 2 {
			t.Errorf("stored %d (%v), refused %d, want %d and 2", n, err, refused, domain.SupportMaxAttachments)
		}
	})

	t.Run("a closed ticket takes no file", func(t *testing.T) {
		closed := "closed"
		if _, err := e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: tk.GetId(), Status: &closed}); err != nil {
			t.Fatal(err)
		}
		_, err := authorize(ada.id, tk.GetId(), first.GetId())
		wantCode(t, "authorize on a closed ticket", err, codes.FailedPrecondition, "support.ticket.closed")
	})
}

// ctxFor is the operator context when no user is named, else a plain one: the person's calls carry no operator mark.
func ctxFor(user string) context.Context {
	if user == "" {
		return operator()
	}
	return context.Background()
}
