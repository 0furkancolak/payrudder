package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// The default dev seed. It exists only for a developer's machine: it signs up people with PUBLIC passwords and fills their
// apps with sample data so that every screen of the product app has something to show. It talks to the gateway like any
// client (the product API with a session, the operator API only where the product API has no route: credit notes), so it
// can run against any stack that runs the merged code, and it is idempotent: every object is looked up by a stable name
// or key first, so running it again changes nothing.
//
// It refuses to run against anything that does not look like a local stack (see checkDevTarget), and no deployment
// profile (Helm, Railway, the compose app profile) runs it.

// Public, dev-only credentials. They are documented on purpose (docs/10-local-dev.md); never use them anywhere real.
const (
	devPassword       = "password123"
	devDefaultToken   = "local-operator-token-change-me-0123456789"
	devSeedUserOne    = "user1@example.com"
	devSeedUserTwo    = "user2@example.com"
	devSeedAccount    = "Acme Labs"
	devSeedAccount2   = "Beta Studio"
	defaultGatewayURL = "http://localhost:42800"
	defaultMailpitURL = "http://localhost:42825"
)

// checkDevTarget refuses a gateway that does not look like a local development stack. The only way past it is the explicit
// --i-know-this-is-dev flag, which is for a dev stack on another host name (a VM, a container network); the environment
// name of the process is checked in any case.
func checkDevTarget(gateway string, force bool, env string) error {
	switch strings.ToLower(env) {
	case "prod", "production", "live", "staging":
		return fmt.Errorf("PAYRUDDER_ENV=%s: the dev seed never runs against %s", env, env)
	}
	u, err := url.Parse(gateway)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("gateway %q is not a URL", gateway)
	}
	if force {
		return nil
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return nil
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("gateway host %q is not local: the dev seed signs up people with a public password and runs only against localhost, 127.0.0.1, [::1] or *.localhost (pass --i-know-this-is-dev if this really is a dev stack)", h)
}

// devSeedOptions are the settings of one run.
type devSeedOptions struct {
	Gateway  string
	Token    string
	Mailpit  string
	Force    bool
	Env      string
	Timeout  time.Duration // how long to wait for the stack and for the invoices
	Out      io.Writer
	HTTP     *http.Client
	Now      func() time.Time
	PollWait time.Duration
}

func devSeedCmd(args []string) error {
	fs := flag.NewFlagSet("dev-seed", flag.ContinueOnError)
	gateway := fs.String("gateway", envOr("PAYRUDDER_GATEWAY_URL", defaultGatewayURL), "gateway base URL (must be a local stack)")
	token := fs.String("operator-token", envOr("PAYRUDDER_OPERATOR_TOKEN", devDefaultToken), "operator token (PAYRUDDER_OPERATOR_TOKEN; the dev default)")
	mailpit := fs.String("mailpit", envOr("MAILPIT_URL", defaultMailpitURL), "Mailpit URL, where the verification mails of the seeded people are read")
	force := fs.Bool("i-know-this-is-dev", false, "allow a gateway whose host is not localhost (a dev stack on another name)")
	wait := fs.Duration("wait", 5*time.Minute, "how long to wait for the gateway and for the invoices to settle")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runDevSeed(devSeedOptions{Gateway: *gateway, Token: *token, Mailpit: *mailpit, Force: *force, Env: getenv("PAYRUDDER_ENV"), Timeout: *wait})
}

func runDevSeed(o devSeedOptions) error {
	if err := checkDevTarget(o.Gateway, o.Force, o.Env); err != nil {
		return err
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.PollWait == 0 {
		o.PollWait = 2 * time.Second
	}
	s := &devSeeder{o: o, base: strings.TrimRight(o.Gateway, "/")}
	s.logf("dev seed against %s (public dev credentials, local stacks only)", s.base)
	if err := s.waitReady(); err != nil {
		return err
	}
	for _, p := range devPeople {
		if err := s.seedPerson(p); err != nil {
			return fmt.Errorf("%s: %w", p.Email, err)
		}
	}
	s.logf("dev seed done: sign in at the console (/app) as %s / %s", devSeedUserOne, devPassword)
	return nil
}

// ---- HTTP ----------------------------------------------------------------------------------------------------------

type devSeeder struct {
	o    devSeedOptions
	base string
}

func (s *devSeeder) logf(format string, a ...any) {
	fmt.Fprintf(s.o.Out, "dev-seed: "+format+"\n", a...)
}

// apiError carries the HTTP status so callers can tell "already there" from a failure.
type apiError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, strings.TrimSpace(e.Body))
}

