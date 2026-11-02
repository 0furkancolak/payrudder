// Package errtrack reports unexpected errors and panics to a Sentry-compatible server: Sentry itself, or the lighter, self-hostable GlitchTip.
// It is off unless PAYRUDDER_ERROR_TRACKING_URL (a DSN, https://<public key>@host/<project id>) is set, and when it is off nothing is sent and the hooks
// cost one atomic load.
//
// The wire format is the Sentry "envelope" with one "event" item: it is what every Sentry SDK sends, a few dozen lines to produce, and GlitchTip,
// Sentry and Relay accept it, so nothing here depends on a vendor library. (An OTLP log export would need the collector in front of an error server
// to be useful; the envelope goes straight to one.) Everything that leaves the process is passed through logx.Scrub, a report is never sent for the
// same place twice within a minute, and at most 60 leave per minute, so a crash loop cannot flood the server or the network.
//
// Services do not call this package themselves: app.Run installs the tracker, reports a panic of the main goroutine and of every goroutine started
// with Group.Go, grpcx and rabbitx report the panics they recover, and the gateway reports the panics and the internal errors it turns into a 500.
// Other code may call Capture for an error it handles but wants to know about.
package errtrack

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // a fingerprint for grouping, not a security hash
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/0furkancolak/payrudder/pkg/logx"
)

// EnvURL is the environment variable that switches error tracking on.
const EnvURL = "PAYRUDDER_ERROR_TRACKING_URL"

const (
	sdkName       = "payrudder.errtrack"
	maxFrames     = 50
	maxText       = 4096
	dedupeWindow  = time.Minute
	perMinute     = 60
	maxInFlight   = 4
	sendTimeout   = 3 * time.Second
	modulePrefix  = "github.com/0furkancolak/payrudder"
	selfPrefix    = modulePrefix + "/pkg/errtrack."
	maxSeenMemory = 1024
)

// Options configures New.
type Options struct {
	// DSN is the value of PAYRUDDER_ERROR_TRACKING_URL. Empty means off.
	DSN string
	// Service, Version and Environment label every report (the process's service name, version and PAYRUDDER_ENV).
	Service, Version, Environment string
	// HTTPClient sends the reports; the default has a short timeout and follows no redirect.
	HTTPClient *http.Client
	// Log is where a failure to report is told (never the DSN). The default is slog's.
	Log *slog.Logger
	// Now is the clock; tests replace it.
	Now func() time.Time
}

// Tracker sends reports. The zero value and nil are off: every method may be called on them.
type Tracker struct {
	endpoint string
	dsn      string // the DSN without a secret, as the envelope header carries it
	key      string
	service  string
	version  string
	env      string
	host     string
	client   *http.Client
	log      *slog.Logger
	now      func() time.Time

	slots chan struct{}
	wg    sync.WaitGroup

	mu     sync.Mutex
	seen   map[string]time.Time
	window time.Time
	sent   int
}

