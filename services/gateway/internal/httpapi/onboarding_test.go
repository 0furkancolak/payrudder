package httpapi

import (
	"context"
	"testing"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func (f *fakeIdentity) GetMyOnboarding(_ context.Context, req *plansv1.GetMyOnboardingRequest) (*plansv1.GetMyOnboardingResponse, error) {
	f.seen(req.GetUserId())
	f.mu.Lock()
	f.onboardingGets = append(f.onboardingGets, req)
	f.mu.Unlock()
	return &plansv1.GetMyOnboardingResponse{Onboarding: &plansv1.Onboarding{SeenTours: []string{"customers"}}}, nil
}

// UpdateMyOnboarding echoes the change as the merged record; the merge itself is plans-svc's and is tested there.
func (f *fakeIdentity) UpdateMyOnboarding(_ context.Context, req *plansv1.UpdateMyOnboardingRequest) (*plansv1.UpdateMyOnboardingResponse, error) {
	f.seen(req.GetUserId())
	f.mu.Lock()
	f.onboardingUpdates = append(f.onboardingUpdates, req)
	f.mu.Unlock()
	return &plansv1.UpdateMyOnboardingResponse{Onboarding: &plansv1.Onboarding{Wizard: req.GetWizard(), SeenTours: req.GetSeenTours()}}, nil
}

// The onboarding record belongs to the signed-in person: the person is the session's, never one a body names, and an API key (which has no person) is refused.
func TestOnboardingRoutesActForTheSessionsPerson(t *testing.T) {
	p := newProduct(t, nil, nil)
	resp, m := p.call(t, "GET", "/app/v1/me/onboarding", tokAda, "", "")
	if resp.StatusCode != 200 || len(p.ident.onboardingGets) != 1 || p.ident.onboardingGets[0].GetUserId() != adaUser {
		t.Fatalf("get: %d %v %v", resp.StatusCode, m, p.ident.onboardingGets)
	}
	if tours, _ := m["seen_tours"].([]any); len(tours) != 1 || tours[0] != "customers" {
		t.Errorf("get answered %v", m)
	}

	body := `{"user_id":"` + bobUser + `","wizard":{"step":"meter","event":true},"seen_tours":["meters"],"forget_tours":["plans"]}`
	resp, m = p.call(t, "PATCH", "/app/v1/me/onboarding", tokAda, "", body)
	if resp.StatusCode != 200 || len(p.ident.onboardingUpdates) != 1 {
		t.Fatalf("patch: %d %v", resp.StatusCode, m)
	}
	got := p.ident.onboardingUpdates[0]
	if got.GetUserId() != adaUser {
		t.Errorf("the update was for %q, want the session's %q", got.GetUserId(), adaUser)
	}
	if got.GetWizard().GetStep() != "meter" || !got.GetWizard().GetEvent() || got.GetSeenTours()[0] != "meters" || got.GetForgetTours()[0] != "plans" {
		t.Errorf("the update lost its fields: %v", got)
	}
	if w, _ := m["wizard"].(map[string]any); w["step"] != "meter" || w["event"] != true {
		t.Errorf("patch answered %v", m)
	}

	if resp, _ = p.call(t, "GET", "/app/v1/me/onboarding", "", "", ""); resp.StatusCode != 401 {
		t.Errorf("without a session: %d", resp.StatusCode)
	}
	if resp, _ = p.call(t, "PATCH", "/app/v1/me/onboarding", "key-ada", "", `{"seen_tours":["x"]}`); resp.StatusCode != 401 {
		t.Errorf("with an API key: %d", resp.StatusCode)
	}
	if resp, _ = p.call(t, "PATCH", "/app/v1/me/onboarding", tokAda, "", `{"nope":`); resp.StatusCode != 400 {
		t.Errorf("malformed body: %d", resp.StatusCode)
	}
}