func statusOf(err error) int {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

func (s *devSeeder) do(method, rawBase, path string, hdr map[string]string, in, out any) error {
	var payload []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		payload = b
	}
	// A free-tier tenant may write five times a second: a 429 is waited out and tried again, never given up on early.
	for attempt := 1; ; attempt++ {
		err := s.once(method, rawBase, path, hdr, payload, out)
		if statusOf(err) != http.StatusTooManyRequests || attempt >= 12 {
			return err
		}
		time.Sleep(time.Duration(attempt) * s.o.PollWait / 2)
	}
}

func (s *devSeeder) once(method, rawBase, path string, hdr map[string]string, payload []byte, out any) error {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, rawBase+path, body) //nolint:gosec // the URL is the developer's own --gateway, checked by checkDevTarget
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := s.o.HTTP.Do(req) //nolint:gosec // see above
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return &apiError{Method: method, Path: path, Status: resp.StatusCode, Body: string(raw)}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// waitReady waits until the gateway answers an operator read that needs plans-svc.
func (s *devSeeder) waitReady() error {
	deadline := s.o.Now().Add(s.o.Timeout)
	var last error
	for {
		last = s.do("GET", s.base, "/admin/v1/tenants?page_size=1", map[string]string{"Authorization": "Bearer " + s.o.Token}, nil, nil)
		if last == nil {
			return nil
		}
		if st := statusOf(last); st == 401 || st == 403 {
			return fmt.Errorf("the gateway refused the operator token (%w); set PAYRUDDER_OPERATOR_TOKEN or --operator-token", last)
		}
		if s.o.Now().After(deadline) {
			return fmt.Errorf("the stack did not become ready within %s: %w", s.o.Timeout, last)
		}
		time.Sleep(s.o.PollWait)
	}
}

// operator calls an operator route.
func (s *devSeeder) operator(method, path string, in, out any) error {
	return s.do(method, s.base, path, map[string]string{"Authorization": "Bearer " + s.o.Token}, in, out)
}

// ---- people, accounts, apps ------------------------------------------------------------------------------------

type devApp struct {
	Name, Currency, Timezone string
	Profile                  string // which data set fills it: "store", "game", "mini"
}

type devPerson struct {
	Email, Account string
	Apps           []devApp
}

var devPeople = []devPerson{
	{Email: devSeedUserOne, Account: devSeedAccount, Apps: []devApp{
		{Name: "Storefront", Currency: "TRY", Timezone: "Europe/Istanbul", Profile: "store"},
		{Name: "Mobile Game", Currency: "USD", Timezone: "UTC", Profile: "game"},
	}},
	{Email: devSeedUserTwo, Account: devSeedAccount2, Apps: []devApp{
		{Name: "Notes Sync", Currency: "EUR", Timezone: "Europe/Berlin", Profile: "mini"},
	}},
}

type sessionOpened struct {
	Token string `json:"token"`
	User  struct {
		EmailVerified bool `json:"email_verified"`
	} `json:"user"`
	Memberships []struct {
		AccountID   string `json:"account_id"`
		AccountName string `json:"account_name"`
		Role        string `json:"role"`
	} `json:"memberships"`
}

func (s *devSeeder) signIn(email string) (*sessionOpened, error) {
	var out sessionOpened
	err := s.do("POST", s.base, "/auth/signin", nil, map[string]string{"email": email, "password": devPassword}, &out)
	return &out, err
}

// ensureUser signs the person in, creating them first (through the real sign-up path) when the address is unknown. A
// person who exists with another password is never touched: the run stops with a clear message.
func (s *devSeeder) ensureUser(p devPerson) (*sessionOpened, error) {
	out, err := s.signIn(p.Email)
	if err != nil {
		if statusOf(err) != 401 {
			return nil, err
		}
		s.logf("%s: signing up", p.Email)
		if err := s.do("POST", s.base, "/auth/signup", nil, map[string]string{
			"email": p.Email, "password": devPassword, "display_name": strings.SplitN(p.Email, "@", 2)[0], "account_name": p.Account, "locale": "en",
		}, nil); err != nil {
			return nil, err
		}
		if out, err = s.signIn(p.Email); err != nil {
			if statusOf(err) == 401 {
				return nil, fmt.Errorf("%s exists with another password (or is locked): the seed never resets a password; sign in by hand or wipe the dev database", p.Email)
			}
			return nil, err
		}
	}
	if !out.User.EmailVerified {
		if err := s.verifyEmail(p.Email, out.Token); err != nil {
			s.logf("%s: warning: the address is not verified (%v); the sandbox works, a live API key needs verification", p.Email, err)
		} else if out, err = s.signIn(p.Email); err != nil {
			return nil, err
		}
	}
	return out, nil
}

var verifyTokenRE = regexp.MustCompile(`qv_[A-Za-z0-9_-]{20,}`)

