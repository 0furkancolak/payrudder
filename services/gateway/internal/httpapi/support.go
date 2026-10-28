package httpapi

import (
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
		{"GET", "/app/v1/support/tickets/{id}", AccessSession, ratelimit.ClassRead, s.supportMyGet},
		{"POST", "/app/v1/support/tickets/{id}/replies", AccessSession, ratelimit.ClassWrite, s.supportMyReply},
		{"PUT", "/app/v1/support/tickets/{id}/status", AccessSession, ratelimit.ClassWrite, s.supportMyStatus},

		{"GET", "/admin/v1/support/tickets", AccessOperator, "", s.supportList},
		{"GET", "/admin/v1/support/tickets/{id}", AccessOperator, "", s.supportGet},
		{"PATCH", "/admin/v1/support/tickets/{id}", AccessOperator, "", s.supportUpdate},
		{"POST", "/admin/v1/support/tickets/{id}/replies", AccessOperator, "", s.supportReply},
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
	resp, err := s.d.Support.ListSupportTickets(r.Context(), &plansv1.ListSupportTicketsRequest{Status: q.Get("status"), Priority: q.Get("priority"), Assignee: q.Get("assignee"), Search: q.Get("search"), SlaBreached: breached})
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
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.TicketId = r.PathValue("id")
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
