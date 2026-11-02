package support

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// The notices of the help desk. They are composed inside the transaction that causes them (from the committed-to-be rows) and sent after it commits, so a change
// that is rolled back mails nobody. They go through usermail: Payrudder's own mail to people, in the person's language, in the background. A mail that is lost is
// not retried: the ticket page shows the same news.

// link is the ticket's address in the console: the person's help page, or the operator's inbox.
func (s *Service) link(page string, id uuid.UUID) string {
	if s.cfg.AppURL == "" {
		return ""
	}
	return strings.TrimRight(s.cfg.AppURL, "/") + page + "?sheet=ticket:" + id.String()
}

func number(n *int64) string {
	if n == nil {
		return ""
	}
	return fmt.Sprintf("#%d", *n)
}

// openerMail is the notice to the person who opened the ticket, with the ticket's status as it is now.
func (s *Service) openerMail(ctx context.Context, q *sqlc.Queries, id uuid.UUID, kind usermail.Kind) ([]usermail.Mail, error) {
	if s.cfg.Mail == nil {
		return nil, nil
	}
	r, err := q.SupportMailTarget(ctx, id)
	if err != nil {
		return nil, err
	}
	return []usermail.Mail{{Kind: kind, To: r.OpenerEmail, Locale: r.OpenerLocale, Link: s.link("/app/support", id), Ticket: number(r.Number), Subject: r.Subject, Status: r.Status}}, nil
}

// operatorMails is the notice of a new ticket (KindTicketNew) or of the person's answer on one (KindTicketCustomerReply): one mail to each operator address, in the
// language of the operator's user (English for an address without one).
func (s *Service) operatorMails(ctx context.Context, q *sqlc.Queries, id uuid.UUID, kind usermail.Kind) ([]usermail.Mail, error) {
	if s.cfg.Mail == nil || len(s.cfg.Operators) == 0 {
		return nil, nil
	}
	r, err := q.SupportMailTarget(ctx, id)
	if err != nil {
		return nil, err
	}
	known, err := q.SupportOperatorLocales(ctx, s.cfg.Operators)
	if err != nil {
		return nil, err
	}
	locale := make(map[string]string, len(known))
	for _, k := range known {
		locale[k.Email] = k.Locale
	}
	out := make([]usermail.Mail, len(s.cfg.Operators))
	for i, to := range s.cfg.Operators {
		out[i] = usermail.Mail{Kind: kind, To: to, Locale: locale[to], Link: s.link("/support", id), Account: r.AccountName, Ticket: number(r.Number), Subject: r.Subject, Priority: r.Priority}
	}
	return out, nil
}

// newTicketMails is the notice of a new ticket, bounded: up to MailBurst tickets within domain.SupportMailWindow are mailed one by one, the next one as a single digest
// (KindTicketBurst, linking the inbox), the rest not at all. The count includes the ticket just inserted. Two tickets opened at the same moment can both land on the
// same side of the line, so the bound is approximate by the number of concurrent opens.
func (s *Service) newTicketMails(ctx context.Context, q *sqlc.Queries, id uuid.UUID) ([]usermail.Mail, error) {
	if s.cfg.MailBurst <= 0 {
		return s.operatorMails(ctx, q, id, usermail.KindTicketNew)
	}
	n, err := q.SupportOpenedSince(ctx, s.now().Add(-domain.SupportMailWindow))
	if err != nil {
		return nil, err
	}
	switch {
	case int(n) <= s.cfg.MailBurst:
		return s.operatorMails(ctx, q, id, usermail.KindTicketNew)
	case int(n) == s.cfg.MailBurst+1:
		mails, err := s.operatorMails(ctx, q, id, usermail.KindTicketBurst)
		for i := range mails {
			mails[i].Link, mails[i].Account, mails[i].Ticket, mails[i].Subject, mails[i].Priority = strings.TrimRight(s.cfg.AppURL, "/")+"/support", "", "", "", ""
		}
		return mails, err
	}
	return nil, nil
}

// send hands the mails to the sender, which returns at once.
func (s *Service) send(ctx context.Context, mails []usermail.Mail) {
	for _, m := range mails {
		s.cfg.Mail.Send(ctx, m)
	}
}
