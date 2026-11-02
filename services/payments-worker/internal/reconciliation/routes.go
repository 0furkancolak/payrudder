// Package reconciliation serves the operator's comparison of what billing calls paid with what the payment ledger holds, the queue of discrepancies it keeps, and the
// settlement lines that tell paid from settled. It maps the request to the query and the commands (ADR 0020); the rules are there.
package reconciliation

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/reconciliation/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/reconciliation/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// ActorHeader names who an operator action is recorded for (the gateway passes the caller's X-Payrudder-Actor).
const ActorHeader = "X-Payrudder-Actor"

// Handler serves the reconciliation endpoints.
type Handler struct {
	Query   query.Deps
	Command command.Deps
	Log     *slog.Logger
}

// Register adds the routes to mux, wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	const base = "/admin/tenants/{tenant}/"
	route := func(pattern string, f http.HandlerFunc) {
		method, path, _ := strings.Cut(pattern, " ")
		mux.HandleFunc(method+" "+base+path, guard(f))
	}
	route("GET reconciliation", h.get)
	route("POST reconciliation/runs", h.run)
	route("GET reconciliation/discrepancies", h.list)
	route("POST reconciliation/discrepancies/resolve", h.resolveMany)
	route("GET reconciliation/discrepancies/{id}", h.one)
	route("POST reconciliation/discrepancies/{id}/assign", h.assign)
	route("POST reconciliation/discrepancies/{id}/explain", h.explain)
	route("POST reconciliation/discrepancies/{id}/resolve", h.resolve)
	route("GET settlement/payments", h.payments)
	route("POST settlement/import", h.importCSV)
	route("POST settlement/sync", h.sync)
	route("GET settlement/fee-rules", h.feeRules)
	route("PUT settlement/fee-rules", h.putFeeRule)
	route("DELETE settlement/fee-rules", h.deleteFeeRule)
}

// get is GET /admin/tenants/{tenant}/reconciliation?period=YYYY-MM: the comparison itself, nothing kept.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	rec, err := query.Get(r.Context(), h.Query, t, r.URL.Query().Get("period"))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rec)
}

func actor(r *http.Request) string {
	a := strings.TrimSpace(r.Header.Get(ActorHeader))
	if runes := []rune(a); len(runes) > 120 {
		a = string(runes[:120])
	}
	if a == "" {
		return "operator"
	}
	return a
}

// run is POST /admin/tenants/{tenant}/reconciliation/runs?environment=live {"period":"2026-10"}: compare and keep.
func (h *Handler) run(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	environment, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	var in struct {
		Period string `json:"period"`
	}
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	res, err := command.Run(r.Context(), h.Command, command.RunInput{Tenant: t, Environment: environment, Period: in.Period, StartedBy: command.ByOperator, Actor: actor(r)})
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

const (
	defaultPage = 50
	maxPage     = 200
)

func badFilter(msg string) error {
	return refuse.New(http.StatusBadRequest, "payments.reconcile.bad_filter", msg)
}

// words reads a query parameter that may repeat or hold a comma-separated list, and checks each word is one of the allowed.
func words(r *http.Request, name string, allowed []string) ([]string, error) {
	var out []string
	for _, v := range r.URL.Query()[name] {
		for _, w := range strings.Split(v, ",") {
			if w = strings.TrimSpace(w); w == "" {
				continue
			}
			if !slices.Contains(allowed, w) {
				return nil, badFilter(name + " " + strconv.Quote(w) + " is not one of " + strings.Join(allowed, ", "))
			}
			out = append(out, w)
		}
	}
	return out, nil
}

// list is GET /admin/tenants/{tenant}/reconciliation/discrepancies?period=&status=&kind=&owner=&invoice_id=&limit=&page_token=: the queue, newest first.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := store.Filter{Period: q.Get("period"), Owner: q.Get("owner"), Invoice: q.Get("invoice_id"), Limit: defaultPage}
	var err error
	if f.Statuses, err = words(r, "status", []string{store.DiscOpen, store.DiscExplained, store.DiscResolved}); err == nil {
		f.Kinds, err = words(r, "kind", query.Kinds)
	}
	if err == nil && f.Period != "" && !validPeriod(f.Period) {
		err = badFilter("period must look like 2026-10")
	}
	if err == nil && q.Get("limit") != "" {
		if n, perr := strconv.Atoi(q.Get("limit")); perr != nil || n < 1 || n > maxPage {
			err = badFilter("limit is 1 to " + strconv.Itoa(maxPage))
		} else {
			f.Limit = n
		}
	}
	if err == nil && q.Get("page_token") != "" {
		if f.AfterID, err = strconv.ParseInt(q.Get("page_token"), 10, 64); err != nil || f.AfterID < 1 {
			err = badFilter("page_token is not one this API gave")
		}
	}
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	asked := f.Limit
	f.Limit++
	rows, err := h.Command.Discrepancies.List(r.Context(), t, f)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	next := ""
	if len(rows) > asked {
		rows = rows[:asked]
		next = strconv.FormatInt(rows[asked-1].ID, 10)
	}
	counts, err := h.Command.Discrepancies.Counts(r.Context(), t, f.Period)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	out := map[string]any{"discrepancies": rows, "counts": counts, "next_page_token": next}
	if runs, err := h.Command.Discrepancies.Runs(r.Context(), t, 1); err == nil && len(runs) == 1 {
		out["last_run"] = runs[0]
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func validPeriod(p string) bool {
	if len(p) != 7 || p[4] != '-' {
		return false
	}
	y, err1 := strconv.Atoi(p[:4])
	m, err2 := strconv.Atoi(p[5:])
	return err1 == nil && err2 == nil && y >= 2000 && m >= 1 && m <= 12
}

func discrepancyID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpx.Fail(w, http.StatusNotFound, errkey.Key("payments.reconcile.not_found"), "no such discrepancy")
		return 0, false
	}
	return id, true
}

