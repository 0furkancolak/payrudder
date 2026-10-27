//go:build integration

package itest

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// testOnboarding: what the console remembers for a person (wizard progress, seen tours) is kept on the server per person, merged and not replaced, so a second
// browser sees it and two browsers writing at once lose nothing.
func testOnboarding(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ada := e.signUp(t, "onboarding-ada@example.com", "Onboarding Ada").GetUser().GetId()
	bob := e.signUp(t, "onboarding-bob@example.com", "Onboarding Bob").GetUser().GetId()

	got, err := e.ident.GetMyOnboarding(ctx, &plansv1.GetMyOnboardingRequest{UserId: ada})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetOnboarding().GetWizard() != nil || len(got.GetOnboarding().GetSeenTours()) != 0 {
		t.Fatalf("a new person has %v, want nothing", got.GetOnboarding())
	}

	// The wizard is a document: a write changes the fields it names.
	if _, err := e.ident.UpdateMyOnboarding(ctx, &plansv1.UpdateMyOnboardingRequest{UserId: ada, Wizard: &plansv1.OnboardingWizard{Step: proto.String("meter"), App: proto.String("app-1"), Customer: proto.Bool(true)}}); err != nil {
		t.Fatal(err)
	}
	up, err := e.ident.UpdateMyOnboarding(ctx, &plansv1.UpdateMyOnboardingRequest{UserId: ada, Wizard: &plansv1.OnboardingWizard{Status: proto.String("done"), Step: proto.String("done"), Customer: proto.Bool(false)}})
	if err != nil {
		t.Fatal(err)
	}
	want := &plansv1.OnboardingWizard{Status: proto.String("done"), Step: proto.String("done"), App: proto.String("app-1"), Customer: proto.Bool(true)}
	if !proto.Equal(up.GetOnboarding().GetWizard(), want) {
		t.Errorf("wizard = %v, want %v", up.GetOnboarding().GetWizard(), want)
	}
	got, _ = e.ident.GetMyOnboarding(ctx, &plansv1.GetMyOnboardingRequest{UserId: ada})
	if !proto.Equal(got.GetOnboarding().GetWizard(), want) {
		t.Errorf("read back = %v, want %v", got.GetOnboarding().GetWizard(), want)
	}

	// Many browsers marking different tours at once: every one of them is kept.
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.ident.UpdateMyOnboarding(ctx, &plansv1.UpdateMyOnboardingRequest{UserId: ada, SeenTours: []string{fmt.Sprintf("tour%02d", i), "shared"}})
			if err != nil {
				t.Errorf("write %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	got, _ = e.ident.GetMyOnboarding(ctx, &plansv1.GetMyOnboardingRequest{UserId: ada})
	if n := len(got.GetOnboarding().GetSeenTours()); n != 13 {
		t.Errorf("%d tours kept (%v), want 13", n, got.GetOnboarding().GetSeenTours())
	}
	if !proto.Equal(got.GetOnboarding().GetWizard(), want) {
		t.Errorf("tour writes changed the wizard: %v", got.GetOnboarding().GetWizard())
	}

	// Forgetting is asked for by name and touches nothing else.
	up, err = e.ident.UpdateMyOnboarding(ctx, &plansv1.UpdateMyOnboardingRequest{UserId: ada, ForgetTours: []string{"shared", "tour00"}})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(up.GetOnboarding().GetSeenTours()); n != 11 || slices.Contains(up.GetOnboarding().GetSeenTours(), "shared") {
		t.Errorf("after forgetting: %v", up.GetOnboarding().GetSeenTours())
	}

	// It belongs to the person: somebody else has nothing of it.
	got, _ = e.ident.GetMyOnboarding(ctx, &plansv1.GetMyOnboardingRequest{UserId: bob})
	if got.GetOnboarding().GetWizard() != nil || len(got.GetOnboarding().GetSeenTours()) != 0 {
		t.Errorf("bob sees %v", got.GetOnboarding())
	}

	// Bad input is a field violation and changes nothing.
	for name, req := range map[string]*plansv1.UpdateMyOnboardingRequest{
		"status":   {UserId: ada, Wizard: &plansv1.OnboardingWizard{Status: proto.String("finished")}},
		"tour id":  {UserId: ada, SeenTours: []string{"not a tour"}},
		"bad user": {UserId: "nobody", SeenTours: []string{"x"}},
	} {
		if _, err := e.ident.UpdateMyOnboarding(ctx, req); code(t, err) != codes.InvalidArgument {
			t.Errorf("%s: %v, want InvalidArgument", name, err)
		}
	}
	got, _ = e.ident.GetMyOnboarding(ctx, &plansv1.GetMyOnboardingRequest{UserId: ada})
	if got.GetOnboarding().GetWizard().GetStatus() != "done" || len(got.GetOnboarding().GetSeenTours()) != 11 {
		t.Errorf("a refused write changed the record: %v", got.GetOnboarding())
	}
}
