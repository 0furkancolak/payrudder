// Package stubworker is a scriptable stand-in for invoice-worker, payments-worker and notifier. It consumes the five
// command queues and answers on billing.replies, so billing-svc's saga can be tested without the real workers.
package stubworker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"google.golang.org/protobuf/proto"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
)

// Action is what the worker does with one command.
type Action struct {
	// Reply is the reply payload (one of the *commandsv1 reply messages, set through Reply helpers). Nil sends nothing,
	// which is how a worker that never answers is scripted.
	Reply func(cmd *commandsv1.CommandEnvelope) *commandsv1.ReplyEnvelope
	// Copies is how many times the reply is published (2 scripts a duplicate reply). 0 means 1.
	Copies int
	// Delay waits before replying.
	Delay time.Duration
}

// Script decides the action for a command. attempt counts the commands seen so far with the same saga and kind.
type Script func(cmd *commandsv1.CommandEnvelope, seen int) Action

// Worker consumes command queues and replies per its Script.
type Worker struct {
	URL    string
	Script Script
	Log    *slog.Logger

	mu       sync.Mutex
	commands []*commandsv1.CommandEnvelope
	byID     map[string]bool
	perKind  map[string]int
	pub      *rabbitx.Publisher
}

// New returns a Worker with the happy-path script when script is nil.
func New(url string, script Script) *Worker {
	if script == nil {
		script = func(*commandsv1.CommandEnvelope, int) Action { return Action{Reply: Succeed} }
	}
	return &Worker{URL: url, Script: script, Log: slog.Default(), byID: map[string]bool{}, perKind: map[string]int{}}
}

// Commands returns every distinct command received so far, in arrival order (redeliveries of a command id are folded).
func (w *Worker) Commands() []*commandsv1.CommandEnvelope {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]*commandsv1.CommandEnvelope(nil), w.commands...)
}

// Count returns how many distinct commands of the kind ("render", "charge", "refund", "status", "notify", "einvoice") arrived.
func (w *Worker) Count(kind string) int {
	n := 0
	for _, c := range w.Commands() {
		if Kind(c) == kind {
			n++
		}
	}
	return n
}

// Kind names the command kind of an envelope.
func Kind(c *commandsv1.CommandEnvelope) string {
	switch c.GetCommand().(type) {
	case *commandsv1.CommandEnvelope_RenderInvoicePdf:
		return "render"
	case *commandsv1.CommandEnvelope_ChargePayment:
		return "charge"
	case *commandsv1.CommandEnvelope_RefundPayment:
		return "refund"
	case *commandsv1.CommandEnvelope_QueryPaymentStatus:
		return "status"
	case *commandsv1.CommandEnvelope_SendNotification:
		return "notify"
	case *commandsv1.CommandEnvelope_IssueEInvoice:
		return "einvoice"
	}
	return "unknown"
}

// Run declares the topology and consumes every command queue until ctx is done.
func (w *Worker) Run(ctx context.Context) error {
	if err := rabbitx.DeclareTopology(w.URL); err != nil {
		return err
	}
	w.pub = rabbitx.NewPublisher(w.URL)
	defer w.pub.Close()
	var wg sync.WaitGroup
	for _, q := range rabbitx.CommandQueues {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rabbitx.Consume(ctx, w.URL, q, 16, w.handle, w.Log)
		}()
	}
	wg.Wait()
	return nil
}

func (w *Worker) handle(ctx context.Context, d amqp.Delivery) rabbitx.Outcome {
	var cmd commandsv1.CommandEnvelope
	if err := proto.Unmarshal(d.Body, &cmd); err != nil {
		return rabbitx.Reject
	}
	w.mu.Lock()
	fresh := !w.byID[cmd.GetCommandId()]
	var seen int
	if fresh {
		w.byID[cmd.GetCommandId()] = true
		w.commands = append(w.commands, &cmd)
		key := cmd.GetSagaId() + "|" + Kind(&cmd)
		seen = w.perKind[key]
		w.perKind[key]++
	}
	w.mu.Unlock()
	if !fresh {
		// A redelivered command: the real workers return the stored reply; the stub just acks.
		return rabbitx.Ack
	}
	act := w.Script(&cmd, seen)
	if act.Reply == nil {
		return rabbitx.Ack
	}
	if act.Delay > 0 {
		select {
		case <-time.After(act.Delay):
		case <-ctx.Done():
			return rabbitx.Requeue
		}
	}
	reply := act.Reply(&cmd)
	reply.CommandId, reply.SagaId, reply.TenantId = cmd.GetCommandId(), cmd.GetSagaId(), cmd.GetTenantId()
	reply.ReplyId = uuid.NewSHA1(uuid.NameSpaceOID, []byte(cmd.GetCommandId()+"|reply")).String()
	body, err := proto.Marshal(reply)
	if err != nil {
		return rabbitx.Reject
	}
	copies := max(act.Copies, 1)
	for range copies {
		if err := w.pub.Publish(ctx, rabbitx.Message{
			Exchange: rabbitx.ExchangeReplies, RoutingKey: rabbitx.ReplyRoutingKey,
			Publishing: amqp.Publishing{ContentType: "application/x-protobuf", Body: body, MessageId: reply.GetReplyId()},
		}); err != nil {
			w.Log.Warn("stub worker: reply publish failed", "error", err)
			return rabbitx.Requeue
		}
	}
	return rabbitx.Ack
}

