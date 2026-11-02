package usermail

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/mailx"
)

var placeholder = regexp.MustCompile(`\{[a-z]+\}`)

func placeholders(c copyText) string {
	all := c.Subject + "|" + strings.Join(c.Lines, "|") + "|" + c.Ignore
	found := placeholder.FindAllString(all, -1)
	sort.Strings(found) // a language may put them in another order
	return strings.Join(found, ",")
}

func TestEveryLanguageHasEveryMail(t *testing.T) {
	kinds := []Kind{KindVerify, KindReset, KindInvite, KindTaken, KindCode, KindTicketNew, KindTicketReply, KindTicketStatus,
		KindTenantSuspended, KindTenantFrozen, KindTenantBanned, KindTenantReinstated}
	for _, l := range Locales {
		for _, k := range kinds {
			got, ok := texts[l][k]
			if !ok || got.Subject == "" || len(got.Lines) == 0 || got.Ignore == "" {
				t.Fatalf("%s/%s: incomplete mail text", l, k)
			}
			en := texts["en"][k]
			if len(got.Lines) != len(en.Lines) {
				t.Errorf("%s/%s: %d lines, English has %d", l, k, len(got.Lines), len(en.Lines))
			}
			// The same placeholders, in any language: a translation that drops {account} would send a mail naming nothing.
			if placeholders(got) != placeholders(en) {
				t.Errorf("%s/%s: placeholders %q differ from English %q", l, k, placeholders(got), placeholders(en))
			}
			if (k == KindTaken || k == KindCode) != (got.Button == "") {
				t.Errorf("%s/%s: only the sign-up warning and the code mail have no button", l, k)
			}
		}
		for _, r := range []string{"owner", "admin", "viewer"} {
			if roles[l][r] == "" {
				t.Errorf("%s: no word for role %s", l, r)
			}
		}
		for _, w := range []string{"open", "pending", "solved", "closed", "low", "normal", "high", "urgent"} {
			if ticketWords[l][w] == "" {
				t.Errorf("%s: no word for ticket %s", l, w)
			}
		}
	}
}

// A tenant status mail names the app and the account in every language, leaves no placeholder behind, keeps a name on one line and escapes it in the HTML, and says
// nothing of the operator's reason (it has no field for it).
func TestTenantStatusMailsNameTheAppInEveryLanguage(t *testing.T) {
	for _, l := range Locales {
		for _, k := range []Kind{KindTenantSuspended, KindTenantFrozen, KindTenantBanned, KindTenantReinstated} {
			subject, text, body := Render(Mail{Kind: k, Locale: l, To: "owner@example.com", Link: "https://app.example/app/support", App: "Shop <b>API</b>\r\nBcc: x@example.com", Account: "Ada Labs"})
			if strings.ContainsAny(subject, "\r\n") || !strings.Contains(subject, "Shop <b>API</b>  Bcc: x@example.com") {
				t.Errorf("%s/%s: subject %q", l, k, subject)
			}
			if !strings.Contains(text, "Ada Labs") || !strings.Contains(text, "https://app.example/app/support") || placeholder.MatchString(text+subject) {
				t.Errorf("%s/%s: text %q", l, k, text)
			}
			if strings.Contains(body, "<b>API</b>") || !strings.Contains(body, "&lt;b&gt;API&lt;/b&gt;") {
				t.Errorf("%s/%s: the app name is not escaped in the html", l, k)
			}
		}
	}
}

func TestRenderEscapesNamesAndKeepsHeadersClean(t *testing.T) {
	subject, text, body := Render(Mail{Kind: KindInvite, Locale: "en", To: "a@example.com", Link: "https://app.example/invite#token=qi_x&y=<z>",
		Account: "Evil <script>alert(1)</script>\r\nBcc: x@example.com", Inviter: "Mallory\nSubject: hi", Role: "admin"})
	if strings.ContainsAny(subject, "\r\n") {
		t.Fatalf("subject has a line break: %q", subject)
	}
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatalf("html not escaped: %s", body)
	}
	if strings.Contains(body, "token=qi_x&y=<z>") {
		t.Fatalf("the link is not escaped in the html: %s", body)
	}
	if !strings.Contains(text, "https://app.example/invite#token=qi_x&y=<z>") {
		t.Fatalf("the text part must carry the link as it is: %s", text)
	}
	if !strings.Contains(text, "administrator") {
		t.Fatalf("the role is spelled out: %s", text)
	}
}

func TestTicketMailNamesTheTicketInTheLanguageAndStaysOnOneLine(t *testing.T) {
	m := Mail{Kind: KindTicketStatus, Locale: "tr", To: "ada@example.com", Link: "https://app.example/app/support?sheet=ticket:1", Ticket: "#1042", Status: "solved",
		Subject: "Fatura {status}\r\nBcc: x@example.com <b>"}
	subject, text, body := Render(m)
	if strings.ContainsAny(subject, "\r\n") || !strings.Contains(subject, "#1042") || !strings.Contains(subject, "çözüldü") {
		t.Fatalf("subject: %q", subject)
	}
	// The subject of the ticket is a person's text: it is not filled in again as a placeholder and it is escaped in the html.
	if !strings.Contains(text, "Fatura {status}  Bcc: x@example.com <b>") || strings.Contains(body, "<b>") {
		t.Fatalf("text %q html %q", text, body)
	}
	if _, _, en := Render(Mail{Kind: KindTicketNew, Locale: "en", Account: "Ada Labs", Ticket: "#7", Priority: "urgent", Subject: "Webhooks"}); !strings.Contains(en, "Ada Labs opened ticket #7 (urgent priority)") {
		t.Fatalf("operator mail: %s", en)
	}
}

func TestArabicIsRightToLeft(t *testing.T) {
	_, _, body := Render(Mail{Kind: KindVerify, Locale: "ar", Link: "https://x.example/v"})
	if !strings.Contains(body, `dir="rtl"`) || !strings.Contains(body, `lang="ar"`) {
		t.Fatalf("arabic mail not marked rtl: %s", body)
	}
	if _, _, en := Render(Mail{Kind: KindVerify, Locale: "xx", Link: "https://x.example/v"}); !strings.Contains(en, `lang="en"`) {
		t.Fatal("an unknown language falls back to English")
	}
}

type relay struct {
	mu   sync.Mutex
	sent []mailx.Message
	err  error
}

func (r *relay) Send(_ context.Context, m mailx.Message) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, m)
	return "id", r.err
}

func TestSMTPSendsInTheBackground(t *testing.T) {
	r := &relay{}
	s := NewSMTP(r, "Payrudder <no-reply@example.com>", slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Send(context.Background(), Mail{Kind: KindReset, To: "ada@example.com", Locale: "de", Link: "https://x.example/r"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := len(r.sent)
		r.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sent) != 1 || r.sent[0].To != "ada@example.com" || r.sent[0].From != "Payrudder <no-reply@example.com>" || !strings.Contains(r.sent[0].Text, "https://x.example/r") {
		t.Fatalf("sent: %+v", r.sent)
	}
}