// New builds a Tracker. An empty DSN gives (nil, nil): tracking is off. A DSN that is not one is an error that does not repeat the DSN.
func New(o Options) (*Tracker, error) {
	if o.DSN == "" {
		return nil, nil
	}
	endpoint, public, key, err := parseDSN(o.DSN)
	if err != nil {
		return nil, err
	}
	t := &Tracker{
		endpoint: endpoint, dsn: public, key: key, service: o.Service, version: o.Version, env: o.Environment,
		client: o.HTTPClient, log: o.Log, now: o.Now, slots: make(chan struct{}, maxInFlight), seen: map[string]time.Time{},
	}
	t.host, _ = os.Hostname()
	if t.client == nil {
		t.client = &http.Client{Timeout: sendTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if t.log == nil {
		t.log = slog.Default()
	}
	if t.now == nil {
		t.now = time.Now
	}
	return t, nil
}

// parseDSN turns https://<key>@host[:port]/[prefix/]<project> into the envelope URL, the DSN without any secret, and the key.
func parseDSN(raw string) (endpoint, public, key string, err error) {
	bad := errors.New("errtrack: " + EnvURL + " is not a DSN of the form https://<public key>@<host>/<project id>")
	u, perr := url.Parse(raw)
	if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User.Username() == "" {
		return "", "", "", bad
	}
	p := strings.Trim(u.Path, "/")
	i := strings.LastIndex(p, "/")
	project, prefix := p[i+1:], p[:i+1] // i is -1 when there is no prefix
	if project == "" {
		return "", "", "", bad
	}
	key = u.User.Username()
	pub := *u
	pub.User = url.User(key)
	return u.Scheme + "://" + u.Host + "/" + prefix + "api/" + project + "/envelope/", pub.String(), key, nil
}

// Enabled reports whether reports are sent.
func (t *Tracker) Enabled() bool { return t != nil && t.endpoint != "" }

// Option adds detail to one report.
type Option func(*event)

// Tag labels a report (a method, a request id). Both parts are scrubbed.
func Tag(k, v string) Option {
	return func(e *event) {
		if e.Tags == nil {
			e.Tags = map[string]string{}
		}
		e.Tags[k] = clip(logx.Scrub(v))
	}
}

// Capture reports an error that was handled but is not expected. Its stack is the caller's.
func (t *Tracker) Capture(ctx context.Context, err error, opts ...Option) {
	if !t.Enabled() || err == nil {
		return
	}
	root := err
	for u := errors.Unwrap(root); u != nil; u = errors.Unwrap(u) {
		root = u
	}
	t.report(ctx, "error", fmt.Sprintf("%T", root), err.Error(), frames(callers(), false), opts)
}

// CapturePanic reports a value that recover returned. Call it from the deferred function that recovered, so the stack of the panic is still there.
// http.ErrAbortHandler is not a failure and is not reported.
func (t *Tracker) CapturePanic(ctx context.Context, p any, opts ...Option) {
	if !t.Enabled() || p == nil {
		return
	}
	typ, msg := fmt.Sprintf("%T", p), fmt.Sprint(p)
	if e, ok := p.(error); ok {
		if errors.Is(e, http.ErrAbortHandler) {
			return
		}
		msg = e.Error()
	}
	t.report(ctx, "fatal", "panic: "+typ, msg, frames(callers(), true), opts)
}

// Flush waits for the reports in flight, at most d. Call it before the process exits.
func (t *Tracker) Flush(d time.Duration) {
	if !t.Enabled() {
		return
	}
	done := make(chan struct{})
	go func() { t.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

func (t *Tracker) report(ctx context.Context, level, typ, msg string, fr []frame, opts []Option) {
	msg = clip(logx.Scrub(msg))
	fp := fingerprint(typ, msg, fr)
	if !t.allow(fp) {
		return
	}
	ev := event{
		EventID: newID(), Timestamp: t.now().UTC().Format("2006-01-02T15:04:05.000Z"), Platform: "go", Level: level, Logger: t.service,
		ServerName: t.host, Release: t.version, Environment: t.env, Fingerprint: []string{fp},
		SDK:       sdk{Name: sdkName, Version: "1"},
		Tags:      map[string]string{"service": t.service},
		Exception: exceptions{Values: []exception{{Type: typ, Value: msg, Stacktrace: &stacktrace{Frames: fr}, Mechanism: &mechanism{Type: "generic", Handled: level == "error"}}}},
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		ev.Tags["trace_id"] = sc.TraceID().String()
	}
	for _, o := range opts {
		o(&ev)
	}
	body := t.envelope(&ev)
	select {
	case t.slots <- struct{}{}:
	default:
		return // four reports are on their way already; this one is dropped rather than queued without bound
	}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		defer func() { <-t.slots }()
		t.send(body)
	}()
}

// allow lets a report through unless the same place reported within the last minute or sixty went out in this minute.
func (t *Tracker) allow(fp string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if last, ok := t.seen[fp]; ok && now.Sub(last) < dedupeWindow {
		return false
	}
	if now.Sub(t.window) >= time.Minute {
		t.window, t.sent = now, 0
	}
	if t.sent >= perMinute {
		return false
	}
	if len(t.seen) >= maxSeenMemory {
		for k, v := range t.seen {
			if now.Sub(v) >= dedupeWindow {
				delete(t.seen, k)
			}
		}
		if len(t.seen) >= maxSeenMemory {
			return false
		}
	}
	t.seen[fp] = now
	t.sent++
	return true
}

func (t *Tracker) envelope(ev *event) []byte {
	payload, _ := json.Marshal(ev)
	head, _ := json.Marshal(map[string]string{"event_id": ev.EventID, "sent_at": t.now().UTC().Format(time.RFC3339), "dsn": t.dsn})
	var b bytes.Buffer
	b.Write(head)
	fmt.Fprintf(&b, "\n{\"type\":\"event\",\"length\":%d}\n", len(payload))
	b.Write(payload)
	b.WriteByte('\n')
	return b.Bytes()
}

func (t *Tracker) send(body []byte) {
	req, err := http.NewRequest(http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	req.Header.Set("X-Sentry-Auth", fmt.Sprintf("Sentry sentry_version=7, sentry_client=%s/1, sentry_key=%s", sdkName, t.key))
	resp, err := t.client.Do(req)
	if err != nil {
		t.log.Warn("error tracking: a report was not sent", "error", logx.Scrub(err.Error()))
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 300 {
		t.log.Warn("error tracking: the server refused a report", "status", resp.StatusCode)
	}
}

// --- the process-wide tracker

var def atomic.Pointer[Tracker]

// Install makes t the tracker that Capture, CapturePanic, Recover and Flush use. app.Run does it once at start; nil turns reporting off.
func Install(t *Tracker) { def.Store(t) }

// Default returns the installed tracker, nil when there is none.
func Default() *Tracker { return def.Load() }

// Capture reports an error with the installed tracker; it does nothing when there is none.
func Capture(ctx context.Context, err error, opts ...Option) { def.Load().Capture(ctx, err, opts...) }

// CapturePanic reports a recovered panic with the installed tracker; it does nothing when there is none.
func CapturePanic(ctx context.Context, p any, opts ...Option) {
	def.Load().CapturePanic(ctx, p, opts...)
}

// Flush waits for the reports of the installed tracker, at most d.
func Flush(d time.Duration) { def.Load().Flush(d) }

// Recover is deferred at the top of a goroutine nothing else guards: it reports the panic, lets the report leave and panics again with the same
// value, so the process still dies with its stack in the log. It does nothing when there is no panic.
//
//	defer errtrack.Recover(ctx)
func Recover(ctx context.Context) {
	if p := recover(); p != nil {
		CapturePanic(ctx, p)
		Flush(2 * time.Second)
		panic(p)
	}
}

// --- the Sentry event

type event struct {
	EventID     string            `json:"event_id"`
	Timestamp   string            `json:"timestamp"`
	Platform    string            `json:"platform"`
	Level       string            `json:"level"`
	Logger      string            `json:"logger,omitempty"`
	ServerName  string            `json:"server_name,omitempty"`
	Release     string            `json:"release,omitempty"`
	Environment string            `json:"environment,omitempty"`
	Fingerprint []string          `json:"fingerprint"`
	SDK         sdk               `json:"sdk"`
	Tags        map[string]string `json:"tags,omitempty"`
	Exception   exceptions        `json:"exception"`
}

type sdk struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type exceptions struct {
	Values []exception `json:"values"`
}

type exception struct {
	Type       string      `json:"type"`
	Value      string      `json:"value"`
	Stacktrace *stacktrace `json:"stacktrace,omitempty"`
	Mechanism  *mechanism  `json:"mechanism,omitempty"`
}

type mechanism struct {
	Type    string `json:"type"`
	Handled bool   `json:"handled"`
}

type stacktrace struct {
	Frames []frame `json:"frames"`
}

type frame struct {
	Filename string `json:"filename"`
	Function string `json:"function"`
	Module   string `json:"module,omitempty"`
	Lineno   int    `json:"lineno"`
	InApp    bool   `json:"in_app"`
}

// callers returns the stack of the caller of Capture or CapturePanic, innermost first.
func callers() []runtime.Frame {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(2, pcs)
	it := runtime.CallersFrames(pcs[:n])
	var out []runtime.Frame
	for {
		f, more := it.Next()
		out = append(out, f)
		if !more {
			break
		}
	}
	return out
}

// frames makes the Sentry frames (outermost first) from a stack. The frames of this package are dropped; for a panic so are the frames up to and
// including runtime.gopanic, and the runtime's own frames that follow it (sigpanic, panicmem), so the top frame is the line that panicked.
func frames(stack []runtime.Frame, panicked bool) []frame {
	for len(stack) > 0 && strings.HasPrefix(stack[0].Function, selfPrefix) {
		stack = stack[1:]
	}
	if panicked {
		for i, f := range stack {
			if f.Function == "runtime.gopanic" {
				stack = stack[i+1:]
				break
			}
		}
		for len(stack) > 0 && strings.HasPrefix(stack[0].Function, "runtime.") {
			stack = stack[1:]
		}
	}
	if len(stack) > maxFrames {
		stack = stack[:maxFrames]
	}
	out := make([]frame, 0, len(stack))
	for i := len(stack) - 1; i >= 0; i-- {
		f := stack[i]
		fn := f.Function
		mod := ""
		if j := strings.LastIndex(fn, "/"); j >= 0 {
			if k := strings.Index(fn[j:], "."); k >= 0 {
				mod, fn = fn[:j+k], fn[j+k+1:]
			}
		} else if k := strings.Index(fn, "."); k >= 0 {
			mod, fn = fn[:k], fn[k+1:]
		}
		out = append(out, frame{
			Filename: path.Join(path.Base(path.Dir(f.File)), path.Base(f.File)), Function: fn, Module: mod, Lineno: f.Line,
			InApp: strings.HasPrefix(f.Function, modulePrefix),
		})
	}
	return out
}

var variable = regexp.MustCompile(`[0-9]+|[0-9a-f]{8,}`)

// fingerprint groups reports: the kind of error, its text with numbers and ids blanked, and the innermost frame (the callers above it differ without the problem differing). The server is told the same
// fingerprint, so it groups what this process would not report twice.
func fingerprint(typ, msg string, fr []frame) string {
	h := sha1.New() //nolint:gosec // see the import
	h.Write([]byte(typ))
	h.Write([]byte{0})
	h.Write([]byte(variable.ReplaceAllString(msg, "#")))
	if n := len(fr); n > 0 {
		fmt.Fprintf(h, "\x00%s:%s:%d", fr[n-1].Module, fr[n-1].Function, fr[n-1].Lineno)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func clip(s string) string {
	if len(s) > maxText {
		return s[:maxText] + "..."
	}
	return s
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
