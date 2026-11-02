package command

import (
	"testing"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
)

// A decline the provider says must not be retried is a different event, so the saga skips dunning; any other decline stays the ordinary one.
func TestDeclinesThatMustNotBeRetriedAreTheirOwnEvent(t *testing.T) {
	declined := func(final bool) *commandsv1.ReplyEnvelope {
		return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentDeclined{PaymentDeclined: &commandsv1.PaymentDeclined{DeclineCode: "generic_decline", Final: final}}}
	}
	status := func(final bool) *commandsv1.ReplyEnvelope {
		return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentStatus{PaymentStatus: &commandsv1.PaymentStatus{State: commandsv1.PaymentStatus_STATE_DECLINED, Final: final}}}
	}
	for _, c := range []struct {
		name  string
		reply *commandsv1.ReplyEnvelope
		want  model.Event
	}{
		{"declined", declined(false), model.EvPaymentDeclined},
		{"declined for good", declined(true), model.EvPaymentDeclinedFinal},
		{"status declined", status(false), model.EvStatusDeclined},
		{"status declined for good", status(true), model.EvStatusDeclinedFinal},
	} {
		if got, _, ok := ReplyEvent(c.reply); !ok || got != c.want {
			t.Errorf("%s: %s %v, want %s", c.name, got, ok, c.want)
		}
	}
}

// A charge that needs the customer, and a status query that finds it still waiting, are events of their own: neither is a decline, so dunning does not start.
func TestRepliesThatAskForTheCustomerAreTheirOwnEvents(t *testing.T) {
	action := &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentActionRequired{PaymentActionRequired: &commandsv1.PaymentActionRequired{PspChargeId: "pi_1"}}}
	status := &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentStatus{PaymentStatus: &commandsv1.PaymentStatus{State: commandsv1.PaymentStatus_STATE_ACTION_REQUIRED}}}
	if got, _, ok := ReplyEvent(action); !ok || got != model.EvPaymentActionRequired {
		t.Errorf("the charge's reply: %s %v", got, ok)
	}
	if got, _, ok := ReplyEvent(status); !ok || got != model.EvStatusActionRequired {
		t.Errorf("the status reply: %s %v", got, ok)
	}
}
