package httpapi

import (
	"net/http"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// The blocklists (plan 13). The app's own list is on the tenant API (customers:read / customers:write); the operator's platform-wide list is the operator's. Who added an entry
// is the signed-in person, the API key or the operator: a body cannot name anyone else.

type blocklistBody struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Note  string `json:"note"`
}

func (s *Server) listBlocklist(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.ListBlocklist(r.Context(), &plansv1.ListBlocklistRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) addBlocklistEntry(w http.ResponseWriter, r *http.Request) {
	var b blocklistBody
	if err := decodeJSON(r, &b); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.AddBlocklistEntry(r.Context(), &plansv1.AddBlocklistEntryRequest{Kind: b.Kind, Value: b.Value, Note: b.Note, Actor: s.tenantActorOf(r)})
	respond(w, r, http.StatusCreated, resp.GetEntry(), err)
}

func (s *Server) removeBlocklistEntry(w http.ResponseWriter, r *http.Request) {
	if _, err := s.d.Plans.RemoveBlocklistEntry(r.Context(), &plansv1.RemoveBlocklistEntryRequest{Id: r.PathValue("id"), Actor: s.tenantActorOf(r)}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPlatformBlocklist(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Admin.ListPlatformBlocklist(r.Context(), &plansv1.ListPlatformBlocklistRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) addPlatformBlocklistEntry(w http.ResponseWriter, r *http.Request) {
	var b blocklistBody
	if err := decodeJSON(r, &b); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Admin.AddPlatformBlocklistEntry(r.Context(), &plansv1.AddPlatformBlocklistEntryRequest{Kind: b.Kind, Value: b.Value, Note: b.Note, Actor: actorOf(r)})
	respond(w, r, http.StatusCreated, resp.GetEntry(), err)
}

func (s *Server) removePlatformBlocklistEntry(w http.ResponseWriter, r *http.Request) {
	if _, err := s.d.Admin.RemovePlatformBlocklistEntry(r.Context(), &plansv1.RemovePlatformBlocklistEntryRequest{Id: r.PathValue("id"), Actor: actorOf(r)}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
