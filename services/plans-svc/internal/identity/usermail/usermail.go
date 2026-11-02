// Package usermail sends the mail Payrudder itself writes to people (not to a tenant's customers, which is the notifier's job through the tenant's own mail
// switch): an address to verify, a password to reset, an invitation to join an account, the warning that somebody tried to sign up with an address that
// already has an account, the help desk's notices (a new ticket for the operators, an answer or a new status for the person), and the notice to the owners of an account
// that the operator took a live app out of service or put it back (plan 14). Texts exist in the six languages of the console. A mail is sent in the background with a bounded number at a time: the request that
// caused it never waits for the SMTP relay, and a lost mail is something the person can ask for again (resend, forgot password).
package usermail

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"

	"github.com/0furkancolak/payrudder/pkg/errtrack"
	"github.com/0furkancolak/payrudder/pkg/mailx"
)

// Kind is what a mail is for.
type Kind string

// The mails to people.
const (
	// KindVerify carries the link that proves an address.
	KindVerify Kind = "verify"
	// KindReset carries the link that sets a new password.
	KindReset Kind = "reset"
	// KindInvite invites the address to join an account.
	KindInvite Kind = "invite"
	// KindTaken tells the owner of an address that somebody tried to sign up with it. It carries no link to anything that acts.
	KindTaken Kind = "taken"
	// KindCode carries a one-time sign-in code (and no link): the person types it into the page that asked for it.
	KindCode Kind = "code"
	// KindTicketNew tells an operator that an account opened a support ticket.
	KindTicketNew Kind = "ticket_new"
	// KindTicketCustomerReply tells the operators that the person answered the operator on a ticket (plan 12): one mail when the ticket was waiting for them.
	KindTicketCustomerReply Kind = "ticket_customer_reply"
	// KindTicketReply tells the person who opened a ticket that the operator answered it.
	KindTicketReply Kind = "ticket_reply"
	// KindTicketStatus tells the person who opened a ticket that its status changed (the operator solved or closed it, or it closed by itself).
	KindTicketStatus Kind = "ticket_status"
	// KindTenantSuspended, KindTenantFrozen and KindTenantBanned tell the owners of an account that the operator took a live app out of service, and
	// KindTenantReinstated that it is back (plan 14). They name the app and say what stops; the operator's reason is not in them.
	KindTenantSuspended  Kind = "tenant_suspended"
	KindTenantFrozen     Kind = "tenant_frozen"
	KindTenantBanned     Kind = "tenant_banned"
	KindTenantReinstated Kind = "tenant_reinstated"
	// KindTenantAppealAccepted and KindTenantAppealRejected tell the owners how the operator decided an appeal against a hold (plan 14), with the operator's answer.
	KindTenantAppealAccepted Kind = "tenant_appeal_accepted"
	KindTenantAppealRejected Kind = "tenant_appeal_rejected"
	// KindTwoFactorOn and KindTwoFactorOff tell the person that two-factor sign-in was turned on or off for their account, KindRecoveryUsed that a recovery code
	// signed somebody in, and KindTwoFactorReset that support turned it off at their request (plan 22 G9). They carry no link: what to do if it was not the
	// person is in the text.
	KindTwoFactorOn    Kind = "two_factor_on"
	KindTwoFactorOff   Kind = "two_factor_off"
	KindRecoveryUsed   Kind = "two_factor_recovery_used"
	KindTwoFactorReset Kind = "two_factor_reset"
)

// Mail is one message to compose. Link is the full URL the button opens; the token inside it is the only secret.
type Mail struct {
	Kind   Kind
	To     string
	Locale string
	Link   string
	// Account is the organisation an invitation is for; Inviter is who invited (a display name or an address); Role is the role offered. App is the app a tenant
	// status mail is about.
	Account string
	App     string
	Inviter string
	Role    string
	// Code is the one-time sign-in code of a KindCode mail.
	Code string
	// Ticket is the ticket's number as people write it ("#1042"), Subject its subject, Status and Priority its state (named in the mail's language). The link of a
	// ticket mail opens the ticket in the console. Account is the organisation that opened it.
	Ticket, Subject, Status, Priority string
	// Answer is the operator's answer to an appeal; a line that names {answer} is left out when there is none.
	Answer string
}

// Locales are the languages mail is written in, as in the console.
var Locales = []string{"en", "tr", "de", "fr", "es", "ar"}

// Locale returns l when mail exists in it, else English.
func Locale(l string) string {
	for _, x := range Locales {
		if x == l {
			return l
		}
	}
	return "en"
}

// Sender is what the commands call to send a mail; it returns at once.
type Sender interface {
	Send(ctx context.Context, m Mail)
}

// SMTP sends mail through a relay in the background.
type SMTP struct {
	Relay mailx.Sender
	From  string
	Log   *slog.Logger
	slots chan struct{}
}

