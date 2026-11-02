package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/gateway/internal/mailadmin"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// MailAdmin is what the gateway asks of the notifier about a tenant's mail (internal/mailadmin is the client). Nil means the notifier's events listener is not
// configured here: the two list views then answer 200 with an empty list and "connected": false (a page that is merely not connected must not log an error in the
// browser on every visit), and lifting a suppression or a preview, which are actions, answer 503 gateway.mail.unavailable.
type MailAdmin interface {
	Suppressions(ctx context.Context, tenantID uuid.UUID, after string, size int) ([]mailadmin.Suppression, string, error)
	Unsuppress(ctx context.Context, tenantID uuid.UUID, address string) error
	Messages(ctx context.Context, tenantID uuid.UUID, after string, size int) ([]mailadmin.Message, string, error)
	PreviewPriceChange(ctx context.Context, in mailadmin.PriceChangePreview) (mailadmin.Rendered, error)
	Event(ctx context.Context, e mailx.Event) error
}

func (s *Server) mailRoutes() []Route {
	return []Route{
		{"GET", "/v1/mail-suppressions", AccessTenant, ratelimit.ClassRead, s.listMailSuppressions},
		{"DELETE", "/v1/mail-suppressions/{address}", AccessTenant, ratelimit.ClassWrite, s.deleteMailSuppression},
		{"GET", "/v1/mail-messages", AccessTenant, ratelimit.ClassRead, s.listMailMessages},
		{"POST", "/v1/mail-previews/price-change", AccessTenant, ratelimit.ClassRead, s.previewPriceChangeMail},
		{"POST", "/inbound/mail-events", AccessPublic, "", s.inboundMailEvents},
	}
}

