package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

// Exit codes: 1 is a failure (an error, a rejected event, a delivery the endpoint did not accept), 2 is a command line that cannot be understood.
const (
	exitFailure = 1
	exitUsage   = 2
)

// errUsage is a mistake in the command line; the message is shown with the exit code 2.
type errUsage string

func (e errUsage) Error() string { return string(e) }

// errFailed is a failure that was already described on the output: the command exits 1 without printing anything more.
var errFailed = errors.New("failed")

// cli is one run of the command: where it reads and writes, and the environment it sees. Tests build their own.
type cli struct {
	in       io.Reader
	out, err io.Writer
	getenv   func(string) string
	// mu keeps the lines of concurrent webhook deliveries apart.
	mu sync.Mutex
}

func run(ctx context.Context, args []string, in io.Reader, out, errw io.Writer, getenv func(string) string) int {
	c := &cli{in: in, out: out, err: errw, getenv: getenv}
	err := c.dispatch(ctx, args)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errFailed):
		return exitFailure
	}
	var u errUsage
	if errors.As(err, &u) {
		fmt.Fprintf(errw, "payrudder: %s\n(run `payrudder help` for the commands)\n", u)
		return exitUsage
	}
	fmt.Fprintf(errw, "payrudder: %s\n", strings.TrimPrefix(err.Error(), "payrudder: "))
	var perr *payrudder.Error
	if errors.As(err, &perr) && perr.Kind == payrudder.KindAuthentication {
		fmt.Fprintln(errw, "The API key was refused. Run `payrudder login` with a current key, or check PAYRUDDER_API_KEY.")
	}
	return exitFailure
}

const usageText = `payrudder: the Payrudder command line

usage: payrudder <command> [flags]

  login                       store your API key (checked against the API) in your user config directory
  logout                      remove the stored key
  events send                 send one usage event
  events tail                 follow a customer's usage (the API does not list events, so this polls the usage totals)
  webhooks list               your webhook endpoints
  webhooks create             register an endpoint (its signing secret is printed once)
  webhooks deliveries         deliveries to your endpoints and how each went; --watch follows them
  webhooks trigger ping       send a test event to an endpoint and report how it answered
  webhooks redeliver          send a delivery again from the start
  webhooks listen             receive deliveries on this machine, verify them and forward them to your app
  customers list              your customers
  customers create            make a customer
  plans create                make a plan (its prices are a version)
  plans publish               publish the next version of a plan from a JSON file
  meters create               make a meter, or the next version of one
  subscriptions create        subscribe a customer to a plan
  invoices list               your invoices
  version                     the version of this program and of the SDK it is built on

The key comes from $PAYRUDDER_API_KEY or from "payrudder login"; the gateway address from --base-url, $PAYRUDDER_BASE_URL or the login
(default http://localhost:42800). A qk_test_ key acts on the sandbox, a qk_live_ key on live. "payrudder <command> -h" lists a command's flags.`

func (c *cli) dispatch(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(c.err, usageText)
		return errUsage("a command is required")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprintln(c.out, usageText)
		return nil
	case "version":
		fmt.Fprintf(c.out, "payrudder %s (built with %s)\n", payrudder.Version, runtime.Version())
		return nil
	case "login":
		return c.login(ctx, rest)
	case "logout":
		return c.logout()
	case "events":
		return c.group(ctx, "events", rest, map[string]func(context.Context, []string) error{"send": c.eventsSend, "tail": c.eventsTail})
	case "webhooks":
		return c.group(ctx, "webhooks", rest, map[string]func(context.Context, []string) error{
			"list": c.webhooksList, "create": c.webhooksCreate, "deliveries": c.webhooksDeliveries, "trigger": c.webhooksTrigger, "listen": c.webhooksListen, "redeliver": c.webhooksRedeliver,
		})
	case "customers":
		return c.group(ctx, "customers", rest, map[string]func(context.Context, []string) error{"list": c.customersList, "create": c.customersCreate})
	case "plans":
		return c.group(ctx, "plans", rest, map[string]func(context.Context, []string) error{"create": c.plansCreate, "publish": c.plansPublish})
	case "meters":
		return c.group(ctx, "meters", rest, map[string]func(context.Context, []string) error{"create": c.metersCreate})
	case "subscriptions":
		return c.group(ctx, "subscriptions", rest, map[string]func(context.Context, []string) error{"create": c.subscriptionsCreate})
	case "invoices":
		return c.group(ctx, "invoices", rest, map[string]func(context.Context, []string) error{"list": c.invoicesList})
	}
	return errUsage(fmt.Sprintf("unknown command %q", cmd))
}

