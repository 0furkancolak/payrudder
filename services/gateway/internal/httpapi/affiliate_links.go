package httpapi

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// The public link of a partner and the referral cookie (phase 47).
//
//	GET /r/<code>             counts a visit, sets the cookie, sends the visitor to the program's landing page (or says thank you)
//	GET /i/<token>?ref=<code> the same, on a payment page: the seller's own checkout can carry the code
//
// The cookie is first-party (set and read by this host only) and holds two things: the partner's code and the time of the visit. No visitor id, no address, no
// profile, nothing for another site. The hosted checkout (a payment link, an invoice, the customer portal) reads it when a payment starts and passes it to billing-svc,
// which tells the affiliate program; the program decides (first touch wins, the window, self-referral). A visitor who has the same code already is not counted
// again, so reloading the link does not inflate the partner's clicks.

const (
	refCookie = "payrudder_ref"
	// maxRefAge bounds how old a cookie is believed, whatever the program's window says: the longest window is a year.
	maxRefAge = 366 * 24 * time.Hour
)

var refCodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

func (s *Server) affiliateLinkRoutes() []Route {
	return []Route{
		{"GET", "/r/{code}", AccessPublic, ratelimit.Class(""), s.affiliateLink},
		{"GET", "/privacy", AccessPublic, ratelimit.Class(""), s.privacyPage},
	}
}

// refValue is the content of the cookie: the code and the Unix time of the visit.
func refValue(code string, at time.Time) string { return code + "." + strconv.FormatInt(at.Unix(), 10) }

// parseRef reads the content of the cookie. A code that is not one, a time that is not a number, in the future or older than a year is not a referral.
func parseRef(v string, now time.Time) (code string, at time.Time, ok bool) {
	i := strings.LastIndexByte(v, '.')
	if i < 0 {
		return "", time.Time{}, false
	}
	code = v[:i]
	secs, err := strconv.ParseInt(v[i+1:], 10, 64)
	if err != nil || !refCodeRe.MatchString(code) {
		return "", time.Time{}, false
	}
	at = time.Unix(secs, 0)
	if at.After(now.Add(time.Minute)) || now.Sub(at) > maxRefAge {
		return "", time.Time{}, false
	}
	return code, at, true
}

// referralOf is the referral cookie of a request, if it carries a good one.
func referralOf(r *http.Request) (code string, at time.Time, ok bool) {
	c, err := r.Cookie(refCookie)
	if err != nil {
		return "", time.Time{}, false
	}
	return parseRef(c.Value, time.Now())
}

// referral is the cookie as the requests to billing-svc carry it.
func referral(r *http.Request) (code string, at *timestamppb.Timestamp) {
	c, t, ok := referralOf(r)
	if !ok {
		return "", nil
	}
	return c, timestamppb.New(t)
}

func (s *Server) secureRefCookie(r *http.Request) bool {
	return r.TLS != nil || strings.HasPrefix(s.d.PublicURL, "https://")
}

func (s *Server) setRefCookie(w http.ResponseWriter, r *http.Request, code string, windowDays int32, now time.Time) {
	//nolint:gosec // Secure follows the public address: development runs on plain http, every deployment on https
	http.SetCookie(w, &http.Cookie{Name: refCookie, Value: refValue(code, now), Path: "/", MaxAge: int(windowDays) * 24 * 3600, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: s.secureRefCookie(r)})
}

// followRef counts a visit on a partner's link and remembers it in the visitor's cookie, and says what the program answered. The first partner a visitor follows keeps
// the cookie while their window lasts (first touch); a visit on the same link again changes nothing and is not counted twice.
func (s *Server) followRef(w http.ResponseWriter, r *http.Request, code string) (*plansv1.TrackAffiliateClickResponse, bool) {
	if !refCodeRe.MatchString(code) {
		return nil, false
	}
	now := time.Now()
	have, at, haveOK := referralOf(r)
	same := haveOK && strings.EqualFold(have, code)
	resp, err := s.d.AffiliatePub.TrackAffiliateClick(r.Context(), &plansv1.TrackAffiliateClickRequest{Code: code, SkipCount: same})
	if err != nil || !resp.GetValid() {
		return resp, false
	}
	switch {
	case same:
		// Already remembered: keep the first visit's time, which the window runs from.
	case haveOK && now.Sub(at) <= time.Duration(resp.GetAttributionWindowDays())*24*time.Hour:
		// Another partner's visit is still inside the window: first touch wins, and this one is only counted.
	default:
		s.setRefCookie(w, r, resp.GetCode(), resp.GetAttributionWindowDays(), now)
	}
	return resp, true
}

// captureRef reads ?ref=<code> on a payment page the seller linked to. A code that is not valid does nothing; the page is shown either way.
func (s *Server) captureRef(w http.ResponseWriter, r *http.Request) {
	if code := r.URL.Query().Get("ref"); code != "" && s.d.AffiliatePub != nil {
		s.followRef(w, r, code)
	}
}

// affiliateLink is GET /r/<code>.
func (s *Server) affiliateLink(w http.ResponseWriter, r *http.Request) {
	lang := pickLang(r)
	if !s.hostedAllowed(w, r) {
		return
	}
	if s.d.AffiliatePub == nil {
		hostedNotFound(w, lang)
		return
	}
	resp, ok := s.followRef(w, r, r.PathValue("code"))
	if !ok {
		hostedNotFound(w, lang)
		return
	}
	if to := landing(resp.GetLandingUrl(), resp.GetCode()); to != "" {
		hostedHeaders(w)
		http.Redirect(w, r, to, http.StatusFound)
		return
	}
	s.render(w, r, http.StatusOK, "referral", &referralView{pageBase: newBase(lang), Privacy: "/privacy" + langSuffix(r)})
}

// landing is the address a partner's visitor is sent to: the program's landing page with the code added as ?ref= (unless it has one), so that the seller's own sign-up
// can read it. Empty when the program has no landing page.
func landing(base, code string) string {
	if base == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	q := u.Query()
	if q.Get("ref") == "" {
		q.Set("ref", code)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

type referralView struct {
	pageBase
	Privacy string
}

func (v *referralView) Title() string { return v.L["ref_title"] }

type privacyView struct{ pageBase }

func (v *privacyView) Title() string { return v.L["priv_title"] }

// privacyPage is GET /privacy: what the referral cookie holds, what is counted, and what the visitor can do.
func (s *Server) privacyPage(w http.ResponseWriter, r *http.Request) {
	if !s.hostedAllowed(w, r) {
		return
	}
	s.render(w, r, http.StatusOK, "privacy", &privacyView{pageBase: newBase(pickLang(r))})
}

// withLink gives an affiliate the public address of their link, which only the gateway knows.
func (s *Server) withLink(a *plansv1.Affiliate) *plansv1.Affiliate {
	if a != nil && a.GetCode() != "" {
		a.Link = strings.TrimRight(s.d.PublicURL, "/") + "/r/" + a.GetCode()
	}
	return a
}
