// Package mailx sends e-mail over SMTP (Mailpit locally). The notifier (tenant mail) and plans-svc (mail to people) share it.
package mailx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime/quotedprintable"
	"net"
	stdmail "net/mail"
	"net/smtp"
	"strings"
	"time"
)

// Message is one email.
type Message struct {
	From    string
	To      string
	Subject string
	HTML    string
	Text    string
	// ID is the Message-ID local part (the dedup key makes it traceable).
	ID string
}

// NotSentError means the message was certainly not accepted (the failure came before the end of DATA), so the caller may
// send it again without risking a duplicate.
type NotSentError struct{ Err error }

func (e *NotSentError) Error() string { return "mail: not sent: " + e.Err.Error() }
func (e *NotSentError) Unwrap() error { return e.Err }

// Sender sends messages and returns the Message-ID it used.
type Sender interface {
	Send(ctx context.Context, m Message) (messageID string, err error)
}

// SMTP sends through an SMTP relay without authentication (Mailpit).
type SMTP struct {
	Addr    string
	Timeout time.Duration
}

// Send implements Sender.
func (s SMTP) Send(ctx context.Context, m Message) (string, error) {
	if strings.ContainsAny(m.To+m.From+m.Subject, "\r\n") {
		return "", fmt.Errorf("mail: header injection attempt in %q", m.To)
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	id := messageID(m)
	boundary := "qb-" + id[:16]
	var body bytes.Buffer
	fmt.Fprintf(&body, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <%s@payrudder.local>\r\nDate: %s\r\nMIME-Version: 1.0\r\n", m.From, m.To, encodeHeader(m.Subject), id,
		time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&body, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	for _, part := range []struct{ typ, text string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		fmt.Fprintf(&body, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, part.typ)
		w := quotedprintable.NewWriter(&body)
		_, _ = w.Write([]byte(part.text))
		_ = w.Close()
		body.WriteString("\r\n")
	}
	fmt.Fprintf(&body, "--%s--\r\n", boundary)

	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return "", &NotSentError{fmt.Errorf("dial %s: %w", s.Addr, err)}
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	host, _, _ := net.SplitHostPort(s.Addr)
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return "", &NotSentError{fmt.Errorf("smtp client: %w", err)}
	}
	defer c.Close()
	from, err := stdmail.ParseAddress(m.From)
	if err != nil {
		return "", &NotSentError{fmt.Errorf("invalid From %q: %w", m.From, err)}
	}
	to, err := stdmail.ParseAddress(m.To)
	if err != nil {
		return "", &NotSentError{fmt.Errorf("invalid recipient %q: %w", m.To, err)}
	}
	if err := c.Mail(from.Address); err != nil {
		return "", &NotSentError{fmt.Errorf("MAIL FROM: %w", err)}
	}
	if err := c.Rcpt(to.Address); err != nil {
		return "", &NotSentError{fmt.Errorf("RCPT TO: %w", err)}
	}
	w, err := c.Data()
	if err != nil {
		return "", &NotSentError{fmt.Errorf("DATA: %w", err)}
	}
	if _, err := w.Write(body.Bytes()); err != nil {
		return "", fmt.Errorf("mail: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("mail: end of data: %w", err)
	}
	_ = c.Quit()
	return id + "@payrudder.local", nil
}

func messageID(m Message) string {
	if m.ID != "" {
		sum := sha256.Sum256([]byte(m.ID))
		return hex.EncodeToString(sum[:16]) + "." + fmt.Sprint(time.Now().UnixNano())
	}
	sum := sha256.Sum256([]byte(m.To + m.Subject + fmt.Sprint(time.Now().UnixNano())))
	return hex.EncodeToString(sum[:16])
}

func encodeHeader(s string) string {
	for _, r := range s {
		if r > 0x7e || r < 0x20 {
			return "=?UTF-8?q?" + strings.ReplaceAll(strings.ReplaceAll(quotedPrintable(s), " ", "_"), "?", "=3F") + "?="
		}
	}
	return s
}

func quotedPrintable(s string) string {
	var b bytes.Buffer
	w := quotedprintable.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return b.String()
}
