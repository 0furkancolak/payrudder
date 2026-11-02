package httpapi

import (
	"context"
	"net/http"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func (f *fakePlans) ListBlocklist(context.Context, *plansv1.ListBlocklistRequest) (*plansv1.ListBlocklistResponse, error) {
	return &plansv1.ListBlocklistResponse{Entries: []*plansv1.BlocklistEntry{{Id: "b1", Kind: "email", Value: "a@example.com", CreatedBy: "ada@example.com", CreateTime: timestamppb.Now()}}}, nil
}

func (f *fakePlans) AddBlocklistEntry(_ context.Context, req *plansv1.AddBlocklistEntryRequest) (*plansv1.AddBlocklistEntryResponse, error) {
	f.lastListAdd = req
	return &plansv1.AddBlocklistEntryResponse{Entry: &plansv1.BlocklistEntry{Id: "b1", Kind: req.GetKind(), Value: req.GetValue(), Note: req.GetNote(), CreatedBy: req.GetActor()}}, nil
}

func (f *fakePlans) RemoveBlocklistEntry(_ context.Context, req *plansv1.RemoveBlocklistEntryRequest) (*plansv1.RemoveBlocklistEntryResponse, error) {
	f.lastListDel = req
	return &plansv1.RemoveBlocklistEntryResponse{}, nil
}

func (f *fakePlans) ListPlatformBlocklist(context.Context, *plansv1.ListPlatformBlocklistRequest) (*plansv1.ListPlatformBlocklistResponse, error) {
	return &plansv1.ListPlatformBlocklistResponse{}, nil
}

func (f *fakePlans) AddPlatformBlocklistEntry(_ context.Context, req *plansv1.AddPlatformBlocklistEntryRequest) (*plansv1.AddPlatformBlocklistEntryResponse, error) {
	f.lastPlatAdd = req
	return &plansv1.AddPlatformBlocklistEntryResponse{Entry: &plansv1.BlocklistEntry{Id: "p1", Kind: req.GetKind(), Value: req.GetValue(), CreatedBy: req.GetActor()}}, nil
}

func (f *fakePlans) RemovePlatformBlocklistEntry(context.Context, *plansv1.RemovePlatformBlocklistEntryRequest) (*plansv1.RemovePlatformBlocklistEntryResponse, error) {
	return &plansv1.RemovePlatformBlocklistEntryResponse{}, nil
}

// The app's blocklist is on the tenant API, recorded for who is authenticated (a body cannot name anyone else), and needs customers:read / customers:write; the operator's is
// the operator's and an API key cannot reach it.
func TestBlocklistRoutes(t *testing.T) {
	ts, f := newTestServer(t)
	if r, body := do(t, ts, http.MethodGet, "/v1/blocklist", "keyed-key", ""); r.StatusCode != http.StatusOK || body["entries"] == nil {
		t.Errorf("list: %d %v", r.StatusCode, body)
	}
	r, body := do(t, ts, http.MethodPost, "/v1/blocklist", "keyed-key", `{"kind":"email","value":"a@example.com","note":"stole a card","actor":"forged@example.com"}`)
	if r.StatusCode != http.StatusCreated || f.lastListAdd.GetActor() != "key:key-123" || f.lastListAdd.GetKind() != "email" || f.lastListAdd.GetNote() != "stole a card" || body["id"] != "b1" {
		t.Errorf("add: %d %v %v", r.StatusCode, body, f.lastListAdd)
	}
	if r, _ := do(t, ts, http.MethodDelete, "/v1/blocklist/b1", "keyed-key", ""); r.StatusCode != http.StatusNoContent || f.lastListDel.GetId() != "b1" || f.lastListDel.GetActor() != "key:key-123" {
		t.Errorf("remove: %d %v", r.StatusCode, f.lastListDel)
	}
	// customers:read reads the list, customers:write changes it: a read-only key cannot add, a key with neither cannot read.
	if r, _ := do(t, ts, http.MethodPost, "/v1/blocklist", "read-key", `{"kind":"email","value":"a@example.com"}`); r.StatusCode != http.StatusForbidden {
		t.Errorf("a read-only key adding: %d", r.StatusCode)
	}
	if r, _ := do(t, ts, http.MethodGet, "/v1/blocklist", "read-key", ""); r.StatusCode != http.StatusOK {
		t.Errorf("a read-only key reading: %d", r.StatusCode)
	}
	if r, _ := do(t, ts, http.MethodGet, "/v1/blocklist", "audit-key", ""); r.StatusCode != http.StatusForbidden {
		t.Errorf("a key without customers:read: %d", r.StatusCode)
	}
	// A frozen tenant's key reads and changes nothing.
	if r, _ := do(t, ts, http.MethodPost, "/v1/blocklist", "frozen-key", `{"kind":"email","value":"a@example.com"}`); r.StatusCode != http.StatusForbidden {
		t.Errorf("a frozen tenant adding: %d", r.StatusCode)
	}
}

func TestPlatformBlocklistIsTheOperators(t *testing.T) {
	ts, f := newTestServer(t)
	if r, _ := do(t, ts, http.MethodGet, "/admin/v1/blocklist", "keyed-key", ""); r.StatusCode == http.StatusOK {
		t.Error("an API key read the operator's blocklist")
	}
	r, body := do(t, ts, http.MethodPost, "/admin/v1/blocklist", operatorToken, `{"kind":"tax_id","value":"TR1","note":"ring"}`)
	if r.StatusCode != http.StatusCreated || f.lastPlatAdd.GetActor() != "operator" || f.lastPlatAdd.GetKind() != "tax_id" || body["id"] != "p1" {
		t.Errorf("add: %d %v %v", r.StatusCode, body, f.lastPlatAdd)
	}
	if r, _ := do(t, ts, http.MethodGet, "/admin/v1/blocklist", operatorToken, ""); r.StatusCode != http.StatusOK {
		t.Errorf("list: %d", r.StatusCode)
	}
	if r, _ := do(t, ts, http.MethodDelete, "/admin/v1/blocklist/p1", operatorToken, ""); r.StatusCode != http.StatusNoContent {
		t.Errorf("remove: %d", r.StatusCode)
	}
}
