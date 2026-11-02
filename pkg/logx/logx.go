// Package logx builds the structured JSON logger every Payrudder process uses.
//
// Records always carry service and version. When the context holds a valid span, trace_id and span_id are added,
// so logs and traces can be joined in Grafana.
package logx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// Options configures New.
type Options struct {
	Service string
	Version string
	Level   string // debug, info, warn, error
	Writer  io.Writer
}

// New returns a JSON logger with service metadata and trace correlation.
func New(opts Options) (*slog.Logger, error) {
	level, err := ParseLevel(opts.Level)
	if err != nil {
		return nil, err
	}
	base := slog.NewJSONHandler(opts.Writer, &slog.HandlerOptions{Level: level})
	h := &traceHandler{next: base}
	return slog.New(h).With(slog.String("service", opts.Service), slog.String("version", opts.Version)), nil
}

// ParseLevel maps a level name to a slog.Level. The empty string means info.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("logx: unknown level %q", s)
	}
}

type traceHandler struct {
	next slog.Handler
}

func (h *traceHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.next.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{next: h.next.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{next: h.next.WithGroup(name)}
}

// Redacted wraps a secret so it never appears in logs. Use it for API keys, DSNs with passwords and tokens.
type Redacted string

// LogValue implements slog.LogValuer.
func (Redacted) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }

// String keeps fmt from printing the secret too.
func (Redacted) String() string { return "[REDACTED]" }

// Scrub blanks the secrets that end up in free text such as an error message: Payrudder tokens (API keys qk_..., sessions qs_..., the links mailed to a person qv_
// qr_ qi_, sign-in challenges qc_; the same family pkg/audit.Mask knows), webhook secrets (whsec_), provider keys (sk_/rk_), bearer tokens, the password of a URL,
// and the value of any key that ends in a secret word (password, secret, token, api key, access key, private key, authorization, credentials, dsn: so client_secret,
// access_token, refresh_token, id_token and secretKey too) written as key=value, key: value, "key":"value" (also JSON quoted again) or in a query string, in any
// case. Redacted covers a value the code knows is secret; Scrub is for text it did not build, so it is deliberately greedy ("invalid token: expired" loses its
// reason). A key that only starts with a secret word (secretary, tokens, token_count, passwordless) is left alone.
func Scrub(s string) string {
	// The expressions are slow next to a substring search and most text has no secret in it: a rule runs only when one of its words is in the text (replacements
	// never add a word to a text that had none, so the lower case copy stays right for the rules after).
	low := strings.ToLower(s)
	for _, r := range scrubbers {
		if containsAny(low, r.words) {
			s = r.re.ReplaceAllString(s, r.with)
		}
	}
	return s
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// secretKey matches the end of a key that names a secret and what follows it up to the value: an optional closing quote (also one escaped by a second quoting), then : or =.
const secretKey = `(?:password|passwd|passphrase|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|secret[_-]?key|signing[_-]?key|authorization|credentials?|dsn)` +
	`(?:[_-]?hash)?(?:\\*["'])?\s*[:=]\s*`

// keyWords are the lower case words a text has to contain for secretKey to match in it.
var keyWords = []string{"password", "passwd", "passphrase", "secret", "token", "key", "authorization", "credential", "dsn"}

var scrubbers = []struct {
	re    *regexp.Regexp
	with  string
	words []string // lower case; the rule can only match in a text that contains one of them
}{
	{regexp.MustCompile(`\b(?:q[ksvric]|whsec|sk|rk)_[A-Za-z0-9_-]{8,}`), "[REDACTED]", []string{"qk_", "qs_", "qv_", "qr_", "qi_", "qc_", "whsec_", "sk_", "rk_"}},
	{regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`), "Bearer [REDACTED]", []string{"bearer"}},
	{regexp.MustCompile(`(?i)(://[^/\s:@]*:)[^@\s/]+@`), "${1}[REDACTED]@", []string{"://"}},
	// One pass for the three shapes of a value: a double-quoted one is taken whole, spaces and all (the quote may be escaped, for JSON that was quoted again), so is a
	// single-quoted one, and an unquoted one ends at a space, a quote, a comma, a semicolon or an ampersand (the next parameter).
	{regexp.MustCompile("(?i)(" + secretKey + `)(?:(\\*")[^"\\]*|(\\*')[^'\\]*|(?:(?:bearer|basic)\s+)?[^\s"'\\,;&]+)`), "${1}${2}${3}[REDACTED]", keyWords},
}
