package httpapi

import (
	"cmp"
	"net/http"
	"slices"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// GET /v1/payments is the tenant's payment ledger as a person reads it: charges and refunds, newest first, each payment folded to its most settled
// outcome (a charge that was pending and then succeeded is one paid payment). It is the tenant-API counterpart of the operator's
// /admin/v1/tenants/{id}/ledger, which stays operator-only because it also carries provider references, decline details and the other ledger kinds.
// The tenant is the caller's own (an API key, or a session acting as a tenant of its accounts); there is no way to name another.

type paymentView struct {
	ID          string    `json:"id"`
	InvoiceID   string    `json:"invoice_id"`
	Provider    string    `json:"provider"`
	Kind        string    `json:"kind"`
	Status      string    `json:"status"`
	AmountMinor string    `json:"amount_minor"`
	Currency    string    `json:"currency"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type paymentsResponse struct {
	Payments []paymentView `json:"payments"`
	// Truncated says the ledger window (the newest 1000 rows) was full: older payments may exist.
	Truncated bool `json:"truncated"`
}

func (s *Server) listPayments(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if n, err := rangedParam(r.URL.Query(), "limit", 1, 200, "must be an integer from 1 to 200"); err != nil {
		writeError(w, r, err)
		return
	} else if n != 0 {
		limit = int(n)
	}
	ti, ok := tenant.FromContext(r.Context())
	if !ok {
		writeError(w, r, errkey.New(codes.Unauthenticated, "identity.tenant.metadata_missing", "no tenant"))
		return
	}
	rows, err := s.recentLedger(r.Context(), ti.ID.String())
	if err != nil {
		s.d.Log.WarnContext(r.Context(), "ledger read failed", "error", err)
		writeError(w, r, errkey.New(codes.Unavailable, "gateway.payments.ledger_unreachable", "the payment ledger is not reachable"))
		return
	}
	folded := foldLedger(rows)
	slices.SortStableFunc(folded, func(a, b paymentOf) int {
		if c := b.OccurredAt.Compare(a.OccurredAt); c != 0 {
			return c
		}
		bi, _ := b.ID.Int64()
		ai, _ := a.ID.Int64()
		return cmp.Compare(bi, ai)
	})
	out := paymentsResponse{Payments: []paymentView{}, Truncated: len(rows) >= ledgerWindowRows}
	for i, p := range folded {
		if i == limit {
			break
		}
		out.Payments = append(out.Payments, paymentView{ID: p.ID.String(), InvoiceID: p.InvoiceID, Provider: p.Provider, Kind: p.Kind, Status: p.shown,
			AmountMinor: itoa(p.amount()), Currency: p.Currency, OccurredAt: p.OccurredAt.UTC()})
	}
	writeJSONValue(w, http.StatusOK, out)
}
