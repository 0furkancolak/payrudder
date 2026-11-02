package mailx

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ParseResendWebhook turns the body of a Resend webhook into Payrudder's neutral delivery events, one for each recipient. Read 2026-11-02 at
// https://resend.com/docs/webhooks/event-types and its pages for email.bounced and email.complained: {"type":"email.bounced","created_at":…,"data":{"email_id":…,
// "to":[…],"bounce":{"type":"Permanent","subType":…,"message":…}}}. email.delivered, email.delivery_delayed, email.bounced and email.complained are understood; every
// other type (sent, opened, clicked, failed, suppressed, received, scheduled) is a valid webhook that says nothing about delivery and returns no events, not an error.
//
// A bounce is hard only when its type is "Permanent" (the page names "Permanent" and gives "Temporary" as an example, and does not list the others): anything else
// is soft, which never suppresses an address. UNVERIFIED: that Nafru's webhooks have this shape.
func ParseResendWebhook(body []byte) ([]Event, error) {
	var in struct {
		Type      string    `json:"type"`
		CreatedAt time.Time `json:"created_at"`
		Data      struct {
			EmailID string   `json:"email_id"`
			To      []string `json:"to"`
			Bounce  struct {
				Type    string `json:"type"`
				SubType string `json:"subType"`
				Message string `json:"message"`
			} `json:"bounce"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, errors.New("the body is not a webhook event")
	}
	var typ EventType
	switch in.Type {
	case "email.delivered":
		typ = EventDelivered
	case "email.delivery_delayed":
		typ = EventDelayed
	case "email.bounced":
		typ = EventBounced
	case "email.complained":
		typ = EventComplained
	default:
		return nil, nil
	}
	if strings.TrimSpace(in.Data.EmailID) == "" {
		return nil, errors.New("the event names no email_id")
	}
	e := Event{Type: typ, MessageID: in.Data.EmailID, OccurredAt: in.CreatedAt}
	if typ == EventBounced {
		e.BounceType = BounceSoft
		if in.Data.Bounce.Type == "Permanent" {
			e.BounceType = BounceHard
		}
		e.Detail = strings.TrimSpace(strings.Join([]string{in.Data.Bounce.SubType, in.Data.Bounce.Message}, ": "))
		e.Detail = strings.Trim(e.Detail, ": ")
	}
	if len(in.Data.To) == 0 {
		return []Event{e}, nil
	}
	out := make([]Event, 0, len(in.Data.To))
	for _, to := range in.Data.To {
		e.Recipient = to
		out = append(out, e)
	}
	return out, nil
}
