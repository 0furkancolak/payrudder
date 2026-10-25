package einvoice_test

import (
	"errors"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/mock"
	"github.com/0furkancolak/payrudder/pkg/einvoice/nilvera"
	"github.com/0furkancolak/payrudder/pkg/einvoice/webhook"
)

func TestRegistry(t *testing.T) {
	r := einvoice.NewRegistry()
	mock.Register(r)
	nilvera.Register(r)
	webhook.Register(r)
	if got := r.Names(); len(got) != 3 || got[0] != "mock" || got[1] != "nilvera" || got[2] != "webhook" {
		t.Errorf("names %v", got)
	}
	if !r.Has("mock") || r.Has("x") {
		t.Error("Has")
	}
	if _, err := r.Build("x", nil); !errors.Is(err, einvoice.ErrUnknownProvider) {
		t.Errorf("%v", err)
	}
	defer func() {
		if recover() == nil {
			t.Error("double registration must panic")
		}
	}()
	mock.Register(r)
}

func TestCapabilities(t *testing.T) {
	c := einvoice.Capabilities{Types: []einvoice.DocumentType{einvoice.TypeEArsiv}}
	if !c.Supports(einvoice.TypeEArsiv) || c.Supports(einvoice.TypePeppol) {
		t.Error("Supports")
	}
	if !einvoice.TypePeppol.Valid() || einvoice.DocumentType("x").Valid() {
		t.Error("Valid")
	}
}
