package httpapi

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// Affiliate payouts and the journal (phase 47). Payrudder moves no money: the owner pays by hand or bank transfer and marks the payout paid, which posts it.

func (s *Server) affiliatePayoutRoutes() []Route {
	return []Route{
		{"GET", "/v1/affiliates/payouts", AccessTenant, ratelimit.ClassRead, s.listAffiliatePayouts},
		{"POST", "/v1/affiliates/payouts", AccessTenant, ratelimit.ClassWrite, s.createAffiliatePayouts},
		{"POST", "/v1/affiliates/payouts/{id}/pay", AccessTenant, ratelimit.ClassWrite, s.markAffiliatePayoutPaid},
		{"POST", "/v1/affiliates/payouts/{id}/cancel", AccessTenant, ratelimit.ClassWrite, s.cancelAffiliatePayout},
		{"GET", "/v1/affiliates/journal", AccessTenant, ratelimit.ClassRead, s.listAffiliateJournal},
		{"GET", "/v1/affiliates/journal.csv", AccessTenant, ratelimit.ClassRead, s.affiliateJournalCSV},
	}
}

func (s *Server) listAffiliatePayouts(w http.ResponseWriter, r *http.Request) {
	size, token, ok := pageParams(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	resp, err := s.d.Affiliates.ListAffiliatePayouts(r.Context(), &plansv1.ListAffiliatePayoutsRequest{AffiliateId: q.Get("affiliate_id"), Status: q.Get("status"), PageSize: size, PageToken: token})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) createAffiliatePayouts(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreateAffiliatePayoutsRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Affiliates.CreateAffiliatePayouts(r.Context(), req)
	respond(w, r, http.StatusCreated, resp, err)
}

func (s *Server) markAffiliatePayoutPaid(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.MarkAffiliatePayoutPaidRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Id = r.PathValue("id")
	resp, err := s.d.Affiliates.MarkAffiliatePayoutPaid(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetPayout(), err)
}

func (s *Server) cancelAffiliatePayout(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Affiliates.CancelAffiliatePayout(r.Context(), &plansv1.CancelAffiliatePayoutRequest{Id: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetPayout(), err)
}

// journalRequest reads the filter of the journal: payout, currency and a period of RFC 3339 times.
func journalRequest(w http.ResponseWriter, r *http.Request) (*plansv1.ListAffiliateJournalRequest, bool) {
	q := r.URL.Query()
	req := &plansv1.ListAffiliateJournalRequest{PayoutId: q.Get("payout_id"), Currency: q.Get("currency")}
	for name, dst := range map[string]**timestamppb.Timestamp{"from": &req.FromTime, "to": &req.ToTime} {
		if v := q.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeError(w, r, invalid(name, "must be an RFC 3339 time"))
				return nil, false
			}
			*dst = timestamppb.New(t)
		}
	}
	return req, true
}

func (s *Server) listAffiliateJournal(w http.ResponseWriter, r *http.Request) {
	size, token, ok := pageParams(w, r)
	if !ok {
		return
	}
	req, ok := journalRequest(w, r)
	if !ok {
		return
	}
	req.PageSize, req.PageToken = size, token
	resp, err := s.d.Affiliates.ListAffiliateJournal(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

// maxJournalRows bounds the CSV: it is read in pages of 200.
const maxJournalRows = 20000

// affiliateJournalCSV is GET /v1/affiliates/journal.csv: every posting of the filter, then a total line per currency whose debits and credits are equal when the
// books balance. A posting of a sandbox payout says fake.
func (s *Server) affiliateJournalCSV(w http.ResponseWriter, r *http.Request) {
	req, ok := journalRequest(w, r)
	if !ok {
		return
	}
	req.PageSize = 200
	var entries []*plansv1.AffiliateJournalEntry
	var totals []*plansv1.AffiliateJournalTotal
	for len(entries) < maxJournalRows {
		resp, err := s.d.Affiliates.ListAffiliateJournal(r.Context(), req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		entries, totals = append(entries, resp.GetEntries()...), resp.GetTotals()
		if resp.GetNextPageToken() == "" {
			break
		}
		req.PageToken = resp.GetNextPageToken()
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="affiliate-journal.csv"`)
	cw := csv.NewWriter(w)
	n := func(v int64) string { return strconv.FormatInt(v, 10) }
	_ = cw.Write([]string{"posted_at", "payout_id", "affiliate_id", "currency", "account", "debit_minor", "credit_minor", "fake", "memo"})
	for _, e := range entries {
		_ = cw.Write([]string{e.GetPostTime().AsTime().UTC().Format(time.RFC3339), e.GetPayoutId(), e.GetAffiliateId(), e.GetCurrency(), e.GetAccount(), n(e.GetDebitMinor()),
			n(e.GetCreditMinor()), strconv.FormatBool(e.GetFake()), csvSafe(e.GetMemo())})
	}
	for _, t := range totals {
		_ = cw.Write([]string{"total", "", "", t.GetCurrency(), "", n(t.GetDebitMinor()), n(t.GetCreditMinor()), "", ""})
	}
	s.finishCSV(r, cw)
}