// one is GET /admin/tenants/{tenant}/reconciliation/discrepancies/{id}: the discrepancy with its events.
func (h *Handler) one(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	id, ok := discrepancyID(w, r)
	if !ok {
		return
	}
	d, events, err := h.Command.Discrepancies.Get(r.Context(), t, id)
	if errors.Is(err, store.ErrNoDiscrepancy) {
		httpx.Fail(w, http.StatusNotFound, errkey.Key("payments.reconcile.not_found"), "no such discrepancy")
		return
	}
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"discrepancy": d, "events": events})
}

// act decodes the tenant, the discrepancy id and the body of an action on one discrepancy, runs it, and answers the discrepancy as it is then.
func (h *Handler) act(w http.ResponseWriter, r *http.Request, body any, do func(t uuid.UUID, id int64) (store.Discrepancy, error)) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	id, ok := discrepancyID(w, r)
	if !ok {
		return
	}
	if !httpx.DecodeBody(w, r, body) {
		return
	}
	d, err := do(t, id)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"discrepancy": d})
}

func (h *Handler) assign(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Owner string `json:"owner"`
	}
	h.act(w, r, &in, func(t uuid.UUID, id int64) (store.Discrepancy, error) {
		return command.Assign(r.Context(), h.Command, t, id, in.Owner, actor(r))
	})
}

func (h *Handler) explain(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Note  string `json:"note"`
		Owner string `json:"owner"`
	}
	h.act(w, r, &in, func(t uuid.UUID, id int64) (store.Discrepancy, error) {
		return command.Explain(r.Context(), h.Command, t, id, in.Note, in.Owner, actor(r))
	})
}

type resolveBody struct {
	Resolution string        `json:"resolution"`
	Note       string        `json:"note"`
	Owner      string        `json:"owner"`
	LedgerFact *command.Fact `json:"ledger_fact,omitempty"`
}

func (h *Handler) resolve(w http.ResponseWriter, r *http.Request) {
	var in resolveBody
	h.act(w, r, &in, func(t uuid.UUID, id int64) (store.Discrepancy, error) {
		return command.Resolve(r.Context(), h.Command, t, id, command.ResolveInput{Resolution: in.Resolution, Note: in.Note, Owner: in.Owner, Actor: actor(r), Fact: in.LedgerFact})
	})
}

// resolveMany is POST /admin/tenants/{tenant}/reconciliation/discrepancies/resolve {"ids":[1,2],"resolution":"not_an_error","note":"..."}.
func (h *Handler) resolveMany(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	var in struct {
		IDs []int64 `json:"ids"`
		resolveBody
	}
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	out, err := command.ResolveMany(r.Context(), h.Command, t, in.IDs, command.ResolveInput{Resolution: in.Resolution, Note: in.Note, Owner: in.Owner, Actor: actor(r), Fact: in.LedgerFact})
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"outcomes": out})
}

// payments is GET /admin/tenants/{tenant}/settlement/payments?period=2026-10: the period's successful payments with their settlement state.
func (h *Handler) payments(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	ps, err := query.Payments(r.Context(), h.Query, t, r.URL.Query().Get("period"))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"period": r.URL.Query().Get("period"), "payments": ps})
}

// importCSV is POST /admin/tenants/{tenant}/settlement/import?environment=live&provider=stripe with a CSV body (docs/reconciliation.md).
func (h *Handler) importCSV(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	environment, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, command.MaxCSV))
	if err != nil {
		httpx.Fail(w, http.StatusRequestEntityTooLarge, errkey.Key("payments.request.too_large"), "the report is larger than 5 MB: import it in parts")
		return
	}
	res, err := command.ImportCSV(r.Context(), h.Command, t, environment, r.URL.Query().Get("provider"), bytes.NewReader(raw))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// sync is POST /admin/tenants/{tenant}/settlement/sync?environment=live {"provider":"stripe","since":"2026-10-01T00:00:00Z"}: read the provider's own settlement.
func (h *Handler) sync(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	environment, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	var in struct {
		Provider string    `json:"provider"`
		Since    time.Time `json:"since"`
	}
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	res, err := command.Sync(r.Context(), h.Command, t, environment, in.Provider, in.Since)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) feeRules(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	rules, err := h.Command.Settlements.FeeRules(r.Context(), t)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

func (h *Handler) putFeeRule(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	var in store.FeeRule
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	if err := command.PutFeeRule(r.Context(), h.Command, t, in); err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rule": in})
}

func (h *Handler) deleteFeeRule(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	environment, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	found, err := h.Command.Settlements.DeleteFeeRule(r.Context(), t, environment, q.Get("provider"), strings.ToUpper(q.Get("currency")))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	if !found {
		httpx.Fail(w, http.StatusNotFound, errkey.Key("payments.settlement.fee_rule_not_found"), "no such fee rule")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
