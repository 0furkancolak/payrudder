package httpx

import (
	"log/slog"
	"net/http"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// ProviderTarget reads the tenant, the environment and the {provider} of a request and finds the tenant's account there. It answers and reports false
// when it cannot.
func ProviderTarget(w http.ResponseWriter, r *http.Request, res target.Resolver) (target.Target, bool) {
	t, ok := Tenant(w, r)
	if !ok {
		return target.Target{}, false
	}
	env, ok := Environment(w, r)
	if !ok {
		return target.Target{}, false
	}
	tg, err := target.Resolve(r.Context(), res, t, env, r.PathValue("provider"))
	if err != nil {
		Reply(slog.Default(), w, r, err) // always a refusal, which is not logged
		return target.Target{}, false
	}
	return tg, true
}