// maxInFlight bounds the mails being sent at once; one more waits no longer than a moment and is then dropped (and logged): the person asks again.
const maxInFlight = 8

// NewSMTP returns a background sender.
func NewSMTP(relay mailx.Sender, from string, log *slog.Logger) *SMTP {
	return &SMTP{Relay: relay, From: from, Log: log, slots: make(chan struct{}, maxInFlight)}
}

// Send renders the mail and sends it in a goroutine. The context's cancellation does not stop it (the request is over by then); the relay has its own timeout.
func (s *SMTP) Send(reqCtx context.Context, m Mail) {
	select {
	case s.slots <- struct{}{}:
	case <-time.After(500 * time.Millisecond):
		s.Log.Warn("mail to a person dropped: sender busy", "kind", m.Kind)
		return
	}
	go func() {
		defer func() { <-s.slots }()
		defer errtrack.Recover(reqCtx)
		subject, text, body := Render(m)
		// WithoutCancel keeps the request's values (trace, request id) but not its end: the mail outlives the request.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), 20*time.Second)
		defer cancel()
		// The address is not logged: it is personal data and the log line only needs to say that a mail failed.
		if _, err := s.Relay.Send(ctx, mailx.Message{From: s.From, To: m.To, Subject: subject, Text: text, HTML: body}); err != nil {
			s.Log.Warn("mail to a person not sent", "kind", m.Kind, "error", err)
		}
	}()
}

// copy is the text of one mail in one language. Lines is the body; Button is the label of the link; Ignore is the closing line.
type copyText struct {
	Subject string
	Lines   []string
	Button  string
	Ignore  string
}

// Render returns the subject, the plain text and the HTML of a mail. Every value that came from a person (names) is escaped in the HTML and cannot add a
// header: the SMTP sender refuses line breaks in subject and addresses, and the subject carries no name from outside.
func Render(m Mail) (subject, text, htmlBody string) {
	loc := Locale(m.Locale)
	c := texts[loc][m.Kind]
	// One pass, so a name that looks like a placeholder (a ticket subject "{status}") is not filled in again.
	fillWith := strings.NewReplacer("{account}", oneLine(m.Account), "{app}", oneLine(m.App), "{inviter}", oneLine(m.Inviter), "{code}", oneLine(m.Code), "{role}", roleWord(loc, m.Role),
		"{ticket}", oneLine(m.Ticket), "{subject}", oneLine(m.Subject), "{status}", ticketWord(loc, m.Status), "{priority}", ticketWord(loc, m.Priority), "{answer}", oneLine(m.Answer))
	fill := fillWith.Replace
	lines := make([]string, len(c.Lines))
	lines = lines[:0]
	for _, l := range c.Lines {
		if m.Answer == "" && strings.Contains(l, "{answer}") {
			continue
		}
		lines = append(lines, fill(l))
	}
	subject = fill(c.Subject)
	c.Ignore = fill(c.Ignore)
	var t strings.Builder
	for _, l := range lines {
		t.WriteString(l + "\n\n")
	}
	if m.Link != "" {
		t.WriteString(c.Button + ": " + m.Link + "\n\n")
	}
	t.WriteString(c.Ignore + "\n\nPayrudder\n")

	dir := "ltr"
	if loc == "ar" {
		dir = "rtl"
	}
	var h strings.Builder
	fmt.Fprintf(&h, `<!doctype html><html lang="%s" dir="%s"><body style="font-family:system-ui,sans-serif;color:#1c2330;max-width:520px;margin:24px auto;padding:0 16px">`, loc, dir)
	for _, l := range lines {
		fmt.Fprintf(&h, `<p style="line-height:1.5">%s</p>`, html.EscapeString(l))
	}
	if m.Link != "" {
		fmt.Fprintf(&h, `<p><a href="%s" style="display:inline-block;background:#2f6fd6;color:#fff;text-decoration:none;padding:10px 18px;border-radius:10px">%s</a></p>`,
			html.EscapeString(m.Link), html.EscapeString(c.Button))
		fmt.Fprintf(&h, `<p style="color:#5b6577;font-size:13px;word-break:break-all">%s</p>`, html.EscapeString(m.Link))
	}
	fmt.Fprintf(&h, `<p style="color:#5b6577;font-size:13px">%s</p><p style="color:#5b6577;font-size:13px">Payrudder</p></body></html>`, html.EscapeString(c.Ignore))
	return subject, t.String(), h.String()
}

// oneLine keeps a name from adding lines to a text it is placed in.
func oneLine(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}

func roleWord(loc, role string) string {
	if w, ok := roles[loc][role]; ok {
		return w
	}
	return role
}

// ticketWord is the word for a ticket status or priority in a language (the value itself when there is none).
func ticketWord(loc, key string) string {
	if w, ok := ticketWords[loc][key]; ok {
		return w
	}
	return key
}
