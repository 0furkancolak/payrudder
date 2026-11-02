package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const testKey = "qk_test_ab12cd34_s3cr3tS3cr3tS3cr3tS3cr3tS3cr3tS3"

// syncBuf is an output buffer the receiver goroutines and the test can both touch.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// request is what the fake gateway saw.
type request struct {
	method, path, query, auth, ua string
	body                          []byte
}

// gateway is a fake Payrudder gateway: handlers by "METHOD /path", and a record of every request.
type gateway struct {
	*httptest.Server
	mu   sync.Mutex
	seen []request
	mux  *http.ServeMux
}

func newGateway(t *testing.T) *gateway {
	t.Helper()
	g := &gateway{mux: http.NewServeMux()}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		g.seen = append(g.seen, request{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("User-Agent"), b})
		g.mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(b))
		g.mux.ServeHTTP(w, r)
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *gateway) handle(pattern string, h http.HandlerFunc) { g.mux.HandleFunc(pattern, h) }

// json answers every call to pattern with v and the status.
func (g *gateway) json(pattern string, status int, v any) {
	g.handle(pattern, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, status, v) })
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, status int, code, key, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "key": key, "message": msg}})
}

func (g *gateway) requests(method, path string) []request {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []request
	for _, r := range g.seen {
		if r.method == method && r.path == path {
			out = append(out, r)
		}
	}
	return out
}

func (g *gateway) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.seen)
}

// env is one run of the command in a fresh config directory against a gateway.
type env struct {
	t      *testing.T
	gw     *gateway
	dir    string
	vars   map[string]string
	out    *syncBuf
	errOut *syncBuf
}

func newEnv(t *testing.T) *env {
	t.Helper()
	gw := newGateway(t)
	dir := filepath.Join(t.TempDir(), "cfg")
	return &env{t: t, gw: gw, dir: dir, vars: map[string]string{"PAYRUDDER_CONFIG_DIR": dir, "PAYRUDDER_BASE_URL": gw.URL, "PAYRUDDER_API_KEY": testKey}}
}

func (e *env) getenv(k string) string { return e.vars[k] }

func (e *env) runCtx(ctx context.Context, stdin string, args ...string) int {
	e.out, e.errOut = &syncBuf{}, &syncBuf{}
	return run(ctx, args, strings.NewReader(stdin), e.out, e.errOut, e.getenv)
}

func (e *env) run(stdin string, args ...string) int {
	e.t.Helper()
	return e.runCtx(context.Background(), stdin, args...)
}

func (e *env) expect(code int, stdin string, args ...string) {
	e.t.Helper()
	if got := e.run(stdin, args...); got != code {
		e.t.Fatalf("payrudder %s: exit %d, want %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), got, code, e.out, e.errOut)
	}
}

func contains(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%s lacks %q:\n%s", what, w, got)
		}
	}
}

func lacks(t *testing.T, what, got string, nots ...string) {
	t.Helper()
	for _, n := range nots {
		if strings.Contains(got, n) {
			t.Errorf("%s contains %q:\n%s", what, n, got)
		}
	}
}

