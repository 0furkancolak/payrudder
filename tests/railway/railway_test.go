// Package railway checks deploy/railway statically, since nothing here can deploy to Railway: every application service of the compose file has a
// config-as-code file, the files and Dockerfiles they name exist, their health checks are endpoints the services serve, and every required setting of
// every service is documented in the README and set by the bootstrap script.
package railway

import (
	"encoding/json"
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
		// A development stand-in (label quotify.dev-only, e.g. mockoidc, the fake Google) is never deployed, so it has no Railway service.
		if labels, _ := s["labels"].(map[string]any); labels["quotify.dev-only"] == "true" {
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
	deployKeys = []string{"healthcheckPath", "healthcheckTimeout", "restartPolicyType", "restartPolicyMaxRetries", "numReplicas", "drainingSeconds", "requiredMountPath"}
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
	if len(files) < 8 {
		t.Fatalf("expected the 5 backing services and 3 init jobs, found %d files", len(files))
	}
	for _, f := range files {
		rel, _ := filepath.Rel(repo, f)
		t.Run(filepath.Base(f), func(t *testing.T) {
			cfg := load(t, repo, rel)
			if !exists(repo, cfg.Build.DockerfilePath) {
				t.Fatalf("dockerfilePath %q does not exist", cfg.Build.DockerfilePath)
			}
			job := strings.HasPrefix(filepath.Base(f), "init-")
			hc, _ := cfg.Deploy["healthcheckPath"].(string)
			if job == (hc != "") {
				t.Errorf("healthcheckPath %q: a one-shot job has none, a backing service has one", hc)
			}
			if job && cfg.Deploy["restartPolicyType"] != "NEVER" {
				t.Error("a one-shot job must have restartPolicyType NEVER")
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
		for _, m := range requiredTag.FindAllStringSubmatch(read(t, f), -1) {
			set[m[1]] = true
		}
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
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
			continue // nginx, no Go config
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

// dryRun runs bootstrap.sh --dry-run and returns, per service, the variable-setting commands it printed. It needs neither the CLI nor a login.
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
	out, err := exec.Command(bash, filepath.Join(repo, "deploy", "railway", "bootstrap.sh"), "--dry-run", "--profile", profile).CombinedOutput()
	if err != nil {
		t.Fatalf("--dry-run --profile %s: %v\n%s", profile, err, out)
	}
	return string(out)
}

// The dry run adds every service (and leaves out the full profile's extras in the small one).
func TestBootstrapDryRun(t *testing.T) {
	repo := root(t)
	for _, profile := range []string{"full", "small"} {
		text := dryRunOutput(t, repo, profile)
		for _, name := range composeApps(t, repo) {
			want := "railway add --service " + name + "\n"
			small := profile == "small" && name == "analytics-svc"
			if has := strings.Contains(text, want); has == small {
				t.Errorf("profile %s: %q present = %v", profile, strings.TrimSpace(want), has)
			}
		}
		if clickhouse := strings.Contains(text, "--service clickhouse"); clickhouse == (profile == "small") {
			t.Errorf("profile %s: ClickHouse present = %v", profile, clickhouse)
		}
		if strings.Contains(text, "unbound variable") {
			t.Errorf("profile %s: %s", profile, text)
		}
	}
}
