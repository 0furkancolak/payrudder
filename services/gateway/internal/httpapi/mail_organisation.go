package httpapi

import (
	"net/http"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// The organisation's e-mail accounts (docs/plans/10, ADR 0026): an account several apps of one organisation use, set up once. Like members, the caller is the session's
// user and the organisation is ?account_id= (default: the first one); a body never names either. plans-svc enforces the role: an owner or an administrator writes, every
// other member reads. The key is sent once and never comes back.

func (s *Server) orgMailRoutes() []Route {
	return []Route{
		{"GET", "/app/v1/account/integrations/mail", AccessSession, ratelimit.ClassRead, s.withAccount(s.orgMailList)},
		{"PUT", "/app/v1/account/integrations/mail", AccessSession, ratelimit.ClassWrite, s.withAccount(s.orgMailPut)},
		{"DELETE", "/app/v1/account/integrations/mail", AccessSession, ratelimit.ClassWrite, s.withAccount(s.orgMailDelete)},
		{"POST", "/app/v1/account/integrations/mail/test", AccessSession, ratelimit.ClassWrite, s.withAccount(s.orgMailTest)},
		{"POST", "/app/v1/account/integrations/mail/bind", AccessSession, ratelimit.ClassWrite, s.withAccount(s.orgMailBind)},
		{"POST", "/app/v1/account/integrations/mail/unbind", AccessSession, ratelimit.ClassWrite, s.withAccount(s.orgMailUnbind)},
	}
}

func (s *Server) orgMailList(w http.ResponseWriter, r *http.Request, sess session, id string) {
	resp, err := s.d.OrganisationMail.ListOrganisationMailAccounts(r.Context(), &plansv1.ListOrganisationMailAccountsRequest{UserId: sess.UserID, AccountId: id})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) orgMailPut(w http.ResponseWriter, r *http.Request, sess session, id string) {
	req := &plansv1.PutOrganisationMailAccountRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.UserId, req.AccountId = sess.UserID, id
	noStore(w)
	resp, err := s.d.OrganisationMail.PutOrganisationMailAccount(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetAccount(), err)
}

// orgMailDelete removes the account named by ?account=. 409 plans.mail_integration.shared_in_use names the apps that still use it.
func (s *Server) orgMailDelete(w http.ResponseWriter, r *http.Request, sess session, id string) {
	noStore(w)
	if _, err := s.d.OrganisationMail.DeleteOrganisationMailAccount(r.Context(), &plansv1.DeleteOrganisationMailAccountRequest{UserId: sess.UserID, AccountId: id, Account: r.URL.Query().Get("account")}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) orgMailTest(w http.ResponseWriter, r *http.Request, sess session, id string) {
	req := &plansv1.TestOrganisationMailAccountRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.UserId, req.AccountId = sess.UserID, id
	noStore(w)
	resp, err := s.d.OrganisationMail.TestOrganisationMailAccount(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) orgMailBind(w http.ResponseWriter, r *http.Request, sess session, id string) {
	req := &plansv1.BindOrganisationMailAccountRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.UserId, req.AccountId = sess.UserID, id
	noStore(w)
	resp, err := s.d.OrganisationMail.BindOrganisationMailAccount(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) orgMailUnbind(w http.ResponseWriter, r *http.Request, sess session, id string) {
	req := &plansv1.UnbindOrganisationMailAccountRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.UserId, req.AccountId = sess.UserID, id
	noStore(w)
	resp, err := s.d.OrganisationMail.UnbindOrganisationMailAccount(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}