func TestLoginStoresTheKeyOnlyAfterTheAPIAcceptedIt(t *testing.T) {
	e := newEnv(t)
	delete(e.vars, "PAYRUDDER_API_KEY")
	e.gw.handle("GET /v1/meters", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			apiError(w, 401, "UNAUTHENTICATED", "identity.api_key.invalid", "bad key")
			return
		}
		writeJSON(w, 200, map[string]any{"meters": []any{}})
	})

	e.expect(0, testKey+"\n", "login", "--api-key-stdin")
	contains(t, "stdout", e.out.String(), "sandbox key qk_test_ab12cd34_…", e.vars["PAYRUDDER_BASE_URL"], filepath.Join(e.dir, "config.json"))
	lacks(t, "stdout", e.out.String(), "s3cr3t")
	lacks(t, "stderr", e.errOut.String(), "s3cr3t")

	raw, err := os.ReadFile(filepath.Join(e.dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg storedConfig
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.APIKey != testKey || cfg.BaseURL != e.gw.URL {
		t.Fatalf("stored %s (%v)", raw, err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(filepath.Join(e.dir, "config.json"))
		dir, _ := os.Stat(e.dir)
		if st.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
			t.Errorf("config file mode %v, directory mode %v: want 0600 in 0700", st.Mode().Perm(), dir.Mode().Perm())
		}
	}
	if left, _ := filepath.Glob(filepath.Join(e.dir, "*.tmp")); len(left) != 0 {
		t.Errorf("temporary file left behind: %v", left)
	}
	if reqs := e.gw.requests("GET", "/v1/meters"); len(reqs) != 1 || !strings.HasPrefix(reqs[0].ua, "payrudder-cli/") {
		t.Errorf("the key should be checked once, as payrudder-cli: %+v", reqs)
	}
}

func TestLoginAsksForTheKeyWhenNothingGivesIt(t *testing.T) {
	e := newEnv(t)
	delete(e.vars, "PAYRUDDER_API_KEY")
	e.gw.json("GET /v1/meters", 200, map[string]any{"meters": []any{}})
	e.expect(0, "  "+testKey+"  \n", "login")
	contains(t, "prompt", e.errOut.String(), "API key (qk_test_... or qk_live_...):")
	if _, err := os.Stat(filepath.Join(e.dir, "config.json")); err != nil {
		t.Error("the key was not stored")
	}
}

func TestLoginKeepsNothingWhenTheKeyIsRefusedOrMalformed(t *testing.T) {
	e := newEnv(t)
	delete(e.vars, "PAYRUDDER_API_KEY")
	e.gw.handle("GET /v1/meters", func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, 401, "UNAUTHENTICATED", "identity.api_key.invalid", "the key is revoked")
	})
	e.expect(1, testKey+"\n", "login", "--api-key-stdin")
	contains(t, "stderr", e.errOut.String(), "refused this key", "nothing was stored")
	if _, err := os.Stat(filepath.Join(e.dir, "config.json")); err == nil {
		t.Error("a refused key was stored")
	}

	before := e.gw.count()
	e.expect(1, "hunter2\n", "login", "--api-key-stdin")
	contains(t, "stderr", e.errOut.String(), "not a Payrudder API key")
	lacks(t, "stderr", e.errOut.String(), "hunter2")
	if e.gw.count() != before {
		t.Error("a key that is not shaped like one was sent to the API")
	}
}

func TestLoginAcceptsAKeyThatMayNotReadMeters(t *testing.T) {
	e := newEnv(t)
	delete(e.vars, "PAYRUDDER_API_KEY")
	e.gw.handle("GET /v1/meters", func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, 403, "PERMISSION_DENIED", "identity.api_key.forbidden", "missing catalog:read")
	})
	e.expect(0, testKey+"\n", "login", "--api-key-stdin")
	contains(t, "stdout", e.out.String(), "cannot read meters; it was accepted")
}

func TestLoginNoVerifyDoesNotCallTheAPI(t *testing.T) {
	e := newEnv(t)
	e.expect(0, testKey+"\n", "login", "--api-key-stdin", "--no-verify", "--base-url", "https://api.example.com")
	if e.gw.count() != 0 {
		t.Error("--no-verify called the API")
	}
	raw, _ := os.ReadFile(filepath.Join(e.dir, "config.json"))
	contains(t, "config", string(raw), "https://api.example.com")
}

func TestStoredLoginIsUsedAndTheEnvironmentOverridesIt(t *testing.T) {
	e := newEnv(t)
	other := "qk_live_zz99yy88_" + strings.Repeat("a", 40)
	e.gw.json("GET /v1/customers", 200, map[string]any{"customers": []any{}, "next_page_token": ""})
	delete(e.vars, "PAYRUDDER_API_KEY")
	delete(e.vars, "PAYRUDDER_BASE_URL")

	e.expect(1, "", "customers", "list") // nothing stored, nothing in the environment
	contains(t, "stderr", e.errOut.String(), "not signed in", "payrudder login")

	e.runLogin(testKey)
	delete(e.vars, "PAYRUDDER_API_KEY")
	e.expect(0, "", "customers", "list")
	e.vars["PAYRUDDER_API_KEY"] = other
	e.expect(0, "", "customers", "list")
	got := e.gw.requests("GET", "/v1/customers")
	if len(got) != 2 || got[0].auth != "Bearer "+testKey || got[1].auth != "Bearer "+other {
		t.Errorf("the stored key serves until $PAYRUDDER_API_KEY is set, which wins: %+v", got)
	}

	e.expect(0, "", "logout")
	contains(t, "stdout", e.out.String(), "Removed")
	delete(e.vars, "PAYRUDDER_API_KEY")
	e.expect(1, "", "customers", "list")
	e.expect(0, "", "logout")
	contains(t, "stdout", e.out.String(), "No stored login")
}

