package httpapi

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// fakeSupport stands in for plans-svc's SupportService and keeps the last request of each call.
type fakeSupport struct {
	plansv1.UnimplementedSupportServiceServer
	mu   sync.Mutex
	last map[string]proto.Message
}

func (f *fakeSupport) remember(name string, m proto.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last == nil {
		f.last = map[string]proto.Message{}
	}
	f.last[name] = m
}

func (f *fakeSupport) got(name string) proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last[name]
}

func (f *fakeSupport) CountMySupportUnread(_ context.Context, r *plansv1.CountMySupportUnreadRequest) (*plansv1.CountMySupportUnreadResponse, error) {
	f.remember("CountMySupportUnread", r)
	return &plansv1.CountMySupportUnreadResponse{Unread: 3}, nil
}

func (f *fakeSupport) GetSupportReport(_ context.Context, r *plansv1.GetSupportReportRequest) (*plansv1.GetSupportReportResponse, error) {
	f.remember("GetSupportReport", r)
	return &plansv1.GetSupportReportResponse{Waiting: 4, Breached: 1, WindowDays: 30, ByStatus: []*plansv1.SupportCount{{Value: "open", Count: 4}}}, nil
}

func (f *fakeSupport) ListSupportOperators(_ context.Context, r *plansv1.ListSupportOperatorsRequest) (*plansv1.ListSupportOperatorsResponse, error) {
	f.remember("ListSupportOperators", r)
	return &plansv1.ListSupportOperatorsResponse{Operators: []*plansv1.SupportOperator{{Email: "ops@example.com", Name: "Ops"}}}, nil
}

func (f *fakeSupport) ListSupportCannedReplies(_ context.Context, r *plansv1.ListSupportCannedRepliesRequest) (*plansv1.ListSupportCannedRepliesResponse, error) {
	f.remember("ListSupportCannedReplies", r)
	return &plansv1.ListSupportCannedRepliesResponse{Replies: []*plansv1.SupportCannedReply{{Id: "c1", Title: "Logs", Body: "Send the ids."}}}, nil
}

func (f *fakeSupport) SaveSupportCannedReply(_ context.Context, r *plansv1.SaveSupportCannedReplyRequest) (*plansv1.SaveSupportCannedReplyResponse, error) {
	f.remember("SaveSupportCannedReply", r)
	return &plansv1.SaveSupportCannedReplyResponse{Reply: &plansv1.SupportCannedReply{Id: "c1", Title: r.GetTitle(), Body: r.GetBody(), UpdatedBy: r.GetAuthorName()}}, nil
}

func (f *fakeSupport) DeleteSupportCannedReply(_ context.Context, r *plansv1.DeleteSupportCannedReplyRequest) (*plansv1.DeleteSupportCannedReplyResponse, error) {
	f.remember("DeleteSupportCannedReply", r)
	return &plansv1.DeleteSupportCannedReplyResponse{}, nil
}

func (f *fakeSupport) UpdateSupportTicket(_ context.Context, r *plansv1.UpdateSupportTicketRequest) (*plansv1.UpdateSupportTicketResponse, error) {
	f.remember("UpdateSupportTicket", r)
	return &plansv1.UpdateSupportTicketResponse{Ticket: &plansv1.SupportTicket{Id: r.GetTicketId(), Tags: r.GetTags()}}, nil
}

func (f *fakeSupport) ListSupportTickets(_ context.Context, r *plansv1.ListSupportTicketsRequest) (*plansv1.ListSupportTicketsResponse, error) {
	f.remember("ListSupportTickets", r)
	return &plansv1.ListSupportTicketsResponse{}, nil
}

// lastSupport is the fake of the product server newProduct built last.
var lastSupport *fakeSupport

