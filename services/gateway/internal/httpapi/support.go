package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// The help desk. A signed-in person opens tickets for their account and answers on them (the tickets of every account they belong to); the operator works the
// whole queue: answers publicly or leaves a note the person never sees, and sets status, priority and assignee. Nothing the browser sends decides whose ticket it
// is: the person's id comes from the session, and plans-svc checks the account's membership.

func (s *Server) supportRoutes() []Route {
	return []Route{
		{"GET", "/app/v1/support/tickets", AccessSession, ratelimit.ClassRead, s.supportMyList},
		{"POST", "/app/v1/support/tickets", AccessSession, ratelimit.ClassWrite, s.supportMyOpen},
		{"GET", "/app/v1/support/unread", AccessSession, ratelimit.ClassRead, s.supportMyUnread},
		{"GET", "/app/v1/support/tickets/{id}", AccessSession, ratelimit.ClassRead, s.supportMyGet},
		{"POST", "/app/v1/support/tickets/{id}/replies", AccessSession, ratelimit.ClassWrite, s.supportMyReply},
		{"PUT", "/app/v1/support/tickets/{id}/status", AccessSession, ratelimit.ClassWrite, s.supportMyStatus},

		{"GET", "/admin/v1/support/tickets", AccessOperator, "", s.supportList},
		{"GET", "/admin/v1/support/tickets/{id}", AccessOperator, "", s.supportGet},
		{"PATCH", "/admin/v1/support/tickets/{id}", AccessOperator, "", s.supportUpdate},
		{"POST", "/admin/v1/support/tickets/{id}/replies", AccessOperator, "", s.supportReply},
		{"GET", "/admin/v1/support/report", AccessOperator, "", s.supportReport},
		{"GET", "/admin/v1/support/operators", AccessOperator, "", s.supportOperators},
		{"GET", "/admin/v1/support/canned-replies", AccessOperator, "", s.supportCannedList},
		{"POST", "/admin/v1/support/canned-replies", AccessOperator, "", s.supportCannedSave},
		{"PUT", "/admin/v1/support/canned-replies/{id}", AccessOperator, "", s.supportCannedSave},
		{"DELETE", "/admin/v1/support/canned-replies/{id}", AccessOperator, "", s.supportCannedDelete},
	}
}

func (s *Server) supportMyList(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	resp, err := s.d.Support.ListMySupportTickets(r.Context(), &plansv1.ListMySupportTicketsRequest{UserId: sess.UserID, Status: r.URL.Query().Get("status")})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) supportMyOpen(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	req := &plansv1.OpenMySupportTicketRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.UserId = sess.UserID
	resp, err := s.d.Support.OpenMySupportTicket(r.Context(), req)
	respond(w, r, http.StatusCreated, resp, err)
}

func (s *Server) supportMyGet(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	resp, err := s.d.Support.GetMySupportTicket(r.Context(), &plansv1.GetMySupportTicketRequest{UserId: sess.UserID, TicketId: r.PathValue("id")})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) supportMyReply(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	req := &plansv1.ReplyMySupportTicketRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.UserId, req.TicketId = sess.UserID, r.PathValue("id")
	resp, err := s.d.Support.ReplyMySupportTicket(r.Context(), req)
	respond(w, r, http.StatusCreated, resp, err)
}

func (s *Server) supportMyStatus(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	req := &plansv1.SetMySupportTicketStatusRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.UserId, req.TicketId = sess.UserID, r.PathValue("id")
	resp, err := s.d.Support.SetMySupportTicketStatus(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) supportList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	breached, _ := strconv.ParseBool(q.Get("sla_breached"))
	resp, err := s.d.Support.ListSupportTickets(r.Context(), &plansv1.ListSupportTicketsRequest{Status: q.Get("status"), Priority: q.Get("priority"), Assignee: q.Get("assignee"), Search: q.Get("search"), SlaBreached: breached, Tag: q.Get("tag")})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) supportGet(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Support.GetSupportTicket(r.Context(), &plansv1.GetSupportTicketRequest{TicketId: r.PathValue("id")})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) supportUpdate(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.UpdateSupportTicketRequest{}
	raw, err := readBody(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	// Tags are changed when the body has the key (an empty list clears them); the proto's own flag is not part of the REST body.
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(raw, &keys)
	_, req.SetTags = keys["tags"]
	// The signed-in operator's address is who the ticket's history names (the machine token: "Payrudder support").
	req.TicketId, req.AuthorName = r.PathValue("id"), operatorFrom(r.Context())
	resp, err := s.d.Support.UpdateSupportTicket(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

// supportReply answers as the signed-in operator (their address is the author shown to the person; the machine token answers as "Payrudder support").
func (s *Server) supportReply(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ReplySupportTicketRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.TicketId, req.AuthorName = r.PathValue("id"), operatorFrom(r.Context())
	resp, err := s.d.Support.ReplySupportTicket(r.Context(), req)
	respond(w, r, http.StatusCreated, resp, err)
}

// supportMyUnread is the number of the person's tickets with an answer or a status change they have not read (the badge on the help page's link).
func (s *Server) supportMyUnread(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Support.CountMySupportUnread(r.Context(), &plansv1.CountMySupportUnreadRequest{UserId: sessionFrom(r.Context()).UserID})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

// supportReport is the operator's report: the queue and the first-answer times of the last 30 days (also what the inbox's badge counts).
func (s *Server) supportReport(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Support.GetSupportReport(r.Context(), &plansv1.GetSupportReportRequest{})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

// supportOperators lists the addresses a ticket can be assigned to.
func (s *Server) supportOperators(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Support.ListSupportOperators(r.Context(), &plansv1.ListSupportOperatorsRequest{})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) supportCannedList(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Support.ListSupportCannedReplies(r.Context(), &plansv1.ListSupportCannedRepliesRequest{})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

// supportCannedSave makes a canned reply (POST) or changes one (PUT /{id}), as the signed-in operator.
func (s *Server) supportCannedSave(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.SaveSupportCannedReplyRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Id, req.AuthorName = r.PathValue("id"), operatorFrom(r.Context())
	resp, err := s.d.Support.SaveSupportCannedReply(r.Context(), req)
	code := http.StatusCreated
	if req.Id != "" {
		code = http.StatusOK
	}
	respond(w, r, code, resp, err)
}

func (s *Server) supportCannedDelete(w http.ResponseWriter, r *http.Request) {
	_, err := s.d.Support.DeleteSupportCannedReply(r.Context(), &plansv1.DeleteSupportCannedReplyRequest{Id: r.PathValue("id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
