package command

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

func refusal(t *testing.T, err error, status int, key string) {
	t.Helper()
	var r *refuse.Error
	if !errors.As(err, &r) || r.Status != status || r.Key != key {
		t.Errorf("err = %v; want %d %s", err, status, key)
	}
}

// What is wrong with a request is refused before the store is asked: these run with no database.
func TestRoutingAndLabelsAreCheckedBeforeAnythingIsStored(t *testing.T) {
	ctx, tenant := context.Background(), uuid.New()
	for _, rules := range [][]store.EInvoiceRoute{
		{{Country: "Germany", Account: "nilvera"}},                                    // not a code
		{{Country: "DE", Account: "nilvera"}, {Country: "de", Account: "nilvera~de"}}, // a country twice
		{{Country: "DE", Account: "nilvera~"}},                                        // not an account key
		{{Country: "DE", Account: "nilvera~Big Label"}},                               // a label of the wrong shape
		make([]store.EInvoiceRoute, maxRules+1),
	} {
		refusal(t, SetRouting(ctx, Deps{}, tenant, "live", "operator", rules), http.StatusBadRequest, "einvoice.routing.invalid")
	}
	refusal(t, DeleteAccount(ctx, Deps{}, tenant, "live", "a~b~c", "operator"), http.StatusBadRequest, "common.invalid_argument")
	refusal(t, SetDefaultAccount(ctx, Deps{}, tenant, "live", "a~b~c", "operator"), http.StatusBadRequest, "common.invalid_argument")
	_, err := PutAccount(ctx, Deps{}, tenant, "live", "operator", Put{Provider: "nilvera", Label: "Big Label"})
	refusal(t, err, http.StatusBadRequest, "einvoice.account.invalid")
	_, err = PutAccount(ctx, Deps{}, tenant, "sandbox", "operator", Put{Provider: "mock", Label: "second"})
	refusal(t, err, http.StatusBadRequest, "einvoice.account.sandbox_mock_only")
}
