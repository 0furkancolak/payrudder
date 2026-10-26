package httpapi

import (
	"net/http"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func (s *Server) createAccount(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreateAccountRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Admin.CreateAccount(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetAccount(), err)
}

func (s *Server) createApp(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreateAppRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.AccountId = r.PathValue("id")
	resp, err := s.d.Admin.CreateApp(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetApp(), err)
}

func (s *Server) listApps(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Admin.ListApps(r.Context(), &plansv1.ListAppsRequest{AccountId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getTenant(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Admin.GetTenant(r.Context(), &plansv1.GetTenantRequest{TenantId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetTenant(), err)
}

func (s *Server) updateTenant(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.UpdateTenantSettingsRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.TenantId = r.PathValue("id")
	resp, err := s.d.Admin.UpdateTenantSettings(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetTenant(), err)
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreateApiKeyRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.TenantId = r.PathValue("id")
	resp, err := s.d.Admin.CreateApiKey(r.Context(), req)
	respond(w, r, http.StatusCreated, resp, err)
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Admin.RevokeApiKey(r.Context(), &plansv1.RevokeApiKeyRequest{ApiKeyId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetApiKey(), err)
}