// verifyEmail reads the verification mail of the address from Mailpit (the dev mail catcher) and spends its link, the
// same way a person would. Production auth is untouched: the seed only reads the mail like the developer could.
func (s *devSeeder) verifyEmail(email, sessionToken string) error {
	find := func() (string, error) {
		var list struct {
			Messages []struct{ ID string } `json:"messages"`
		}
		q := url.Values{"query": {"to:" + email}, "limit": {"10"}}
		if err := s.do("GET", strings.TrimRight(s.o.Mailpit, "/"), "/api/v1/search?"+q.Encode(), nil, nil, &list); err != nil {
			return "", err
		}
		for _, m := range list.Messages { // newest first
			var msg struct{ Text, HTML string }
			if err := s.do("GET", strings.TrimRight(s.o.Mailpit, "/"), "/api/v1/message/"+m.ID, nil, nil, &msg); err != nil {
				continue
			}
			if t := verifyTokenRE.FindString(msg.Text + " " + msg.HTML); t != "" {
				return t, nil
			}
		}
		return "", errors.New("no verification mail in Mailpit")
	}
	tok, err := find()
	if err != nil {
		// Mail goes out in the background; one resend (throttled to a minute by the server) and one more look.
		time.Sleep(s.o.PollWait)
		if tok, err = find(); err != nil {
			_ = s.do("POST", s.base, "/auth/resend-verification", map[string]string{"Authorization": "Bearer " + sessionToken}, nil, nil)
			time.Sleep(2 * s.o.PollWait)
			if tok, err = find(); err != nil {
				return err
			}
		}
	}
	if err := s.do("POST", s.base, "/auth/verify", nil, map[string]string{"token": tok}, nil); err != nil {
		return err
	}
	s.logf("%s: address verified from the Mailpit mail", email)
	return nil
}

type appJSON struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Tenants []struct {
		ID          string `json:"id"`
		Environment string `json:"environment"`
	} `json:"tenants"`
}

func (a appJSON) sandboxTenant() string {
	for _, t := range a.Tenants {
		if strings.Contains(strings.ToUpper(t.Environment), "SANDBOX") {
			return t.ID
		}
	}
	return ""
}

func (s *devSeeder) seedPerson(p devPerson) error {
	so, err := s.ensureUser(p)
	if err != nil {
		return err
	}
	if len(so.Memberships) == 0 {
		return errors.New("the user has no account")
	}
	acct := so.Memberships[0]
	for _, m := range so.Memberships { // reuse the account of that name when there is a choice
		if m.AccountName == p.Account {
			acct = m
		}
	}
	hdr := map[string]string{"Authorization": "Bearer " + so.Token}
	if err := s.ensureDevPlan(acct.AccountID); err != nil {
		return err
	}
	var apps struct {
		Apps []appJSON `json:"apps"`
	}
	if err := s.do("GET", s.base, "/app/v1/apps?account_id="+acct.AccountID, hdr, nil, &apps); err != nil {
		return err
	}
	for _, want := range p.Apps {
		var app *appJSON
		for i := range apps.Apps {
			if apps.Apps[i].Name == want.Name {
				app = &apps.Apps[i]
			}
		}
		if app == nil {
			s.logf("%s: creating app %q (%s, %s)", p.Email, want.Name, want.Currency, want.Timezone)
			var created appJSON
			if err := s.do("POST", s.base, "/app/v1/apps?account_id="+acct.AccountID, hdr, map[string]string{
				"name": want.Name, "currency": want.Currency, "timezone": want.Timezone}, &created); err != nil {
				return err
			}
			app = &created
		}
		tid := app.sandboxTenant()
		if tid == "" {
			return fmt.Errorf("app %q has no sandbox tenant", want.Name)
		}
		t := &tenantSeed{s: s, label: want.Name, tenant: tid, session: so.Token, app: want, appID: app.ID}
		if err := t.seed(); err != nil {
			return fmt.Errorf("app %q: %w", want.Name, err)
		}
	}
	return nil
}

// ensureDevPlan puts the account on the Scale plan when the stack runs Payrudder as a SaaS (PAYRUDDER_SAAS=on, what make dev does): the seed makes two apps for
// the first person, which the Free plan would refuse. The operator's manual override is the same call the console makes; with SaaS off there is nothing to do.
func (s *devSeeder) ensureDevPlan(account string) error {
	var info struct {
		Enabled bool `json:"enabled"`
	}
	if err := s.operator("GET", "/admin/v1/saas", nil, &info); err != nil || !info.Enabled {
		return nil
	}
	var body struct {
		PlanCode string `json:"plan_code"`
	}
	body.PlanCode = "scale"
	s.logf("%s: on the Scale plan (SaaS is on)", account)
	return s.operator("PUT", "/admin/v1/organizations/"+account+"/plan", body, nil)
}
