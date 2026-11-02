// Package disputes serves RecordPaymentDispute of BillingService: payments-worker tells billing that a provider says a customer questioned a payment with their bank, or
// that the case moved on or ended. Billing announces each step once (the webhook payment.disputed, an audit entry) and, when the seller asked for it, blocks the customer.
package disputes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantevents"
)

// Blocker is the part of plans-svc's PlansService that blocks a customer.
type Blocker interface {
	BlockCustomer(ctx context.Context, in *plansv1.BlockCustomerRequest, opts ...grpc.CallOption) (*plansv1.BlockCustomerResponse, error)
}

// Handler implements RecordPaymentDispute of billingv1.BillingServiceServer.
type Handler struct {
	system *tenantdb.DB
	plans  Blocker
}

// New returns the handler over the billing_system database; plans nil never blocks.
func New(system *tenantdb.DB, plans Blocker) *Handler { return &Handler{system: system, plans: plans} }

// RecordPaymentDispute implements billingv1.BillingServiceServer. It is idempotent per (provider, dispute, outcome, and for an open case whether it is a chargeback: Stripe's
// inquiry that escalates is the same dispute): the webhook event has a key of its own, and the audit entry is written only when the event was new to the outbox. The block is asked for again on every call that finds a formal dispute open, because a block that failed
// must be tried again and a second block of a blocked customer changes nothing.
func (h *Handler) RecordPaymentDispute(ctx context.Context, req *billingv1.RecordPaymentDisputeRequest) (*billingv1.RecordPaymentDisputeResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	invoice, err := errkey.ParseUUID("billing.request.bad_id", "invoice_id", req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	switch req.GetOutcome() {
	case "open", "won", "lost":
	default:
		return nil, errkey.New(codes.InvalidArgument, "common.invalid_argument", "outcome must be open, won or lost")
	}
	if req.GetDisputeRef() == "" || req.GetProvider() == "" {
		return nil, errkey.New(codes.InvalidArgument, "common.invalid_argument", "provider and dispute_ref are required")
	}
	var out billingv1.RecordPaymentDisputeResponse
	var customer string
	var block bool
	err = h.system.InTx(ctx, ti.ID, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		inv, err := q.GetInvoiceBrief(ctx, sqlc.GetInvoiceBriefParams{TenantID: ti.ID, ID: invoice})
		if err != nil {
			return err
		}
		customer = inv.CustomerExternalID
		at := time.Now().UTC()
		if t := req.GetOccurredTime(); t != nil {
			at = t.AsTime()
		}
		data := map[string]any{
			"invoice_id": invoice.String(), "invoice_number": inv.Number, "customer_external_id": inv.CustomerExternalID, "provider": req.GetProvider(), "dispute_id": req.GetDisputeRef(),
			"payment_ref": req.GetPaymentRef(), "amount_minor": strconv.FormatInt(req.GetAmountMinor(), 10), "currency": req.GetCurrency(), "outcome": req.GetOutcome(), "formal": req.GetFormal(),
			"reason": req.GetReason(), "provider_status": req.GetProviderStatus(),
		}
		if t := req.GetEvidenceDueTime(); t != nil {
			data["evidence_due_time"] = t.AsTime().UTC().Format(time.RFC3339)
		}
		key := req.GetProvider() + "|" + req.GetDisputeRef() + "|" + req.GetOutcome()
		if req.GetOutcome() == "open" && req.GetFormal() {
			key += "|formal" // the chargeback an inquiry became is a step of its own; the key of the inquiry (and of every event before this) is unchanged
		}
		out.Announced, err = tenantevents.WriteNew(ctx, tx, ti.ID, tenantevents.EventPaymentDisputed, key, at, data)
		if err != nil {
			return err
		}
		if req.GetOutcome() == "open" && req.GetFormal() {
			if s, err := q.GetDunningSettings(ctx, ti.ID); err == nil {
				block = s.BlockOnChargeback
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if !out.Announced {
			return nil
		}
		return audit.Record(ctx, tx, audit.Event{TenantID: ti.ID, Action: "payment.dispute_" + req.GetOutcome(), TargetType: "invoice", TargetID: invoice.String(),
			Summary: fmt.Sprintf("%s: a dispute of invoice %s (%d %s%s)", map[string]string{"open": "opened", "won": "won", "lost": "lost"}[req.GetOutcome()], inv.Number, req.GetAmountMinor(), req.GetCurrency(),
				map[bool]string{true: "", false: ", an inquiry"}[req.GetFormal()]),
			Detail: map[string]any{"provider": req.GetProvider(), "dispute_id": req.GetDisputeRef(), "payment_ref": req.GetPaymentRef(), "amount_minor": req.GetAmountMinor(), "currency": req.GetCurrency(),
				"outcome": req.GetOutcome(), "formal": req.GetFormal(), "reason": req.GetReason(), "provider_status": req.GetProviderStatus()}})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errkey.New(codes.NotFound, "billing.invoice.not_found", "invoice not found")
	}
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	if block && customer != "" && h.plans != nil {
		// The seller asked for it (dunning settings): a formal dispute blocks the customer. Blocking twice keeps the first block, so a repeat is harmless.
		_, err := h.plans.BlockCustomer(inputs.TenantCtx(ctx, ti.ID), &plansv1.BlockCustomerRequest{ExternalId: customer, Category: "chargeback",
			Reason: "a chargeback was opened (" + req.GetProvider() + " " + req.GetDisputeRef() + ")", Actor: "system"})
		if err != nil {
			return nil, apierr.Internal(ctx, fmt.Errorf("block the customer of a chargeback: %w", err))
		}
		out.CustomerBlocked = true
	}
	return &out, nil
}