// inboundMailEvents is the webhook of the mail provider's delivery events (bounces, complaints, deliveries): the translating layer between a provider and the notifier's
// neutral endpoint. The body is a Resend webhook, signed with Svix (pkg/mailx: ParseResendWebhook, VerifySvix; UNVERIFIED that Nafru's webhooks are the same); a request
// that does not verify is 401, an event that says nothing about delivery is 200 and ignored, and a notifier that cannot be reached is 503 so that the provider sends it
// again (the notifier's side is idempotent). 404 until PAYRUDDER_MAIL_WEBHOOK_SECRET is set (with the notifier's address). Limited per address like the other public
// webhooks and pages (hostedAllowed): 429 over the limit.
func (s *Server) inboundMailEvents(w http.ResponseWriter, r *http.Request) {
	if s.d.MailWebhookSecret == "" || s.d.Mail == nil {
		http.NotFound(w, r)
		return
	}
	// Before the body is read: it can be checked only after, and a public route that reads 64 KiB per request for anybody is a free way to spend the gateway's memory.
	if !s.hostedAllowed(w, r) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, mailWebhookLimit+1))
	if err != nil || len(body) > mailWebhookLimit {
		http.Error(w, "the body is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if mailx.VerifySvix(r.Header, body, s.d.MailWebhookSecret, time.Now(), mailx.SvixTolerance) != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	events, err := mailx.ParseResendWebhook(body)
	if err != nil {
		writeError(w, r, errkey.New(codes.InvalidArgument, "gateway.request.malformed_json", "the body must be the JSON of a delivery webhook"))
		return
	}
	for _, e := range events {
		if err := s.d.Mail.Event(r.Context(), e); err != nil {
			var bad mailadmin.BadRequestError
			if errors.As(err, &bad) {
				continue // the notifier refused this event for good; a retry would be refused again, and the others in the body may still count
			}
			writeError(w, r, errkey.New(codes.Unavailable, "gateway.mail.unavailable", "the notifier did not answer: try again in a moment"))
			return
		}
	}
	writeJSONValue(w, http.StatusOK, map[string]int{"events": len(events)})
}

// mailWebhookLimit is the largest webhook body taken: a delivery event is a few hundred bytes.
const mailWebhookLimit = 64 << 10

// mailTenant is the tenant of the request, or the answer when the notifier is not reachable from here. The tenant comes from the credential, never from the request.
func (s *Server) mailAdminTenant(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if s.d.Mail == nil {
		writeError(w, r, errkey.New(codes.Unavailable, "gateway.mail.unavailable", "the mail log and suppression list are not connected on this deployment"))
		return uuid.Nil, false
	}
	ti, err := tenant.Require(r.Context())
	if err != nil {
		writeError(w, r, err)
		return uuid.Nil, false
	}
	return ti.ID, true
}

// mailError turns what the notifier client reports into the gateway's answers.
func mailError(w http.ResponseWriter, r *http.Request, err error) {
	var bad mailadmin.BadRequestError
	switch {
	case errors.Is(err, mailadmin.ErrNotFound):
		writeError(w, r, errkey.New(codes.NotFound, "gateway.mail.suppression_not_found", "that address is not on the suppression list"))
	case errors.As(err, &bad):
		writeError(w, r, invalid("request", bad.Message))
	default:
		writeError(w, r, errkey.New(codes.Unavailable, "gateway.mail.unavailable", "the notifier did not answer: try again in a moment"))
	}
}

// mailPage reads page_size (1 to 500, default 50) and the page token.
func mailPage(r *http.Request) (size int, token string, err error) {
	n, err := rangedParam(r.URL.Query(), "page_size", 1, 500, "between 1 and 500")
	if err != nil {
		return 0, "", err
	}
	if n == 0 {
		n = 50
	}
	return int(n), r.URL.Query().Get("page_token"), nil
}

// listMailSuppressions is GET /v1/mail-suppressions: the addresses the tenant's mail is not sent to (a permanent bounce, a complaint, or the operator's word), in address
// order, a page at a time.
func (s *Server) listMailSuppressions(w http.ResponseWriter, r *http.Request) {
	if s.d.Mail == nil {
		noStore(w)
		writeJSONValue(w, http.StatusOK, map[string]any{"suppressions": []mailadmin.Suppression{}, "next_page_token": "", "connected": false})
		return
	}
	tid, ok := s.mailAdminTenant(w, r)
	if !ok {
		return
	}
	size, token, err := mailPage(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	rows, next, err := s.d.Mail.Suppressions(r.Context(), tid, token, size)
	if err != nil {
		mailError(w, r, err)
		return
	}
	if rows == nil {
		rows = []mailadmin.Suppression{}
	}
	noStore(w)
	writeJSONValue(w, http.StatusOK, map[string]any{"suppressions": rows, "next_page_token": next, "connected": true})
}

// deleteMailSuppression is DELETE /v1/mail-suppressions/{address}: the address is mailed again. Lifting what is not there is a 404.
func (s *Server) deleteMailSuppression(w http.ResponseWriter, r *http.Request) {
	tid, ok := s.mailAdminTenant(w, r)
	if !ok {
		return
	}
	address := strings.TrimSpace(r.PathValue("address"))
	if address == "" || len(address) > 320 || !strings.Contains(address, "@") || strings.ContainsAny(address, "/?#\\ ") {
		writeError(w, r, invalid("address", "an e-mail address"))
		return
	}
	if err := s.d.Mail.Unsuppress(r.Context(), tid, address); err != nil {
		mailError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listMailMessages is GET /v1/mail-messages: the tenant's recent mail, newest first, with what became of each, a page at a time.
func (s *Server) listMailMessages(w http.ResponseWriter, r *http.Request) {
	if s.d.Mail == nil {
		noStore(w)
		writeJSONValue(w, http.StatusOK, map[string]any{"messages": []mailadmin.Message{}, "next_page_token": "", "connected": false})
		return
	}
	tid, ok := s.mailAdminTenant(w, r)
	if !ok {
		return
	}
	size, token, err := mailPage(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	rows, next, err := s.d.Mail.Messages(r.Context(), tid, token, size)
	if err != nil {
		mailError(w, r, err)
		return
	}
	noStore(w)
	if rows == nil {
		rows = []mailadmin.Message{}
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"messages": rows, "next_page_token": next, "connected": true})
}

// previewPriceChangeMail is POST /v1/mail-previews/price-change: the notice a customer would get of a price change, rendered by the notifier in the customer's language
// from the numbers in the body. The tenant's own name, time zone and currency (and its default language, when the body names none) are the gateway's to fill in: they are
// read from plans-svc for the tenant of the credential. It sends nothing and keeps nothing.
func (s *Server) previewPriceChangeMail(w http.ResponseWriter, r *http.Request) {
	tid, ok := s.mailAdminTenant(w, r)
	if !ok {
		return
	}
	var body struct {
		Locale          string    `json:"locale"`
		CustomerName    string    `json:"customer_name"`
		PlanCode        string    `json:"plan_code"`
		EffectiveTime   time.Time `json:"effective_time"`
		NewRenewalMinor int64     `json:"new_renewal_minor"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	for field, v := range map[string]string{"customer_name": body.CustomerName, "plan_code": body.PlanCode} {
		if utf8.RuneCountInString(v) > 200 {
			writeError(w, r, invalid(field, "at most 200 characters"))
			return
		}
	}
	if body.PlanCode == "" || body.NewRenewalMinor < 0 || body.EffectiveTime.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) {
		writeError(w, r, invalid("effective_time", "a plan_code, a renewal amount and the date the new price starts are required"))
		return
	}
	tn, err := s.d.Admin.GetTenant(tenant.WithOperator(r.Context()), &plansv1.GetTenantRequest{TenantId: tid.String()})
	if err != nil {
		writeError(w, r, err)
		return
	}
	in := mailadmin.PriceChangePreview{Locale: body.Locale, CustomerName: body.CustomerName, TenantName: tn.GetTenant().GetName(), PlanCode: body.PlanCode,
		EffectiveTime: body.EffectiveTime, NewRenewalMinor: body.NewRenewalMinor, Currency: tn.GetTenant().GetCurrency(), Timezone: tn.GetTenant().GetTimezone()}
	if in.Locale == "" {
		in.Locale = tn.GetTenant().GetDefaultLanguage()
	}
	out, err := s.d.Mail.PreviewPriceChange(r.Context(), in)
	if err != nil {
		mailError(w, r, err)
		return
	}
	noStore(w)
	writeJSONValue(w, http.StatusOK, out)
}
