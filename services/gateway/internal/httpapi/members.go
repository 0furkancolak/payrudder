package httpapi

import (
	"net/http"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/gateway/internal/ratelimit"
)

// Members and invitations (phase 39). Like the rest of /app/v1 the caller is the session's user and the account is ?account_id= (default: the first one),
// checked against the session's memberships here and again in SQL by plans-svc. A body never names the acting user or the account. Roles are enforced
// by plans-svc: owner and administrator invite and manage, only an owner handles owners, a viewer may only read and leave.

func (s *Server) memberRoutes() []Route {
	return []Route{
		{"PATCH", "/app/v1/account", AccessSession, ratelimit.ClassWrite, s.accountRename},
		{"GET", "/app/v1/account/invitations", AccessSession, ratelimit.ClassRead, s.invitationList},
		{"POST", "/app/v1/account/invitations", AccessSession, ratelimit.ClassWrite, s.invitationCreate},
		{"DELETE", "/app/v1/account/invitations/{id}", AccessSession, ratelimit.ClassWrite, s.invitationRevoke},
		{"POST", "/app/v1/account/invitations/{id}/resend", AccessSession, ratelimit.ClassWrite, s.invitationResend},
		{"PATCH", "/app/v1/account/members/{user_id}", AccessSession, ratelimit.ClassWrite, s.memberRole},
		{"DELETE", "/app/v1/account/members/{user_id}", AccessSession, ratelimit.ClassWrite, s.memberRemove},
		{"POST", "/app/v1/invitations/accept", AccessSession, ratelimit.ClassWrite, s.invitationAcceptAsUser},
		{"POST", "/auth/invitations/lookup", AccessPublic, "", s.invitationLookup},
		{"POST", "/auth/invitations/accept", AccessPublic, "", s.invitationAccept},
	}
}

func (s *Server) accountRename(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id, err := accountOf(r, sess)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Identity.RenameMyAccount(r.Context(), &plansv1.RenameMyAccountRequest{UserId: sess.UserID, AccountId: id, Name: body.Name})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) invitationList(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id, err := accountOf(r, sess)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Identity.ListInvitations(r.Context(), &plansv1.ListInvitationsRequest{UserId: sess.UserID, AccountId: id})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) invitationCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id, err := accountOf(r, sess)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var body struct {
		Email  string `json:"email"`
		Role   string `json:"role"`
		Locale string `json:"locale"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Identity.InviteMember(r.Context(), &plansv1.InviteMemberRequest{UserId: sess.UserID, AccountId: id, Email: body.Email, Role: body.Role,
		Locale: localeOf(r, body.Locale)})
	respond(w, r, http.StatusCreated, resp.GetInvitation(), err)
}

func (s *Server) invitationRevoke(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id, err := accountOf(r, sess)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if _, err := s.d.Identity.RevokeInvitation(r.Context(), &plansv1.RevokeInvitationRequest{UserId: sess.UserID, AccountId: id, InvitationId: r.PathValue("id")}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) invitationResend(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id, err := accountOf(r, sess)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Identity.ResendInvitation(r.Context(), &plansv1.ResendInvitationRequest{UserId: sess.UserID, AccountId: id, InvitationId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetInvitation(), err)
}

func (s *Server) memberRole(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id, err := accountOf(r, sess)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Identity.UpdateMemberRole(r.Context(), &plansv1.UpdateMemberRoleRequest{UserId: sess.UserID, AccountId: id, MemberUserId: r.PathValue("user_id"), Role: body.Role})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) memberRemove(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id, err := accountOf(r, sess)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if _, err := s.d.Identity.RemoveMember(r.Context(), &plansv1.RemoveMemberRequest{UserId: sess.UserID, AccountId: id, MemberUserId: r.PathValue("user_id")}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// invitationAcceptAsUser is POST /app/v1/invitations/accept {token}: the signed-in person joins the account the invitation was sent for. The invitation
// must have been sent to the person's own address.
func (s *Server) invitationAcceptAsUser(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Identity.AcceptInvitationAsUser(r.Context(), &plansv1.AcceptInvitationAsUserRequest{UserId: sess.UserID, Token: body.Token})
	respond(w, r, http.StatusOK, resp, err)
}

// invitationLookup is POST /auth/invitations/lookup {token}: what a live invitation is for, so the accept page asks for the right thing. The token travels
// in a body, not in the URL.
func (s *Server) invitationLookup(w http.ResponseWriter, r *http.Request) {
	if !s.authAllowed(w, r, &s.tokenUses, tokenUsesPerMinute) {
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	resp, err := s.d.Identity.LookupInvitation(r.Context(), &plansv1.LookupInvitationRequest{Token: body.Token})
	respond(w, r, http.StatusOK, resp, err)
}

// invitationAccept is POST /auth/invitations/accept {token, password, display_name}: a person without an account joins as a new user and gets a session.
// An address that has an account is answered 409 identity.invite.sign_in_required; that person signs in and uses /app/v1/invitations/accept.
func (s *Server) invitationAccept(w http.ResponseWriter, r *http.Request) {
	if !s.authAllowed(w, r, &s.tokenUses, tokenUsesPerMinute) {
		return
	}
	var body struct {
		Token       string `json:"token"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		Locale      string `json:"locale"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	resp, err := s.d.Identity.AcceptInvitation(r.Context(), &plansv1.AcceptInvitationRequest{Token: body.Token, Password: body.Password, DisplayName: body.DisplayName,
		Locale: localeOf(r, body.Locale)})
	respond(w, r, http.StatusOK, resp, err)
}
