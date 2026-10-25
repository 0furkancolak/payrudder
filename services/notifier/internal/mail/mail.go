// Package mail is the notifier's name for the shared SMTP sender (pkg/mailx).
package mail

import "github.com/0furkancolak/payrudder/pkg/mailx"

type (
	// Message is one email.
	Message = mailx.Message
	// NotSentError means the message was certainly not accepted.
	NotSentError = mailx.NotSentError
	// Sender sends messages.
	Sender = mailx.Sender
	// SMTP sends through an SMTP relay without authentication.
	SMTP = mailx.SMTP
)
