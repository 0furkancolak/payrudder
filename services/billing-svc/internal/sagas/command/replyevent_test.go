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
