package onboarding

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func TestMergeWizardOverwritesWhatIsGivenAndKeepsTheRest(t *testing.T) {
	cur := &plansv1.OnboardingWizard{Status: proto.String("active"), Step: proto.String("meter"), App: proto.String("app-1"), Currency: proto.String("TRY")}
	got, err := MergeWizard(cur, &plansv1.OnboardingWizard{Step: proto.String("package"), Meter: proto.String("api_calls")})
	if err != nil {
		t.Fatal(err)
	}
	want := &plansv1.OnboardingWizard{Status: proto.String("active"), Step: proto.String("package"), App: proto.String("app-1"), Currency: proto.String("TRY"), Meter: proto.String("api_calls")}
	if !proto.Equal(got, want) {
		t.Errorf("merged = %v, want %v", got, want)
	}
	if cur.GetStep() != "meter" {
		t.Error("the stored document was changed in place")
	}
}

func TestMergeWizardFromNothingFillsDefaults(t *testing.T) {
	got, err := MergeWizard(nil, &plansv1.OnboardingWizard{App: proto.String("a")})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetStatus() != "active" || got.GetStep() != "welcome" || got.GetApp() != "a" {
		t.Errorf("merged = %v", got)
	}
}

func TestMergeWizardKeepsAFlagOnceSet(t *testing.T) {
	cur := &plansv1.OnboardingWizard{Status: proto.String("done"), Step: proto.String("done"), Event: proto.Bool(true), Hidden: proto.Bool(true), Customer: proto.Bool(true)}
	got, err := MergeWizard(cur, &plansv1.OnboardingWizard{Event: proto.Bool(false), Hidden: proto.Bool(false), Customer: proto.Bool(false)})
	if err != nil {
		t.Fatal(err)
	}
	if !got.GetEvent() || !got.GetHidden() || !got.GetCustomer() {
		t.Errorf("a stale browser undid a flag: %v", got)
	}
}

func TestMergeWizardValidates(t *testing.T) {
	for name, patch := range map[string]*plansv1.OnboardingWizard{
		"status":   {Status: proto.String("finished")},
		"step":     {Step: proto.String("nowhere")},
		"currency": {Currency: proto.String("try")},
		"timezone": {Timezone: proto.String("Europe/Istanbul; drop")},
		"app":      {App: proto.String(strings.Repeat("a", 129))},
		"meter":    {Meter: proto.String("line\nbreak")},
	} {
		if _, err := MergeWizard(nil, patch); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: err = %v, want InvalidArgument", name, err)
		}
	}
	if _, err := MergeWizard(nil, &plansv1.OnboardingWizard{Currency: proto.String("USD"), Timezone: proto.String("America/Argentina/Buenos_Aires"), Status: proto.String("skipped")}); err != nil {
		t.Errorf("valid patch refused: %v", err)
	}
}

func TestMergeToursIsAUnion(t *testing.T) {
	got, err := MergeTours([]string{"customers", "meters"}, []string{"meters", "plans"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"customers", "meters", "plans"}; !slices.Equal(got, want) {
		t.Errorf("merged = %v, want %v", got, want)
	}
}

func TestMergeToursForgetsOnlyWhenAsked(t *testing.T) {
	got, err := MergeTours([]string{"a", "b", "c"}, nil, []string{"b", "zz"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "c"}; !slices.Equal(got, want) {
		t.Errorf("merged = %v, want %v", got, want)
	}
	// Asked for in the same request, the removal comes last.
	got, _ = MergeTours(nil, []string{"x"}, []string{"x"})
	if len(got) != 0 {
		t.Errorf("merged = %v, want none", got)
	}
}

func TestMergeToursValidatesAndBounds(t *testing.T) {
	for _, bad := range []string{"", "has space", "x/y", strings.Repeat("a", 65), "é"} {
		if _, err := MergeTours(nil, []string{bad}, nil); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%q: err = %v, want InvalidArgument", bad, err)
		}
	}
	many := make([]string, MaxTours+1)
	for i := range many {
		many[i] = fmt.Sprintf("tour%d", i)
	}
	if _, err := MergeTours(nil, many, nil); status.Code(err) != codes.InvalidArgument {
		t.Errorf("too many tours: err = %v, want InvalidArgument", err)
	}
}