// The help desk's desk: the person's unread count is for the person of the session; the report, the operators and the canned replies are the operator's, and a
// canned reply is saved as the signed-in operator; a ticket's tags change when the body has the key, an empty list clearing them.
func TestSupportDeskRoutes(t *testing.T) {
	p := newProduct(t, nil, nil)
	fs := lastSupport

	if resp, m := p.call(t, "GET", "/app/v1/support/unread", tokAda, "", ""); resp.StatusCode != http.StatusOK || m["unread"] != float64(3) {
		t.Fatalf("unread: %d %v", resp.StatusCode, m)
	}
	if got := fs.got("CountMySupportUnread").(*plansv1.CountMySupportUnreadRequest).GetUserId(); got == "" {
		t.Error("the unread count was asked for nobody: the person is the session's, never the body's")
	}
	if resp, _ := p.call(t, "GET", "/app/v1/support/unread", "", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unread without a session: %d", resp.StatusCode)
	}

	for _, path := range []string{"/admin/v1/support/report", "/admin/v1/support/operators", "/admin/v1/support/canned-replies"} {
		if resp, _ := p.call(t, "GET", path, tokAda, "", ""); resp.StatusCode == http.StatusOK {
			t.Errorf("%s served a person who is no operator", path)
		}
		if resp, _ := p.call(t, "GET", path, operatorToken, "", ""); resp.StatusCode != http.StatusOK {
			t.Errorf("%s for the operator: %d", path, resp.StatusCode)
		}
	}
	if resp, m := p.call(t, "GET", "/admin/v1/support/report", operatorToken, "", ""); resp.StatusCode != http.StatusOK || m["waiting"] != float64(4) || m["breached"] != float64(1) {
		t.Errorf("report: %d %v", resp.StatusCode, m)
	}

	if resp, m := p.call(t, "POST", "/admin/v1/support/canned-replies", operatorToken, "", `{"title":"Logs","body":"Send the ids.","author_name":"forged"}`); resp.StatusCode != http.StatusCreated || m["title"] == nil && m["reply"] == nil {
		t.Fatalf("make a canned reply: %d %v", resp.StatusCode, m)
	}
	made := fs.got("SaveSupportCannedReply").(*plansv1.SaveSupportCannedReplyRequest)
	if made.GetId() != "" || made.GetAuthorName() == "forged" {
		t.Errorf("a new canned reply: %+v (the author is the gateway's to say)", made)
	}
	if resp, _ := p.call(t, "PUT", "/admin/v1/support/canned-replies/c1", operatorToken, "", `{"title":"Logs 2","body":"Send the ids."}`); resp.StatusCode != http.StatusOK {
		t.Errorf("change one: %d", resp.StatusCode)
	}
	if got := fs.got("SaveSupportCannedReply").(*plansv1.SaveSupportCannedReplyRequest); got.GetId() != "c1" {
		t.Errorf("a change names its id: %+v", got)
	}
	if resp, _ := p.call(t, "DELETE", "/admin/v1/support/canned-replies/c1", operatorToken, "", ""); resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete: %d", resp.StatusCode)
	}

	// Tags change when the body has the key; an empty list clears them; a body without the key leaves them. The flag of the proto is not the caller's to set.
	p.call(t, "PATCH", "/admin/v1/support/tickets/t1", operatorToken, "", `{"tags":["refund","vat"]}`)
	if got := fs.got("UpdateSupportTicket").(*plansv1.UpdateSupportTicketRequest); !got.GetSetTags() || len(got.GetTags()) != 2 {
		t.Errorf("tags set: %+v", got)
	}
	p.call(t, "PATCH", "/admin/v1/support/tickets/t1", operatorToken, "", `{"tags":[]}`)
	if got := fs.got("UpdateSupportTicket").(*plansv1.UpdateSupportTicketRequest); !got.GetSetTags() || len(got.GetTags()) != 0 {
		t.Errorf("tags cleared: %+v", got)
	}
	p.call(t, "PATCH", "/admin/v1/support/tickets/t1", operatorToken, "", `{"priority":"high","set_tags":true}`)
	if got := fs.got("UpdateSupportTicket").(*plansv1.UpdateSupportTicketRequest); got.GetSetTags() || got.GetPriority() != "high" {
		t.Errorf("a body without tags: %+v", got)
	}
	p.call(t, "GET", "/admin/v1/support/tickets?tag=vat&status=active", operatorToken, "", "")
	if got := fs.got("ListSupportTickets").(*plansv1.ListSupportTicketsRequest); got.GetTag() != "vat" || got.GetStatus() != "active" {
		t.Errorf("list by tag: %+v", got)
	}
}
