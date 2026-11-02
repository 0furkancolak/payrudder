package mock_test

import (
	"testing"

	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/conformance"
	"github.com/0furkancolak/payrudder/pkg/einvoice/mock"
)

// The mock is the reference adapter of the contract: it has no wire, so the checks about failures and cancelled calls do not apply to it.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Harness{New: func(testing.TB) einvoice.Provider { return mock.New() }})
}
