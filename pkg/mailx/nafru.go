package mailx

import (
	"context"
	"strings"
)

// Nafru sends through Nafru Email. Its API is described as Resend-compatible (Nafru's official SDK is Resend's with another base URL), so the wire shape is
// Resend's (resend.go). Everything below is what Payrudder ADDS for Nafru, and none of it has been confirmed against Nafru's documentation or a Nafru account:
//
//   - UNVERIFIED: that Nafru Email applications (where a customer edits Payrudder's mail) can match a message by the tag "payrudder_template" or the header
//     "X-Payrudder-Template" and apply a template of their own. Payrudder sends both and renders its own HTML either way, so a Nafru that ignores them changes nothing.
//   - UNVERIFIED: Nafru's webhooks (delivery, bounce, complaint): their payload, signature and event names. The notifier's mail-event endpoint takes Payrudder's
//     neutral Event (event.go); a Nafru delivery needs a translating layer once Nafru's contract is known. Resend's own webhooks are signed with Svix, which this
//     endpoint does not speak.
//   - UNVERIFIED: Nafru's sending limits, its sender-domain verification and its idempotency window (Resend's is 24 hours, 256 characters).
//   - UNVERIFIED: that Nafru accepts Resend's `attachments` (filename and base64 content). Message.Attachments are sent that way; the only mail that carries one (the copy of an
//     accepted agreement) also holds the same page in its body, so a Nafru that drops or refuses the field loses the PDF, not the evidence.
//
// NOT BUILT: a "template mode" in which Payrudder sends the template id and its variables and Nafru renders (docs/email-template-contract.md lists what would be
// sent). It needs Nafru's template contract; until then Payrudder renders every mail.
type Nafru struct{ Resend }

// Template tag and header: which Payrudder mail a message is (the template name with its version, "invoice_paid.v1").
const (
	TemplateTag    = "payrudder_template"
	TemplateHeader = "X-Payrudder-Template"
)

// Send implements Sender: the message as Resend sends it, plus the template's name as a tag and a header. A name a Resend-style provider would refuse in a tag
// (ASCII letters, digits, underscores and dashes only, 256 characters at most) is left out instead of failing the send; the version's dot is written as an
// underscore so that "invoice_paid.v1" is a valid tag value.
func (n Nafru) Send(ctx context.Context, m Message) (string, error) {
	if v := tagValue(m.Template); v != "" {
		m.Tags = append(m.Tags[:len(m.Tags):len(m.Tags)], Tag{Name: TemplateTag, Value: v})
		h := map[string]string{TemplateHeader: m.Template}
		for k, x := range m.Headers {
			h[k] = x
		}
		m.Headers = h
	}
	return n.Resend.Send(ctx, m)
}

// tagValue is the template name as a tag value, or "" when it cannot be one.
func tagValue(template string) string {
	v := strings.ReplaceAll(template, ".", "_")
	if v == "" || len(v) > 256 {
		return ""
	}
	for _, r := range v {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if !letter && (r < '0' || r > '9') && r != '_' && r != '-' {
			return ""
		}
	}
	return v
}
