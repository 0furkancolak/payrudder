package support

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

func TestAPersonReadsOnlyTheHistoryKindsOnTheAllowList(t *testing.T) {
	var rows []sqlc.ListSupportEventsRow
	for _, kind := range []string{"status", "tags", "priority", "assignee", "a-kind-added-next-year"} {
		rows = append(rows, sqlc.ListSupportEventsRow{ID: uuid.New(), Kind: kind, ActorKind: "operator", ActorName: "Sam", FromValue: "a", ToValue: "secret-" + kind, CreatedAt: time.Now()})
	}
	kinds := func(evs []*plansv1.SupportEvent) []string {
		var out []string
		for _, e := range evs {
			out = append(out, e.GetKind())
		}
		return out
	}
	if got := kinds(eventsOf(rows, true)); !reflect.DeepEqual(got, []string{"status", "priority"}) {
		t.Errorf("a person reads %v", got)
	}
	if got := kinds(eventsOf(rows, false)); len(got) != len(rows) {
		t.Errorf("the operator reads %v", got)
	}
	// The list itself is a decision: a kind joins it on purpose, with a look at what its values hold.
	var allowed []string
	for k := range personEventKinds {
		allowed = append(allowed, k)
	}
	sort.Strings(allowed)
	if !reflect.DeepEqual(allowed, []string{"priority", "status"}) {
		t.Errorf("the allow-list is %v", allowed)
	}
}

func TestForPersonTakesTheOperatorsOwnFields(t *testing.T) {
	got := forPerson(&plansv1.SupportTicket{Id: "t", Subject: "s", Tags: []string{"refund"}, Assignee: "ops@example.com", Priority: "high"})
	if len(got.GetTags()) != 0 || got.GetAssignee() != "" || got.GetId() != "t" || got.GetSubject() != "s" || got.GetPriority() != "high" {
		t.Errorf("for a person: %+v", got)
	}
}