// Succeed answers every command with its success reply.
func Succeed(cmd *commandsv1.CommandEnvelope) *commandsv1.ReplyEnvelope {
	switch c := cmd.GetCommand().(type) {
	case *commandsv1.CommandEnvelope_RenderInvoicePdf:
		return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PdfRendered{PdfRendered: &commandsv1.PdfRendered{
			ObjectKey: cmd.GetTenantId() + "/" + c.RenderInvoicePdf.GetInvoice().GetInvoiceId() + "/v1.pdf", Sha256: "stub", SizeBytes: 1}}}
	case *commandsv1.CommandEnvelope_ChargePayment:
		return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentSucceeded{PaymentSucceeded: &commandsv1.PaymentSucceeded{PspChargeId: "ch_" + cmd.GetCommandId()}}}
	case *commandsv1.CommandEnvelope_RefundPayment:
		return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentRefunded{PaymentRefunded: &commandsv1.PaymentRefunded{PspRefundId: "re_" + cmd.GetCommandId()}}}
	case *commandsv1.CommandEnvelope_QueryPaymentStatus:
		return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentStatus{PaymentStatus: &commandsv1.PaymentStatus{
			State: commandsv1.PaymentStatus_STATE_SUCCEEDED, PspChargeId: "ch_found"}}}
	case *commandsv1.CommandEnvelope_IssueEInvoice:
		if c.IssueEInvoice.GetAction() == "cancel" {
			return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_EInvoiceResult{EInvoiceResult: &commandsv1.EInvoiceResult{State: "cancelled"}}}
		}
		return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_EInvoiceResult{EInvoiceResult: &commandsv1.EInvoiceResult{
			State: "issued", LegalNumber: "STB" + c.IssueEInvoice.GetDocumentId()[:8], ExternalId: "ext_" + c.IssueEInvoice.GetDocumentId(), DocumentType: "e-arsiv", Provider: "mock",
			XmlObjectKey: cmd.GetTenantId() + "/einvoice/" + c.IssueEInvoice.GetDocumentId() + "/document.xml"}}}
	default:
		return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_NotificationSent{NotificationSent: &commandsv1.NotificationSent{MessageId: "msg_" + cmd.GetCommandId()}}}
	}
}

// Fixed builds a reply constructor that ignores the command.
func Fixed(r *commandsv1.ReplyEnvelope) func(*commandsv1.CommandEnvelope) *commandsv1.ReplyEnvelope {
	return func(*commandsv1.CommandEnvelope) *commandsv1.ReplyEnvelope {
		return proto.Clone(r).(*commandsv1.ReplyEnvelope)
	}
}

// Declined is a PaymentDeclined reply.
func Declined(*commandsv1.CommandEnvelope) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentDeclined{PaymentDeclined: &commandsv1.PaymentDeclined{DeclineCode: "card_declined"}}}
}

// Errored is a PaymentErrored reply (the charge's fate is unknown).
func Errored(*commandsv1.CommandEnvelope) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentErrored{PaymentErrored: &commandsv1.PaymentErrored{Code: "timeout", Message: "psp timed out"}}}
}

// Pending is the reply to a charge the provider accepted and settles later (a bank transfer): not a failure, billing asks for the status.
func Pending(*commandsv1.CommandEnvelope) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentErrored{PaymentErrored: &commandsv1.PaymentErrored{Code: "PENDING", Message: "awaiting the transfer"}}}
}

// StatusPending is a payment status reply saying the payment has not settled yet.
func StatusPending(*commandsv1.CommandEnvelope) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentStatus{PaymentStatus: &commandsv1.PaymentStatus{State: commandsv1.PaymentStatus_STATE_PENDING}}}
}

// PdfFailed is a permanent render failure.
func PdfFailed(*commandsv1.CommandEnvelope) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PdfFailed{PdfFailed: &commandsv1.PdfFailed{Reason: "template error"}}}
}

// EInvoiceRetryable is a failed e-invoice answer that billing-svc retries with backoff.
func EInvoiceRetryable(*commandsv1.CommandEnvelope) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_EInvoiceResult{EInvoiceResult: &commandsv1.EInvoiceResult{
		State: "failed", Retryable: true, ErrorKey: "einvoice.provider.unavailable", ErrorMessage: "timeout"}}}
}

// EInvoiceRejected is a failed e-invoice answer that waits for the operator.
func EInvoiceRejected(*commandsv1.CommandEnvelope) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_EInvoiceResult{EInvoiceResult: &commandsv1.EInvoiceResult{
		State: "failed", ErrorKey: "einvoice.provider.rejected", ErrorMessage: "VKN not found"}}}
}

// StatusNotFound is a payment status reply saying the PSP never saw the charge.
func StatusNotFound(*commandsv1.CommandEnvelope) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentStatus{PaymentStatus: &commandsv1.PaymentStatus{State: commandsv1.PaymentStatus_STATE_NOT_FOUND}}}
}
