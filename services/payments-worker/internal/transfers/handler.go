// Package transfers serves the operator endpoints of bank transfers: the state of an invoice's transfer, confirming a receipt and giving up on a pending
// transfer. It maps requests to the commands and queries of its subpackages (ADR 0020); the rules are there.
package transfers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/transfers/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/transfers/query"
)

// ActorHeader names who an operator action is recorded for (the gateway passes the caller's X-Payrudder-Actor).
const ActorHeader = "X-Payrudder-Actor"

const maxBody = 16 << 10

// Handler serves the transfer endpoints.
type Handler struct {
	Deps command.Deps
	Log  *slog.Logger
}

// Register adds the routes to mux, each wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	const base = "/admin/tenants/{tenant}/invoices/{invoice}/transfer"
	mux.HandleFunc("GET "+base, guard(h.get))
	mux.HandleFunc("POST "+base+"/receipts", guard(h.confirm))
	mux.HandleFunc("POST "+base+"/reject", guard(h.reject))
}

func ids(r *http.Request) (tenant, invoice uuid.UUID, err error) {
	if tenant, err = errkey.ParseUUID("payments.transfer.bad_tenant", "tenant", r.PathValue("tenant")); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if invoice, err = errkey.ParseUUID("payments.transfer.bad_invoice_id", "invoice", r.PathValue("invoice")); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return tenant, invoice, nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	t, inv, err := ids(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	st, err := query.Get(r.Context(), h.Deps.Deps, t, inv)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	write(w, http.StatusOK, st)
}

// minor reads an amount of minor units written as a JSON number or as a string (the API writes int64 as strings elsewhere).
type minor int64

func (m *minor) UnmarshalJSON(b []byte) error {
	s := string(bytes.Trim(b, `"`))
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return errors.New("amount_minor must be an integer")
	}
	*m = minor(n)
	return nil
}

type receiptBody struct {
	AmountMinor   minor     `json:"amount_minor"`
	Currency      string    `json:"currency"`
	ReceivedAt    time.Time `json:"received_at"`
	BankReference string    `json:"bank_reference"`
	Note          string    `json:"note"`
}

func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) {
	t, inv, err := ids(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var body receiptBody
	if err := decode(w, r, &body); err != nil {
		h.fail(w, r, err)
		return
	}
	res, err := command.Confirm(r.Context(), h.Deps, command.Receipt{Tenant: t, Invoice: inv, AmountMinor: int64(body.AmountMinor), Currency: body.Currency, ReceivedAt: body.ReceivedAt,
		BankReference: body.BankReference, Note: body.Note, Actor: r.Header.Get(ActorHeader)})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	code := http.StatusCreated
	if res.Duplicate {
		code = http.StatusOK
	}
	write(w, code, map[string]any{"receipt": res.Receipt, "duplicate": res.Duplicate, "paid": res.Paid, "saga_notified": res.SagaNotified, "transfer": res.State})
}

type rejectBody struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request) {
	t, inv, err := ids(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var body rejectBody
	if err := decode(w, r, &body); err != nil {
		h.fail(w, r, err)
		return
	}
	st, err := command.Reject(r.Context(), h.Deps, command.Close{Tenant: t, Invoice: inv, Outcome: body.Outcome, Reason: body.Reason, Actor: r.Header.Get(ActorHeader)})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	write(w, http.StatusOK, st)
}

// decode reads a JSON body; an empty body leaves v as it is.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		return errkey.New(codes.InvalidArgument, "payments.transfer.bad_body", "unreadable or too large body")
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, v); err != nil {
		return errkey.Newf(codes.InvalidArgument, "payments.transfer.bad_body", "malformed JSON: %v", err)
	}
	return nil
}

func write(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

var httpCodes = map[codes.Code]int{
	codes.InvalidArgument: http.StatusBadRequest, codes.NotFound: http.StatusNotFound, codes.AlreadyExists: http.StatusConflict,
	codes.Aborted: http.StatusConflict, codes.FailedPrecondition: http.StatusConflict, codes.Unavailable: http.StatusServiceUnavailable,
}

var codeNames = map[codes.Code]string{
	codes.InvalidArgument: "INVALID_ARGUMENT", codes.NotFound: "NOT_FOUND", codes.AlreadyExists: "ALREADY_EXISTS", codes.Aborted: "ABORTED",
	codes.FailedPrecondition: "FAILED_PRECONDITION", codes.Unavailable: "UNAVAILABLE",
}

// fail writes the gateway's error envelope, so the response reads the same whether the gateway or this worker produced it:
// {"error": {"code": "FAILED_PRECONDITION", "key": "payments.transfer.overpayment", "message": "…"}}.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	st := status.Convert(err)
	code, ok := httpCodes[st.Code()]
	body := map[string]string{"code": codeNames[st.Code()], "key": errkey.Of(err), "message": st.Message()}
	if !ok {
		code = http.StatusInternalServerError
		body["code"], body["message"] = "INTERNAL", "internal error"
		h.Log.ErrorContext(r.Context(), "transfer request failed", "path", r.URL.Path, "err", err)
	}
	if body["key"] == "" {
		body["key"] = errkey.Default(st.Code())
		if !ok {
			body["key"] = errkey.Default(codes.Internal)
		}
	}
	write(w, code, map[string]any{"error": body})
}

// BillingClient lets the commands ask billing-svc about an invoice and nudge its saga. The payments-worker is an internal caller: it acts for the tenant
// and, for the nudge, as the operator.
type BillingClient struct {
	Invoices billingv1.BillingServiceClient
	Admin    billingv1.BillingAdminServiceClient
}

// InvoiceStatus implements command.Billing.
func (b BillingClient) InvoiceStatus(ctx context.Context, t, invoice uuid.UUID) (string, bool, error) {
	ctx = tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: t}))
	r, err := b.Invoices.GetInvoice(ctx, &billingv1.GetInvoiceRequest{InvoiceId: invoice.String()})
	if status.Code(err) == codes.NotFound {
		return "", false, nil
	}
	return r.GetInvoice().GetStatus(), err == nil, err
}

// Nudge implements command.Billing: RetryInvoiceSaga re-asks the payment status of a saga that is waiting for it.
func (b BillingClient) Nudge(ctx context.Context, t, invoice uuid.UUID) error {
	ctx = tenant.OutgoingContext(tenant.WithOperator(ctx))
	_, err := b.Admin.RetryInvoiceSaga(ctx, &billingv1.RetryInvoiceSagaRequest{TenantId: t.String(), InvoiceId: invoice.String()})
	return err
}