// runLogin stores key through the command, with the gateway of the environment, and leaves the environment as the user's shell would have it.
func (e *env) runLogin(key string) {
	e.gw.json("GET /v1/meters", 200, map[string]any{"meters": []any{}})
	e.vars["PAYRUDDER_BASE_URL"] = e.gw.URL
	e.expect(0, key+"\n", "login", "--api-key-stdin")
	delete(e.vars, "PAYRUDDER_BASE_URL")
}

func TestAWorldReadableConfigIsFlagged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes mean something else on Windows")
	}
	e := newEnv(t)
	delete(e.vars, "PAYRUDDER_API_KEY")
	if err := os.MkdirAll(e.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(e.dir, "config.json")
	if err := os.WriteFile(p, []byte(`{"api_key":"`+testKey+`"}`), 0o644); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	e.gw.json("GET /v1/customers", 200, map[string]any{"customers": []any{}, "next_page_token": ""})
	e.expect(0, "", "customers", "list")
	contains(t, "stderr", e.errOut.String(), "can be read by other users", "chmod 600")
}

func TestUsageErrorsExitWithTwo(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{
		nil, {"nope"}, {"events"}, {"events", "nope"}, {"customers", "list", "--limit", "0"}, {"customers", "list", "extra"}, {"customers", "list", "--nope"},
		{"events", "send"}, {"events", "tail"},
	} {
		if code := e.run("", args...); code != 2 {
			t.Errorf("payrudder %v: exit %d, want 2 (%s)", args, code, e.errOut)
		}
	}
	if e.gw.count() != 0 {
		t.Error("a malformed command line reached the API")
	}
	e.expect(0, "", "help")
	contains(t, "help", e.out.String(), "webhooks listen", "events tail")
	e.expect(0, "", "version")
	contains(t, "version", e.out.String(), "payrudder 0.0.0")
	e.expect(0, "", "customers", "list", "-h")
}

func TestAnAPIErrorShowsItsKeyAndRequestAndAnAuthErrorSaysWhatToDo(t *testing.T) {
	e := newEnv(t)
	e.gw.handle("GET /v1/customers", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-Id", "req-123")
		apiError(w, 401, "UNAUTHENTICATED", "identity.api_key.invalid", "the key is revoked")
	})
	e.expect(1, "", "customers", "list")
	contains(t, "stderr", e.errOut.String(), "identity.api_key.invalid", "req-123", "payrudder login")
	lacks(t, "stderr", e.errOut.String(), "s3cr3t")
}

func TestContextCancelStopsAWatcher(t *testing.T) {
	e := newEnv(t)
	e.gw.json("GET /v1/webhook-deliveries", 200, map[string]any{"deliveries": []any{}, "next_page_token": ""})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- e.runCtx(ctx, "", "webhooks", "deliveries", "--watch", "--interval", "10ms") }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d after Ctrl-C", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher ignored the cancelled context")
	}
}