func (c *cli) group(ctx context.Context, name string, args []string, subs map[string]func(context.Context, []string) error) error {
	if len(args) == 0 {
		return errUsage(fmt.Sprintf("%s needs a subcommand (%s)", name, joinKeys(subs)))
	}
	f, ok := subs[args[0]]
	if !ok {
		return errUsage(fmt.Sprintf("unknown subcommand %q of %s (%s)", args[0], name, joinKeys(subs)))
	}
	return f(ctx, args[1:])
}

func joinKeys(m map[string]func(context.Context, []string) error) string {
	return strings.Join(slices.Sorted(maps.Keys(m)), ", ")
}

// flags makes a flag set that reports its mistakes as usage errors and prints its help on the error output.
func (c *cli) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.err)
	return fs
}

// parse reads the flags; a flag error is a usage error (flag has printed it already) and -h is not an error.
func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errUsage(err.Error())
	}
	if fs.NArg() > 0 {
		return errUsage(fmt.Sprintf("%s: unexpected argument %q", fs.Name(), fs.Arg(0)))
	}
	return nil
}

// printJSON writes v as indented JSON.
func (c *cli) printJSON(v any) error {
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (c *cli) println(format string, a ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(c.out, format+"\n", a...)
}

// --- the stored login

// storedConfig is what "payrudder login" keeps. The file is the user's own (0600, in a 0700 directory) and holds the API key in the clear, like the
// config of the AWS, GitHub and Stripe command lines: protect the disk, rotate the key when a laptop is lost.
type storedConfig struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url,omitempty"`
}

func (c *cli) configPath() (string, error) {
	if d := c.getenv("PAYRUDDER_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "config.json"), nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot find your config directory (%w): set PAYRUDDER_CONFIG_DIR", err)
	}
	return filepath.Join(d, "payrudder", "config.json"), nil
}

func (c *cli) loadConfig() (storedConfig, error) {
	var cfg storedConfig
	p, err := c.configPath()
	if err != nil {
		return cfg, err
	}
	f, err := os.Open(p) //nolint:gosec // the user's own config file, at a path derived from their config directory
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(c.err, "payrudder: warning: %s can be read by other users (mode %04o); run: chmod 600 %s\n", p, st.Mode().Perm(), p)
	}
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return storedConfig{}, fmt.Errorf("%s is not valid (%w): run `payrudder login` again", p, err)
	}
	return cfg, nil
}

func (c *cli) saveConfig(cfg storedConfig) (string, error) {
	p, err := c.configPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(cfg, "", "  ") //nolint:gosec // storing the key is the purpose of this file (mode 0600)
	// Written next to the file and renamed, in a file that did not exist (a fresh random name, created with O_EXCL and mode 0600), so the key is never on disk readable
	// by others, not even for a moment, and a link that somebody planted at a name the program would have used (config.json.tmp) is not followed.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".config-*.tmp")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(append(b, '\n'))
	if err := errors.Join(werr, tmp.Close()); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return p, nil
}

// client builds the SDK client from the environment, the stored login and the --base-url flag (in that order of weakness: flag, then environment, then login).
func (c *cli) client(baseFlag string) (*payrudder.Client, error) {
	cfg, err := c.loadConfig()
	if err != nil {
		return nil, err
	}
	key := firstNonEmpty(c.getenv(payrudder.EnvAPIKey), cfg.APIKey)
	if key == "" {
		return nil, errors.New("not signed in: run `payrudder login`, or set PAYRUDDER_API_KEY")
	}
	return c.newClient(key, firstNonEmpty(baseFlag, c.getenv(payrudder.EnvBaseURL), cfg.BaseURL))
}

// refuseLiveKeyInTheClear stops a live key (qk_live_) from being sent over plain http to anything but this machine: on the way anyone could read it. The address the
// SDK falls back to (an empty base) is http://localhost, so it passes; so do localhost, *.localhost and a loopback address. A sandbox key may go over http.
func refuseLiveKeyInTheClear(key, base string) error {
	if !strings.HasPrefix(key, "qk_live_") || base == "" {
		return nil
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" {
		return nil // https is what it should be, and anything that is not http(s) the SDK refuses itself
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if ip, err := netip.ParseAddr(host); (err == nil && ip.Unmap().IsLoopback()) || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return nil
	}
	return fmt.Errorf("refusing to send a live API key over plain http to %s: use an https:// address (http is allowed only for localhost, 127.0.0.1 and ::1)", u.Host)
}

func (c *cli) newClient(key, base string) (*payrudder.Client, error) {
	if err := refuseLiveKeyInTheClear(key, base); err != nil {
		return nil, err
	}
	opts := []payrudder.Option{payrudder.WithAPIKey(key), payrudder.WithUserAgent("payrudder-cli/" + payrudder.Version)}
	if base != "" {
		opts = append(opts, payrudder.WithBaseURL(base))
	}
	return payrudder.New(opts...)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// readLine reads one line from the input without its line end.
func (c *cli) readLine() (string, error) {
	s, err := bufio.NewReader(c.in).ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || s == "") {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}
