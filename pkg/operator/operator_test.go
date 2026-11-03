package operator

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	r := Parse([]string{" Boss@Example.com, both@example.com ", ""}, []string{"help@example.com", "BOTH@example.com"})
	if want := []string{"boss@example.com"}; !reflect.DeepEqual(r.Admins(), want) {
		t.Errorf("admins %v, want %v", r.Admins(), want)
	}
	if want := []string{"help@example.com", "both@example.com"}; !reflect.DeepEqual(r.Support(), want) {
		t.Errorf("support %v, want %v", r.Support(), want)
	}
	for email, want := range map[string]Role{"BOSS@example.com ": Admin, "help@example.com": Support, "both@example.com": Support, "x@example.com": None, "": None} {
		if got := r.Of(email, true); got != want {
			t.Errorf("Of(%q) = %q, want %q", email, got, want)
		}
	}
	if r.Of("boss@example.com", false) != None {
		t.Error("an unverified address is no operator")
	}
}

func TestEmptyAndCanWrite(t *testing.T) {
	r := Parse(nil, []string{""})
	if len(r.Admins())+len(r.Support()) != 0 || r.Of("a@b.c", true) != None {
		t.Error("empty lists make no operator")
	}
	if !Admin.CanWrite() || Support.CanWrite() || None.CanWrite() {
		t.Error("only admin writes")
	}
}