// The temporary file of a login is a fresh name made with O_EXCL: a link that somebody planted where the old code wrote (config.json.tmp) is neither followed nor
// replaced, so it cannot make the key land in, or overwrite, another file.
func TestLoginDoesNotFollowALinkPlantedAtTheTemporaryName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need privileges on Windows")
	}
	e := newEnv(t)
	delete(e.vars, "PAYRUDDER_API_KEY")
	if err := os.MkdirAll(e.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.json.tmp", "config.json"} {
		if err := os.Symlink(victim, filepath.Join(e.dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	e.gw.json("GET /v1/meters", 200, map[string]any{"meters": []any{}})
	e.expect(0, testKey+"\n", "login", "--api-key-stdin")
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Fatalf("the file a link pointed at now holds %q", b)
	}
	st, err := os.Lstat(filepath.Join(e.dir, "config.json"))
	if err != nil || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm() != 0o600 {
		t.Fatalf("config.json after the login: %v, %v", st, err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.dir, "config.json")); !strings.Contains(string(b), testKey) {
		t.Errorf("the key was not stored: %s", b)
	}
	if st, err := os.Lstat(filepath.Join(e.dir, "config.json.tmp")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the planted link was touched: %v %v", st, err)
	}
	if left, _ := filepath.Glob(filepath.Join(e.dir, ".config-*")); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

// A live key goes over https, or over http to this machine; a sandbox key may go over http. Checked at login (stored nothing, asked nothing) and on every command that
// builds a client, from the environment or from the stored login.
func TestALiveKeyIsNotSentOverPlainHTTPToAnotherHost(t *testing.T) {
	live := "qk_live_zz99yy88_" + strings.Repeat("a", 40)
	for base, ok := range map[string]bool{
		"https://api.example.com": true, "https://api.example.com:8443/base": true, "HTTPS://API.EXAMPLE.COM": true,
		"http://localhost:42800": true, "http://LOCALHOST": true, "http://localhost.": true, "http://app.localhost:3000": true, "http://127.0.0.1:8000": true, "http://127.9.9.9": true,
		"http://[::1]:8000": true, "http://[::ffff:127.0.0.1]:8000": true, "http://user@localhost:1": true,
		"http://api.example.com": false, "HTTP://API.EXAMPLE.COM": false, "http://api.example.com:8000": false, "http://10.0.0.5:8000": false, "http://192.168.1.2": false,
		"http://127.0.0.1.example.com": false, "http://localhost.example.com": false, "http://notlocalhost": false, "http://0.0.0.0:8000": false, "http://[2606:4700::1111]": false,
		"http://localhost@evil.example/": false,
	} {
		e := newEnv(t)
		delete(e.vars, "PAYRUDDER_BASE_URL")
		// at login
		if got := e.run(live+"\n", "login", "--api-key-stdin", "--no-verify", "--base-url", base); (got == 0) != ok {
			t.Errorf("login with a live key and %s: exit %d (stderr %q), want ok=%v", base, got, e.errOut, ok)
		}
		if _, err := os.Stat(filepath.Join(e.dir, "config.json")); (err == nil) != ok {
			t.Errorf("login with a live key and %s: stored=%v, want %v", base, err == nil, ok)
		}
		if !ok {
			contains(t, "stderr", e.errOut.String(), "refusing to send a live API key over plain http", "https://")
			lacks(t, "stderr", e.errOut.String(), live)
		}
		// from the environment
		e.vars["PAYRUDDER_API_KEY"], e.vars["PAYRUDDER_BASE_URL"] = live, base
		if ok {
			continue // the address does not answer; only the refusals are looked at here
		}
		e.expect(1, "", "customers", "list")
		contains(t, "stderr", e.errOut.String(), "refusing to send a live API key over plain http")
	}

	// A stored login: the refusal comes at the command, not only at login (the file may have been written by hand or by an older version).
	e := newEnv(t)
	delete(e.vars, "PAYRUDDER_API_KEY")
	delete(e.vars, "PAYRUDDER_BASE_URL")
	if err := os.MkdirAll(e.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dir, "config.json"), []byte(`{"api_key":"`+live+`","base_url":"http://api.example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	e.expect(1, "", "customers", "list")
	contains(t, "stderr", e.errOut.String(), "refusing to send a live API key over plain http to api.example.com")
	if e.gw.count() != 0 {
		t.Error("something was sent")
	}

	// A sandbox key may go over http, and a live key to the local gateway really is sent.
	e = newEnv(t)
	e.gw.json("GET /v1/customers", 200, map[string]any{"customers": []any{}, "next_page_token": ""})
	e.vars["PAYRUDDER_BASE_URL"] = "http://api.example.com:1"
	e.expect(0, testKey+"\n", "login", "--api-key-stdin", "--no-verify")
	e.vars["PAYRUDDER_API_KEY"], e.vars["PAYRUDDER_BASE_URL"] = live, e.gw.URL // httptest listens on 127.0.0.1
	e.expect(0, "", "customers", "list")
	if got := e.gw.requests("GET", "/v1/customers"); len(got) != 1 || got[0].auth != "Bearer "+live {
		t.Errorf("a live key to the loopback gateway: %+v", got)
	}
}
