// Package railway checks deploy/railway statically, since nothing here can deploy to Railway: every application service of the compose file has a
// config-as-code file, the files and Dockerfiles they name exist, their health checks are endpoints the services serve, and every required setting of
// every service is documented in the README and set by the bootstrap script.
package railway

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func root(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func read(t *testing.T, path ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(path...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(path ...string) bool {
	_, err := os.Stat(filepath.Join(path...))
	return err == nil
}

// composeApps are the services of deploy/compose/compose.yaml that are built from this repository (infrastructure images are not).
func composeApps(t *testing.T, repo string) []string {
	t.Helper()
	var c struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(read(t, repo, "deploy", "compose", "compose.yaml")), &c); err != nil {
		t.Fatal(err)
	}
	var apps []string
	for name, s := range c.Services {
		// A development stand-in (label payrudder.dev-only, e.g. mockoidc, the fake Google) is never deployed, so it has no Railway service.
		if labels, _ := s["labels"].(map[string]any); labels["payrudder.dev-only"] == "true" {
			continue
		}
		if _, ok := s["build"]; ok {
			apps = append(apps, name)
		}
	}
	slices.Sort(apps)
	if len(apps) < 13 {
		t.Fatalf("expected at least 13 built services in compose, found %v", apps)
	}
	return apps
}

type railwayConfig struct {
	Build struct {
		Builder        string   `json:"builder"`
		DockerfilePath string   `json:"dockerfilePath"`
		WatchPatterns  []string `json:"watchPatterns"`
	} `json:"build"`
	Deploy map[string]any `json:"deploy"`
}

// The keys of railway.schema.json (https://railway.com/railway.schema.json) that these files may use.
var (
	buildKeys  = []string{"builder", "dockerfilePath", "watchPatterns"}
	deployKeys = []string{"healthcheckPath", "healthcheckTimeout", "restartPolicyType", "restartPolicyMaxRetries", "numReplicas", "drainingSeconds", "requiredMountPath", "startCommand"}
)

func load(t *testing.T, repo, path string) railwayConfig {
	t.Helper()
	raw := read(t, repo, path)
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	for k := range top {
		if k != "$schema" && k != "build" && k != "deploy" {
			t.Errorf("%s: unknown top-level key %q", path, k)
		}
	}
	var generic struct{ Build, Deploy map[string]any }
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	for k := range generic.Build {
		if !slices.Contains(buildKeys, k) {
			t.Errorf("%s: build.%s is not a key this repo uses (railway.schema.json)", path, k)
		}
	}
	for k := range generic.Deploy {
		if !slices.Contains(deployKeys, k) {
			t.Errorf("%s: deploy.%s is not a key this repo uses (railway.schema.json)", path, k)
		}
	}
	var cfg railwayConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return cfg
}

func TestEveryComposeServiceHasARailwayConfig(t *testing.T) {
	repo := root(t)
	for _, name := range composeApps(t, repo) {
		path := "deploy/railway/services/" + name + ".railway.json"
		t.Run(name, func(t *testing.T) {
			if !exists(repo, path) {
				t.Fatalf("%s is missing", path)
			}
			cfg := load(t, repo, path)
			if cfg.Build.Builder != "DOCKERFILE" {
				t.Errorf("builder = %q, want DOCKERFILE", cfg.Build.Builder)
			}
			if !exists(repo, cfg.Build.DockerfilePath) {
				t.Errorf("dockerfilePath %q does not exist", cfg.Build.DockerfilePath)
			}
			if len(cfg.Build.WatchPatterns) == 0 {
				t.Error("no watchPatterns")
			}
			for _, p := range cfg.Build.WatchPatterns {
				if !strings.HasPrefix(p, "/") {
					t.Errorf("watch pattern %q is not rooted at the repo root", p)
				}
			}
			hc, _ := cfg.Deploy["healthcheckPath"].(string)
			if name == "admin-ui" {
				if hc != "/" {
					t.Errorf("admin-ui healthcheckPath = %q, want /", hc)
				}
			} else if hc != "/healthz" && hc != "/readyz" {
				t.Errorf("healthcheckPath = %q: pkg/health serves /healthz and /readyz", hc)
			}
			if cfg.Deploy["healthcheckTimeout"] == nil || cfg.Deploy["restartPolicyType"] == nil {
				t.Error("healthcheckTimeout and restartPolicyType are required")
			}
		})
	}
}

func TestInfraConfigs(t *testing.T) {
	repo := root(t)
	files, _ := filepath.Glob(filepath.Join(repo, "deploy", "railway", "infra", "*.railway.json"))
	iac := read(t, repo, "deploy", "railway", "iac", ".railway", "railway.ts")
	if len(files) < 4 {
		t.Fatalf("expected pgbouncer, rabbitmq, redpanda and clickhouse, found %d files", len(files))
	}
	for _, f := range files {
		rel, _ := filepath.Rel(repo, f)
		t.Run(filepath.Base(f), func(t *testing.T) {
			cfg := load(t, repo, rel)
			name := strings.TrimSuffix(filepath.Base(f), ".railway.json")
			// PgBouncer speaks only the Postgres protocol and Locomotive serves nothing: Railway checks they are running, not an HTTP path.
			hc, _ := cfg.Deploy["healthcheckPath"].(string)
			if (hc == "") != (name == "pgbouncer" || name == "locomotive") {
				t.Errorf("healthcheckPath %q: every backing service but PgBouncer and Locomotive has one", hc)
			}
			// No build: a published image, which railway.ts names as the service's source.
			if cfg.Build.DockerfilePath == "" {
				if !regexp.MustCompile(`svc\("infra", "` + name + `", \{ source: image\(`).MatchString(iac) {
					t.Errorf("%s has no Dockerfile and railway.ts gives it no image", name)
				}
				if name == "pgbouncer" && !strings.Contains(fmt.Sprint(cfg.Deploy["startCommand"]), "auth_query") {
					t.Errorf("pgbouncer: the start command has no auth_query: %v", cfg.Deploy["startCommand"])
				}
				return
			}
			if !exists(repo, cfg.Build.DockerfilePath) {
				t.Fatalf("dockerfilePath %q does not exist", cfg.Build.DockerfilePath)
			}
			// Every file COPYed from the repo by the image must exist.
			for _, m := range regexp.MustCompile(`(?m)^COPY\s+(deploy/\S+)`).FindAllStringSubmatch(read(t, repo, cfg.Build.DockerfilePath), -1) {
				if !exists(repo, m[1]) {
					t.Errorf("%s copies %s, which does not exist", cfg.Build.DockerfilePath, m[1])
				}
			}
		})
	}
}

// configDir is where a service's Config struct lives.
func configDir(repo, name string) string {
	if name == "mockpsp" {
		return filepath.Join(repo, "tools", "mockpsp")
	}
	return filepath.Join(repo, "services", name)
}

var requiredTag = regexp.MustCompile(`env:"([A-Z0-9_]+),required`)

// requiredEnv returns the required variables of a service: its own `env:"X,required"` fields and the ones of config.Base.
func requiredEnv(t *testing.T, repo, name string) []string {
	t.Helper()
	set := map[string]bool{}
	for _, src := range []string{read(t, repo, "pkg", "config", "config.go")} {
		for _, m := range requiredTag.FindAllStringSubmatch(src, -1) {
			set[m[1]] = true
		}
	}
	mains, _ := filepath.Glob(filepath.Join(configDir(repo, name), "cmd", "*", "*.go"))
	for _, f := range mains {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src := read(t, f)
		for _, m := range requiredTag.FindAllStringSubmatch(src, -1) {
			set[m[1]] = true
		}
		// The Postgres settings a service embeds (pkg/pgxx/settings.go) carry required variables of their own.
		for _, typ := range []string{"Settings", "SystemSettings"} {
			if strings.Contains(src, "pgxx."+typ+"\n") {
				for _, m := range requiredTag.FindAllStringSubmatch(pgxxSettings(t, repo, typ), -1) {
					set[m[1]] = true
				}
			}
		}
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// pgxxSettings is the source of the struct pgxx.<typ>, with the structs it embeds.
func pgxxSettings(t *testing.T, repo, typ string) string {
	t.Helper()
	src := read(t, repo, "pkg", "pgxx", "settings.go")
	_, rest, ok := strings.Cut(src, "type "+typ+" struct {\n")
	if !ok {
		t.Fatalf("pkg/pgxx/settings.go has no struct %s", typ)
	}
	body, _, _ := strings.Cut(rest, "\n}\n")
	if typ == "Settings" {
		body += pgxxSettings(t, repo, "SystemSettings")
	}
	return body
}

// section returns the README text under "### <name>" up to the next heading.
func section(readme, name string) string {
	_, rest, ok := strings.Cut(readme, "\n### "+name+"\n")
	if !ok {
		return ""
	}
	body, _, _ := strings.Cut(rest, "\n#")
	return body
}

func TestRequiredSettingsAreDocumentedAndSet(t *testing.T) {
	repo := root(t)
	readme := read(t, repo, "deploy", "railway", "README.md")
	dry := dryRun(t, repo, "full")
	for _, name := range composeApps(t, repo) {
		if name == "admin-ui" {
			continue // the console, no Go config
		}
		t.Run(name, func(t *testing.T) {
			sec := section(readme, name)
			if sec == "" {
				t.Fatalf("README.md has no \"### %s\" section with an env table", name)
			}
			vars := requiredEnv(t, repo, name)
			if len(vars) < 2 {
				t.Fatalf("found only %v: the Config struct was not parsed", vars)
			}
			for _, v := range vars {
				if !strings.Contains(sec, "| `"+v+"` |") {
					t.Errorf("%s is required by %s but is not in its README env table", v, name)
				}
				// The dry run quotes an argument that has special characters (a reference variable), so the name follows a space or a quote.
				if !strings.Contains(dry[name], " "+v+"=") && !strings.Contains(dry[name], "'"+v+"=") {
					t.Errorf("%s is required by %s but bootstrap.sh never sets it", v, name)
				}
			}
		})
	}
}

// Every service the script builds from go.Dockerfile names a directory that holds its main package.
func TestBootstrapServiceDirs(t *testing.T) {
	repo := root(t)
	script := read(t, repo, "deploy", "railway", "bootstrap.sh")
	found := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\s*go_service (\S+) (\S+) (\d+)`).FindAllStringSubmatch(script, -1) {
		found[m[1]] = m[2]
		if !exists(repo, m[2], "go.mod") || !exists(repo, m[2], "cmd", filepath.Base(m[2])) {
			t.Errorf("go_service %s: %s has no go.mod and cmd/%s", m[1], m[2], filepath.Base(m[2]))
		}
	}
	for _, name := range composeApps(t, repo) {
		if name == "admin-ui" {
			continue
		}
		if found[name] == "" {
			t.Errorf("bootstrap.sh has no go_service line for %s", name)
		}
	}
}

// baseArgs are the flags a dry run of one environment needs.
var baseArgs = []string{"--dry-run", "--environment", "staging", "--console-url", "https://console.example.com"}

// dryRun runs bootstrap.sh --dry-run (profile full: production, small: staging) and returns, per service, the variable-setting commands it printed. It needs neither the CLI nor a login.
func dryRun(t *testing.T, repo, profile string) map[string]string {
	t.Helper()
	out := dryRunOutput(t, repo, profile)
	perService := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		_, rest, ok := strings.Cut(line, "railway variable set --service ")
		if !ok {
			continue
		}
		svc, _, _ := strings.Cut(rest, " ")
		perService[svc] += " " + rest + "\n"
	}
	return perService
}

func dryRunOutput(t *testing.T, repo, profile string) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	env := map[string]string{"full": "production", "small": "staging"}[profile]
	out, err := exec.Command(bash, filepath.Join(repo, "deploy", "railway", "bootstrap.sh"), "--dry-run", "--environment", env, "--console-url", "https://console.example.com").CombinedOutput()
	if err != nil {
		t.Fatalf("--dry-run --environment %s: %v\n%s", env, err, out)
	}
	return string(out)
}

// railway.ts declares every service and the dry run deploys every one of them, production with ClickHouse and analytics-svc and staging without.
func TestBootstrapDryRun(t *testing.T) {
	repo := root(t)
	iac := read(t, repo, "deploy", "railway", "iac", ".railway", "railway.ts")
	for _, name := range append(composeApps(t, repo), "pgbouncer", "rabbitmq", "redpanda", "clickhouse") {
		if name != "admin-ui" && !strings.Contains(iac, `"`+name+`"`) {
			t.Errorf("railway.ts does not declare %s", name)
		}
	}
	for _, profile := range []string{"full", "small"} {
		text := dryRunOutput(t, repo, profile)
		deploy := " --detach\n" // no environment is connected to GitHub: the checkout is uploaded
		for _, name := range composeApps(t, repo) {
			if name == "admin-ui" {
				continue
			}
			small := profile == "small" && name == "analytics-svc"
			if has := strings.Contains(text, "--service "+name+deploy); has == small {
				t.Errorf("profile %s: %s deployed = %v", profile, name, has)
			}
		}
		if clickhouse := strings.Contains(text, "--service clickhouse"); clickhouse == (profile == "small") {
			t.Errorf("profile %s: ClickHouse present = %v", profile, clickhouse)
		}
		for _, gone := range []string{"init-postgres", "init-redpanda", "init-minio", "minio", "mailpit", "admin-ui"} {
			if strings.Contains(text, "--service "+gone+" ") {
				t.Errorf("profile %s: %s is still set or deployed", profile, gone)
			}
		}
		if strings.Contains(text, "unbound variable") {
			t.Errorf("profile %s: %s", profile, text)
		}
	}
}

// The console's mail page talks to the notifier's signed listener through the gateway (plan 11). Both ends are set by the bootstrap script and documented: the
// notifier listens on :9111 with a generated secret, the gateway calls it by its private name and refers to the same secret, and the listener's port is the one
// in the address (the gateway refuses to start with one of its two variables).
func TestMailEventsAreWired(t *testing.T) {
	repo := root(t)
	readme := read(t, repo, "deploy", "railway", "README.md")
	dry := dryRun(t, repo, "small")
	for _, want := range []string{"NOTIFIER_EVENTS_ADDR=:9111"} {
		if !strings.Contains(dry["notifier"], " "+want) {
			t.Errorf("bootstrap.sh does not set %s on the notifier:\n%s", want, dry["notifier"])
		}
	}
	for _, want := range []string{"PAYRUDDER_NOTIFIER_EVENTS_URL=http://notifier.railway.internal:9111", "'PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET=${{notifier.NOTIFIER_MAIL_EVENTS_SECRET}}"} {
		if !strings.Contains(dry["gateway"], want) {
			t.Errorf("bootstrap.sh does not set %s on the gateway:\n%s", want, dry["gateway"])
		}
	}
	if out := dryRunOutput(t, repo, "small"); !strings.Contains(out, "[only if NOTIFIER_MAIL_EVENTS_SECRET is unset on notifier]") {
		t.Errorf("bootstrap.sh does not generate the notifier's secret:\n%s", out)
	}
	for _, v := range []string{"NOTIFIER_EVENTS_ADDR", "NOTIFIER_MAIL_EVENTS_SECRET"} {
		if !strings.Contains(section(readme, "notifier"), "| `"+v+"` |") {
			t.Errorf("README.md's notifier table lacks %s", v)
		}
	}
	for _, v := range []string{"PAYRUDDER_NOTIFIER_EVENTS_URL", "PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET"} {
		if !strings.Contains(section(readme, "gateway"), "| `"+v+"` |") {
			t.Errorf("README.md's gateway table lacks %s", v)
		}
	}
}

// ERROR_TRACKING_URL puts the DSN on every Go service (but not the mock provider, which has no tracker) and nowhere without it.
func TestErrorTrackingURLIsSetOnEveryGoServiceWhenGiven(t *testing.T) {
	repo := root(t)
	const dsn = "https://publickey@errors.example.com/7"
	run := func(env ...string) map[string]string {
		cmd := exec.Command("bash", filepath.Join(repo, "deploy", "railway", "bootstrap.sh"), "--dry-run", "--environment", "production", "--console-url", "https://console.example.com")
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("dry run: %v\n%s", err, out)
		}
		per := map[string]string{}
		for _, line := range strings.Split(string(out), "\n") {
			if _, rest, ok := strings.Cut(line, "railway variable set --service "); ok {
				svc, _, _ := strings.Cut(rest, " ")
				per[svc] += " " + rest + "\n"
			}
		}
		return per
	}
	without := run("ERROR_TRACKING_URL=")
	with := run("ERROR_TRACKING_URL=" + dsn)
	for _, name := range composeApps(t, repo) {
		if name == "admin-ui" {
			continue
		}
		if strings.Contains(without[name], "PAYRUDDER_ERROR_TRACKING_URL") {
			t.Errorf("%s gets an error tracking URL that nobody gave", name)
		}
		has := strings.Contains(with[name], " PAYRUDDER_ERROR_TRACKING_URL="+dsn)
		if want := name != "mockpsp"; has != want {
			t.Errorf("%s: PAYRUDDER_ERROR_TRACKING_URL set = %v, want %v", name, has, want)
		}
	}
}

// The provider's webhook of delivery events answers 404 until the gateway has PAYRUDDER_MAIL_WEBHOOK_SECRET, a secret the operator gets from the provider (it cannot be generated),
// so the bootstrap passes MAIL_WEBHOOK_SECRET on to the gateway, and to nobody else, and sets nothing when it is empty. The README's gateway table lists it.
func TestMailWebhookSecretIsSetOnTheGatewayWhenGiven(t *testing.T) {
	repo := root(t)
	const secret = "whsec_dGhlLXN2aXgtc2VjcmV0LW9mLXRoZS10ZXN0cw=="
	run := func(value string) map[string]string {
		cmd := exec.Command("bash", append([]string{filepath.Join(repo, "deploy", "railway", "bootstrap.sh")}, baseArgs...)...)
		cmd.Env = append(os.Environ(), "MAIL_WEBHOOK_SECRET="+value)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("dry run: %v\n%s", err, out)
		}
		per := map[string]string{}
		for _, line := range strings.Split(string(out), "\n") {
			if _, rest, ok := strings.Cut(line, "railway variable set --service "); ok {
				svc, _, _ := strings.Cut(rest, " ")
				per[svc] += " " + rest + "\n"
			}
		}
		return per
	}
	without, with := run(""), run(secret)
	for svc, vars := range without {
		if strings.Contains(vars, "PAYRUDDER_MAIL_WEBHOOK_SECRET") {
			t.Errorf("%s gets a mail webhook secret that nobody gave", svc)
		}
	}
	for svc, vars := range with {
		if has := strings.Contains(vars, " PAYRUDDER_MAIL_WEBHOOK_SECRET="+secret); has != (svc == "gateway") {
			t.Errorf("%s: PAYRUDDER_MAIL_WEBHOOK_SECRET set = %v, want %v", svc, has, svc == "gateway")
		}
	}
	if !strings.Contains(with["gateway"], "PAYRUDDER_NOTIFIER_EVENTS_URL=") {
		t.Errorf("the gateway gets the webhook secret without the notifier's address, and refuses to start:\n%s", with["gateway"])
	}
	if !strings.Contains(section(read(t, repo, "deploy", "railway", "README.md"), "gateway"), "| `PAYRUDDER_MAIL_WEBHOOK_SECRET` |") {
		t.Error("README.md's gateway table lacks PAYRUDDER_MAIL_WEBHOOK_SECRET")
	}
}

// One Railway environment of plan 23: the console on Cloudflare (no admin-ui, the proxy secret generated), custom domains in every public address, a
// sandbox-only deployment, and the nafru-geo and Nafru keys passed to the one service that reads each. Bad values stop the script before it does anything.
func TestBootstrapForAnEnvironment(t *testing.T) {
	repo := root(t)
	run := func(env []string, args ...string) (string, error) {
		cmd := exec.Command("bash", append([]string{filepath.Join(repo, "deploy", "railway", "bootstrap.sh"), "--dry-run"}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run([]string{"GEO_API_KEY=geo-key", "NAFRU_BASE_URL=https://mail.example.com", "NAFRU_API_KEY=nafru-key"},
		"--environment", "staging", "--console-url", "https://console-staging.example.com", "--api-domain", "api-staging.example.com",
		"--allowed-environments", "sandbox")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	per := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if _, rest, ok := strings.Cut(line, "railway variable set --service "); ok {
			svc, _, _ := strings.Cut(rest, " ")
			per[svc] += " " + rest + "\n"
		}
	}
	if strings.Contains(out, "admin-ui") {
		t.Error("admin-ui is created although the console runs elsewhere")
	}
	if !strings.Contains(out, "[only if PAYRUDDER_TRUSTED_PROXY_SECRET is unset on gateway]") {
		t.Error("the gateway's proxy secret for the console's Worker is not generated")
	}
	for _, want := range []string{"railway domain api-staging.example.com --service gateway --port 8000", "railway config apply --environment staging --yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	for svc, wants := range map[string][]string{
		"gateway": {"PAYRUDDER_APP_URL=https://console-staging.example.com", "PAYRUDDER_PUBLIC_URL=https://api-staging.example.com",
			"PAYRUDDER_ALLOWED_ENVIRONMENTS=sandbox ", "PAYRUDDER_GEO_API_KEY=geo-key"},
		"plans-svc":   {"PAYRUDDER_APP_URL=https://console-staging.example.com", "PAYRUDDER_ALLOWED_ENVIRONMENTS=sandbox ", "PAYRUDDER_KAFKA_CREATE_TOPICS=true"},
		"billing-svc": {"PAYRUDDER_PUBLIC_URL=https://api-staging.example.com", "S3_BUCKET=payrudder-invoices-staging", "S3_REGION=auto"},
		"assets-svc": {"S3_BUCKET=payrudder-assets-staging", "'PAYRUDDER_PG_APP_DSN=postgres://assets_app:${{Postgres.PG_APP_PASSWORD}}@pgbouncer.railway.internal:6432/assets_db'",
			"'PAYRUDDER_PG_SYSTEM_DSN=postgres://assets_system:${{Postgres.PG_SYSTEM_PASSWORD}}@${{Postgres.RAILWAY_PRIVATE_DOMAIN}}:5432/assets_db'",
			"'PAYRUDDER_PG_ADMIN_DSN=${{Postgres.DATABASE_URL}}'"},
		"notifier": {"NAFRU_BASE_URL=https://mail.example.com", "NAFRU_API_KEY=nafru-key"},
	} {
		for _, want := range wants {
			if !strings.Contains(per[svc], " "+want) {
				t.Errorf("%s lacks %s:\n%s", svc, want, per[svc])
			}
		}
	}
	for svc, vars := range per {
		if svc != "gateway" && strings.Contains(vars, "PAYRUDDER_GEO_API_KEY") {
			t.Errorf("%s gets the nafru-geo key", svc)
		}
		if svc != "notifier" && strings.Contains(vars, "NAFRU_API_KEY") {
			t.Errorf("%s gets the Nafru mail key", svc)
		}
	}
	for _, bad := range [][]string{{"--environment", "staging", "--console-url", "https://c.example.com", "--allowed-environments", "live"},
		{"--environment", "staging", "--console-url", "http://console.example.com"}, {"--console-url", "https://c.example.com"},
		{"--environment", "staging", "--console-url", "https://c.example.com", "--api-domain", "https://api.example.com"}} {
		if out, err := run(nil, bad...); err == nil {
			t.Errorf("%v was accepted:\n%s", bad, out)
		}
	}
	// A real run without the storage keys, or with half of Nafru's, stops before it touches Railway (no CLI is called: none is needed for this).
	for missing, want := range map[string]string{"S3_SECRET_KEY": "S3_SECRET_KEY is required", "NAFRU_API_KEY": "go together"} {
		env := []string{"NAFRU_BASE_URL=https://mail.example.com", "NAFRU_API_KEY=k", "S3_ENDPOINT=https://a.r2.cloudflarestorage.com", "S3_ACCESS_KEY=a", "S3_SECRET_KEY=s", missing + "="}
		cmd := exec.Command("bash", filepath.Join(repo, "deploy", "railway", "bootstrap.sh"), "--environment", "staging", "--console-url", "https://c.example.com")
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), want) {
			t.Errorf("a run without %s: %v\n%s", missing, err, out)
		}
	}
}

// A release deploys production and a push to main staging with --deploy-only (deploy-production.yml, deploy-staging.yml): every Payrudder service of
// the environment is built from the checkout, plans-svc first and the gateway last, each waiting for its build; nothing is created, set or linked, and
// the console's container is left out when it runs elsewhere. Staging has no analytics-svc.
func TestBootstrapDeployOnly(t *testing.T) {
	repo := root(t)
	for _, env := range []string{"production", "staging"} {
		t.Run(env, func(t *testing.T) { testDeployOnly(t, repo, env) })
	}
	if out, err := exec.Command("bash", filepath.Join(repo, "deploy", "railway", "bootstrap.sh"), "--dry-run", "--deploy-only").CombinedOutput(); err == nil {
		t.Errorf("--deploy-only without --environment was accepted:\n%s", out)
	}
}

func testDeployOnly(t *testing.T, repo, env string) {
	out, err := exec.Command("bash", filepath.Join(repo, "deploy", "railway", "bootstrap.sh"), "--dry-run", "--deploy-only", "--environment", env).CombinedOutput()
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	var got []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "+ ") {
			svc, ok := strings.CutPrefix(line, "+ railway up --service ")
			if !ok || !strings.HasSuffix(svc, " --ci") {
				t.Errorf("--deploy-only runs more than `railway up --ci`: %q", line)
				continue
			}
			got = append(got, strings.TrimSuffix(svc, " --ci"))
		}
	}
	var want []string
	for _, name := range composeApps(t, repo) {
		if name != "admin-ui" && (env == "production" || name != "analytics-svc") {
			want = append(want, name)
		}
	}
	if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
		t.Errorf("deployed %v, want every service of %v", got, want)
	}
	if len(got) == 0 || got[0] != "plans-svc" || got[len(got)-1] != "gateway" {
		t.Errorf("order %v: plans-svc must be first and the gateway last", got)
	}
}

// TestSetupSecrets runs setup-secrets.sh with a fake gh: --check counts what is missing, a run fills it (repository secrets through gh's stdin, the
// bootstrap inputs into a mode-600 file that a shell reads back unchanged), and a second --check passes.
func TestSetupSecrets(t *testing.T) {
	repo, dir := root(t), t.TempDir()
	bin, secrets, log := filepath.Join(dir, "bin"), filepath.Join(dir, "secrets"), filepath.Join(dir, "gh.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// The fake gh lists CLOUDFLARE_ACCOUNT_ID and every secret set through it.
	fake := "#!/bin/bash\n" +
		"if [[ $1 == secret && $2 == list ]]; then printf 'CLOUDFLARE_ACCOUNT_ID\\t2026-11-05\\n'; [[ -f $GH_LOG ]] && sed 's/=.*/\\t2026-11-05/' \"$GH_LOG\"; exit 0; fi\n" +
		"if [[ $1 == secret && $2 == set ]]; then printf '%s=%s\\n' \"$3\" \"$(cat)\" >>\"$GH_LOG\"; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(stdin string, args ...string) (string, error) {
		cmd := exec.Command("bash", append([]string{filepath.Join(repo, "deploy", "railway", "setup-secrets.sh")}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "PAYRUDDER_SECRETS_DIR="+secrets, "GH_LOG="+log, "GEO_API_KEY=from-the-shell")
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, err := run("", "--check", "production")
	if err == nil || !strings.Contains(out, "10 zorunlu kalem eksik") {
		t.Fatalf("--check with nothing set: want 10 missing (the shell's GEO_API_KEY does not count), got err=%v\n%s", err, out)
	}
	for _, want := range []string{"✓ CLOUDFLARE_ACCOUNT_ID", "✗ CLOUDFLARE_API_TOKEN", "✗ RAILWAY_TOKEN_PRODUCTION", "✗ GEO_API_KEY", "- NAFRU_API_KEY"} {
		if !strings.Contains(out, want) {
			t.Errorf("--check: no %q in\n%s", want, out)
		}
	}
	if out, _ := run("", "--check", "staging"); strings.Contains(out, "RAILWAY_TOKEN_PRODUCTION") || !strings.Contains(out, "✗ RAILWAY_TOKEN_STAGING") {
		t.Errorf("staging must ask for its own token, not production's:\n%s", out)
	}

	// Answers in order: Cloudflare token, Railway token, GEO (Enter generates), a wrong then a right Nafru URL, the Nafru key, the R2 endpoint and keys,
	// operators, no Google, the Telegram bot and chat, the Locomotive token, no watchdog.
	key := `a key with spaces, 'quotes' and $dollars`
	answers := "cf_token_abcdefghijklmnopqrstuvwxyz0123\nrlwy_proj_abcdefghijklmnop\n\nnot-a-url\nhttps://api.nafru.test\n" + key +
		"\nhttps://" + strings.Repeat("a", 32) + ".r2.cloudflarestorage.com\n" + strings.Repeat("b", 32) + "\n" + strings.Repeat("c", 64) + "\nops@payrudder.com\n\n" +
		"123456:" + strings.Repeat("t", 35) + "\n-1001234\n" + strings.Repeat("l", 36) + "\n\n"
	if out, err := run(answers, "production"); err != nil || !strings.Contains(out, "bootstrap.sh --dry-run --environment production --console-url https://console.payrudder.com") {
		t.Fatalf("a full run: err=%v\n%s", err, out)
	}
	if b, _ := os.ReadFile(log); string(b) != "CLOUDFLARE_API_TOKEN=cf_token_abcdefghijklmnopqrstuvwxyz0123\nRAILWAY_TOKEN_PRODUCTION=rlwy_proj_abcdefghijklmnop\n" {
		t.Errorf("gh secret set got:\n%s", b)
	}
	file := filepath.Join(secrets, "production.env")
	if st, err := os.Stat(file); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the file: %v %v", st, err)
	}
	back, err := exec.Command("bash", "-c", `set -a; . "$1"; printf '%s|%s|%s' "$NAFRU_API_KEY" "$NAFRU_BASE_URL" "$GEO_API_KEY"`, "-", file).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(string(back), "|"); got[0] != key || got[1] != "https://api.nafru.test" || !regexp.MustCompile(`^ngeo_payrudder_production_[0-9a-f]{48}$`).MatchString(got[2]) {
		t.Errorf("read back: %q", got)
	}
	if out, err := run("", "--check", "production"); err != nil {
		t.Errorf("--check after the run: %v\n%s", err, out)
	}
}

func TestGoogleSignInIsSetOnTheGatewayWhenGiven(t *testing.T) {
	cmd := exec.Command("bash", append([]string{filepath.Join(root(t), "deploy", "railway", "bootstrap.sh")}, baseArgs...)...)
	cmd.Env = append(os.Environ(), "PAYRUDDER_GOOGLE_CLIENT_ID=id.apps.googleusercontent.com", "PAYRUDDER_GOOGLE_CLIENT_SECRET=google-secret")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "railway variable set --service gateway --skip-deploys PAYRUDDER_GOOGLE_CLIENT_ID=id.apps.googleusercontent.com PAYRUDDER_GOOGLE_CLIENT_SECRET=google-secret") {
		t.Errorf("no Google variables on the gateway:\n%s", out)
	}
}

// Observability on Railway: Prometheus scrapes every Payrudder service over the private network, and every Go service sends its traces to the
// environment's collector.
func TestObservabilityReachesEveryService(t *testing.T) {
	repo := root(t)
	prom := read(t, repo, "deploy", "railway", "images", "prometheus", "prometheus.yml")
	text := dryRunOutput(t, repo, "full")
	for _, name := range composeApps(t, repo) {
		if name == "admin-ui" || name == "mockpsp" {
			continue
		}
		if !strings.Contains(prom, `"`+name+`.railway.internal:9100"`) {
			t.Errorf("Prometheus does not scrape %s", name)
		}
		if !strings.Contains(text, "--service "+name+" --skip-deploys SERVICE_DIR=") || !regexp.MustCompile(`--service `+name+` --skip-deploys SERVICE_DIR=\S+ GOMEMLIMIT=\S+ PAYRUDDER_OTLP_ENDPOINT=otel-collector\.railway\.internal:4317`).MatchString(text) {
			t.Errorf("%s does not send its traces to the collector", name)
		}
	}
}
