package mailx

import (
	"testing"
	"time"
)

// The payloads are the examples on Resend's pages for the events (read 2026-11-02), cut to what the translation reads.
func TestParseResendWebhook(t *testing.T) {
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		body string
		want []Event
	}{
		"a permanent bounce": {
			`{"type":"email.bounced","created_at":"2026-11-02T10:00:00Z","data":{"email_id":"56761188","to":["a@example.com"],"bounce":{"message":"mailbox unavailable","subType":"Suppressed","type":"Permanent"}}}`,
			[]Event{{Type: EventBounced, MessageID: "56761188", Recipient: "a@example.com", BounceType: BounceHard, OccurredAt: at, Detail: "Suppressed: mailbox unavailable"}},
		},
		"a temporary bounce is soft": {
			`{"type":"email.bounced","created_at":"2026-11-02T10:00:00Z","data":{"email_id":"m2","to":["a@example.com"],"bounce":{"type":"Temporary","message":"mailbox full"}}}`,
			[]Event{{Type: EventBounced, MessageID: "m2", Recipient: "a@example.com", BounceType: BounceSoft, OccurredAt: at, Detail: "mailbox full"}},
		},
		"a bounce of a type nobody documented is soft, never suppressing": {
			`{"type":"email.bounced","data":{"email_id":"m3","to":["a@example.com"],"bounce":{"type":"Undetermined"}}}`,
			[]Event{{Type: EventBounced, MessageID: "m3", Recipient: "a@example.com", BounceType: BounceSoft}},
		},
		"a complaint": {
			`{"type":"email.complained","created_at":"2026-11-02T10:00:00Z","data":{"email_id":"m4","to":["a@example.com"],"from":"Acme <b@acme.example>"}}`,
			[]Event{{Type: EventComplained, MessageID: "m4", Recipient: "a@example.com", OccurredAt: at}},
		},
		"delivered and delayed": {
			`{"type":"email.delivery_delayed","data":{"email_id":"m5","to":["a@example.com"]}}`,
			[]Event{{Type: EventDelayed, MessageID: "m5", Recipient: "a@example.com"}},
		},
		"one event for each recipient": {
			`{"type":"email.complained","data":{"email_id":"m6","to":["a@example.com","b@example.com"]}}`,
			[]Event{{Type: EventComplained, MessageID: "m6", Recipient: "a@example.com"}, {Type: EventComplained, MessageID: "m6", Recipient: "b@example.com"}},
		},
		"no recipient: the one the notifier recorded": {
			`{"type":"email.delivered","data":{"email_id":"m7"}}`,
			[]Event{{Type: EventDelivered, MessageID: "m7"}},
		},
	} {
		got, err := ParseResendWebhook([]byte(c.body))
		if err != nil || len(got) != len(c.want) {
			t.Errorf("%s: %v, %v", name, got, err)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: event %d = %+v, want %+v", name, i, got[i], c.want[i])
			}
			if err := got[i].Validate(); err != nil {
				t.Errorf("%s: the event does not validate: %v", name, err)
			}
		}
	}
}

func TestParseResendWebhookIgnoresWhatIsNotDelivery(t *testing.T) {
	for _, typ := range []string{"email.sent", "email.opened", "email.clicked", "email.failed", "email.suppressed", "email.received", "email.scheduled", "domain.updated"} {
		got, err := ParseResendWebhook([]byte(`{"type":"` + typ + `","data":{"email_id":"m"}}`))
		if err != nil || len(got) != 0 {
			t.Errorf("%s: %v, %v; a valid webhook that says nothing about delivery is no event and no error", typ, got, err)
		}
	}
}

func TestParseResendWebhookRefusesWhatIsNotAnEvent(t *testing.T) {
	for _, body := range []string{``, `not json`, `[]`, `{"type":"email.bounced","data":{}}`, `{"type":"email.bounced","data":{"email_id":"  "}}`} {
		if got, err := ParseResendWebhook([]byte(body)); err == nil {
			t.Errorf("%q gave %v", body, got)
		}
	}
}
