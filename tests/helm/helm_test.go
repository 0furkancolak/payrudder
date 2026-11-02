// Package helm tests the rendered manifests of deploy/helm: they run `helm template` (bin/helm, built by make tools) and look at
// the YAML, so they need no cluster. Properties that matter for safety are asserted here rather than reviewed by eye.
package helm

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/0furkancolak/payrudder/pkg/env"
)

type obj map[string]any

func (o obj) get(path ...string) any {
	var cur any = map[string]any(o)
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

func (o obj) str(path ...string) string { s, _ := o.get(path...).(string); return s }
func (o obj) kind() string              { return o.str("kind") }
func (o obj) name() string              { return o.str("metadata", "name") }

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// render runs helm template for a chart and values files / --set arguments and returns the manifests.
func render(t *testing.T, chart string, args ...string) []obj {
	t.Helper()
	root := repoRoot(t)
	helm := filepath.Join(root, "bin", "helm")
	if _, err := os.Stat(helm); err != nil {
		t.Skipf("%s not built (make tools)", helm)
	}
	full := append([]string{"template", "x", filepath.Join(root, "deploy", "helm", chart), "--namespace", "payrudder"}, args...)
	cmd := exec.Command(helm, full...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm %s: %v\n%s", strings.Join(full, " "), err, stderr.String())
	}
	var docs []obj
	dec := yaml.NewDecoder(&out)
	for {
		var m map[string]any // decoding into obj would make every nested map an obj too
		if err := dec.Decode(&m); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("parse rendered YAML: %v", err)
		}
		if len(m) > 0 {
			docs = append(docs, obj(m))
		}
	}
	return docs
}

func values(t *testing.T, chart, name string) string {
	return filepath.Join(repoRoot(t), "deploy", "helm", chart, name)
}

// containers returns every container of every Deployment, StatefulSet and Job.
func containers(docs []obj) []obj {
	var out []obj
	for _, d := range docs {
		var list any
		switch d.kind() {
		case "Deployment", "StatefulSet", "Job":
			list = d.get("spec", "template", "spec", "containers")
		}
		if cs, ok := list.([]any); ok {
			for _, c := range cs {
				if m, ok := c.(map[string]any); ok {
					out = append(out, obj(m))
				}
			}
		}
	}
	return out
}

func envOf(c obj) map[string]string {
	out := map[string]string{}
	if es, ok := c["env"].([]any); ok {
		for _, e := range es {
			m, _ := e.(map[string]any)
			n, _ := m["name"].(string)
			v, _ := m["value"].(string)
			out[n] = v
		}
	}
	return out
}

func find(docs []obj, kind, name string) obj {
	for _, d := range docs {
		if d.kind() == kind && d.name() == name {
			return d
		}
	}
	return nil
}

// TestSandboxProfileCannotCreateALiveTenant (phase 45 G) reads the allowed environments the sandbox profile gives plans-svc and the gateway through the same
// parser the services use, and asks the same question they ask: may a live tenant (or a live key) exist here? The services enforce it (plans-svc
// SandboxOnlyDeployment and SandboxOnlyProductPath, the gateway's TestSandboxOnlyDeploymentRejectsLiveKeys); this ties the chart to those tests, so a profile
// that quietly allowed live would fail here and not on a customer's cluster.
func TestSandboxProfileCannotCreateALiveTenant(t *testing.T) {
	docs := render(t, "payrudder", "-f", values(t, "payrudder", "values-sandbox.yaml"))
	seen := map[string]bool{}
	for _, c := range containers(docs) {
		raw, ok := envOf(c)["PAYRUDDER_ALLOWED_ENVIRONMENTS"]
		if !ok {
			continue
		}
		name, _ := c["name"].(string)
		list, err := env.ParseList(raw)
		if err != nil {
			t.Errorf("%s: PAYRUDDER_ALLOWED_ENVIRONMENTS=%q does not parse: %v", name, raw, err)
			continue
		}
		strs := make([]string, len(list))
		for i, e := range list {
			strs[i] = e.String()
		}
		if env.Allowed(strs, env.Live) {
			t.Errorf("%s allows live (%q): the sandbox profile must not let a live tenant or key exist", name, raw)
		}
		if !env.Allowed(strs, env.Sandbox) {
			t.Errorf("%s does not allow sandbox (%q)", name, raw)
		}
		seen[name] = true
	}
	for _, must := range []string{"plans-svc", "gateway"} {
		if !seen[must] {
			t.Errorf("%s does not carry PAYRUDDER_ALLOWED_ENVIRONMENTS in the sandbox profile: seen %v", must, seen)
		}
	}
	// For contrast, the default install serves both.
	for _, c := range containers(render(t, "payrudder")) {
		if raw, ok := envOf(c)["PAYRUDDER_ALLOWED_ENVIRONMENTS"]; ok {
			list, err := env.ParseList(raw)
			if err != nil || len(list) != 2 {
				t.Errorf("default install: %v PAYRUDDER_ALLOWED_ENVIRONMENTS=%q (%v), want sandbox and live", c["name"], raw, err)
			}
		}
	}
}

func TestSandboxProfileHasNoLiveProvider(t *testing.T) {
	docs := render(t, "payrudder", "-f", values(t, "payrudder", "values-sandbox.yaml"))
	sawEnvs, sawPayments := 0, 0
	for _, c := range containers(docs) {
		env := envOf(c)
		if v, ok := env["PAYRUDDER_ALLOWED_ENVIRONMENTS"]; ok {
			sawEnvs++
			if v != "sandbox" {
				t.Errorf("%v allows environments %q, want only sandbox", c["name"], v)
			}
		}
		if v, ok := env["PAYMENTS_LIVE_PROVIDER"]; ok {
			sawPayments++
			if v != "" {
				t.Errorf("%v has PAYMENTS_LIVE_PROVIDER=%q: a sandbox install must have no live provider", c["name"], v)
			}
			if env["PAYMENTS_ALLOW_MOCK_LIVE"] != "false" {
				t.Errorf("%v has PAYMENTS_ALLOW_MOCK_LIVE=%q: a sandbox install must not allow live to reach the mock", c["name"], env["PAYMENTS_ALLOW_MOCK_LIVE"])
			}
		}
		for k, v := range env {
			if strings.Contains(strings.ToLower(k), "psp") && !strings.Contains(strings.ToLower(v), "mockpsp") && strings.HasPrefix(v, "http") {
				t.Errorf("%v: %s=%s points at something that is not mock-psp", c["name"], k, v)
			}
		}
	}
	if sawEnvs < 2 || sawPayments < 1 {
		t.Fatalf("saw PAYRUDDER_ALLOWED_ENVIRONMENTS %d times and PAYMENTS_LIVE_PROVIDER %d times: the profile is not being checked", sawEnvs, sawPayments)
	}
	if find(docs, "Deployment", "mockpsp") == nil {
		t.Error("the sandbox profile needs mock-psp")
	}
	// A default install, for contrast, does have mock-psp as the live provider.
	for _, c := range containers(render(t, "payrudder")) {
		if v, ok := envOf(c)["PAYMENTS_LIVE_PROVIDER"]; ok && v != "mockpsp" {
			t.Errorf("default install: PAYMENTS_LIVE_PROVIDER=%q, want mockpsp", v)
		} else if ok && envOf(c)["PAYMENTS_ALLOW_MOCK_LIVE"] != "true" {
			// payments-worker refuses to start with the mock as the live provider unless the install says so (ADR 0023).
			t.Errorf("default install: PAYMENTS_ALLOW_MOCK_LIVE=%q, want true next to the mock live provider", envOf(c)["PAYMENTS_ALLOW_MOCK_LIVE"])
		}
	}
}

func TestExternalS3RendersNoMinIO(t *testing.T) {
	infra := render(t, "payrudder-infra", "--set", "minio.enabled=false")
	for _, d := range infra {
		if strings.Contains(d.name(), "minio") {
			t.Errorf("minio.enabled=false still renders %s/%s", d.kind(), d.name())
		}
	}
	app := render(t, "payrudder", "--set", "s3.endpoint=https://s3.eu-west-1.amazonaws.com", "--set", "s3.bootstrap.enabled=false", "--set", "s3.forcePathStyle=false")
	for _, d := range app {
		if strings.Contains(d.name(), "minio") || strings.Contains(d.name(), "s3-bootstrap") {
			t.Errorf("external S3 renders %s/%s", d.kind(), d.name())
		}
	}
	saw := false
	for _, c := range containers(app) {
		if v, ok := envOf(c)["S3_ENDPOINT"]; ok {
			saw = true
			if v != "https://s3.eu-west-1.amazonaws.com" {
				t.Errorf("%v S3_ENDPOINT = %q", c["name"], v)
			}
		}
	}
	if !saw {
		t.Fatal("no container has S3_ENDPOINT")
	}
	// And the default install of the infra chart does contain it.
	if find(render(t, "payrudder-infra"), "StatefulSet", "minio") == nil {
		t.Error("the default infra install has no MinIO")
	}
}

func TestNetworkPolicies(t *testing.T) {
	docs := render(t, "payrudder")
	if find(docs, "NetworkPolicy", "default-deny-ingress") == nil {
		t.Fatal("no default-deny ingress policy")
	}
	callers := func(policy string) []string {
		np := find(docs, "NetworkPolicy", policy)
		if np == nil {
			t.Fatalf("no policy %s", policy)
		}
		rules, _ := np.get("spec", "ingress").([]any)
		var out []string
		for _, r := range rules[:1] { // the first rule is the service port; the second is metrics
			from, _ := r.(map[string]any)["from"].([]any)
			for _, f := range from {
				out = append(out, obj(f.(map[string]any)).str("podSelector", "matchLabels", "app.kubernetes.io/name"))
			}
		}
		return out
	}
	if got := strings.Join(callers("allow-ingest-svc"), ","); got != "gateway,billing-svc,plans-svc,notifier" {
		// plans-svc is there for the sandbox reset (phase 45 E): it asks every service that keeps tenant data to empty a sandbox. The notifier meters the mails it
		// sends into the platform tenant (plan 11, ADR 0024).
		t.Errorf("ingest-svc accepts calls from %q, want gateway,billing-svc,plans-svc,notifier", got)
	}
	if got := strings.Join(callers("allow-limits-svc"), ","); got != "gateway,notifier" {
		t.Errorf("limits-svc accepts calls from %q, want gateway,notifier (the notifier asks a plan's mails allowance)", got)
	}
	if got := strings.Join(callers("allow-mockpsp"), ","); got != "payments-worker" {
		t.Errorf("mockpsp accepts calls from %q, want only payments-worker", got)
	}
	// The gateway is the only service whose port is open to everyone, and the only one with a NodePort in the default install.
	for _, d := range docs {
		if d.kind() == "NetworkPolicy" && d.name() != "default-deny-ingress" {
			rules, _ := d.get("spec", "ingress").([]any)
			first := rules[0].(map[string]any)
			if _, restricted := first["from"]; !restricted && d.name() != "allow-gateway" {
				t.Errorf("%s accepts calls from anyone", d.name())
			}
		}
		if d.kind() == "Service" && d.str("spec", "type") == "NodePort" && d.name() != "gateway" {
			t.Errorf("Service %s is a NodePort in the default install", d.name())
		}
	}
}

func TestMigrationsAreHooksAndTheirRoleStaysOutOfThePods(t *testing.T) {
	docs := render(t, "payrudder")
	migrating := 0
	for _, d := range docs {
		if d.kind() != "Job" || !strings.HasSuffix(d.name(), "-migrate") {
			continue
		}
		migrating++
		if !strings.Contains(d.str("metadata", "annotations", "helm.sh/hook"), "pre-upgrade") {
			t.Errorf("%s is not a pre-install/pre-upgrade hook", d.name())
		}
		c := containers([]obj{d})[0]
		env := envOf(c)
		if env["PAYRUDDER_MIGRATE_ONLY"] != "true" || env["PAYRUDDER_MIGRATE_ON_START"] != "true" {
			t.Errorf("%s does not run the migrations and exit: %v", d.name(), env)
		}
	}
	if migrating < 8 {
		t.Errorf("%d migration Jobs, want one per schema (8)", migrating)
	}
	for _, d := range docs {
		if d.kind() == "Secret" && strings.HasSuffix(d.name(), "-env") {
			if _, ok := d.get("stringData").(map[string]any)["PAYRUDDER_PG_MIGRATOR_DSN"]; ok {
				t.Errorf("runtime secret %s holds the migrator DSN", d.name())
			}
		}
		if d.kind() == "Deployment" {
			for _, c := range containers([]obj{d}) {
				if v := envOf(c)["PAYRUDDER_MIGRATE_ON_START"]; v != "false" {
					t.Errorf("Deployment %s migrates on start (%q): that is the Job's work", d.name(), v)
				}
			}
		}
	}
}

func TestEveryWorkloadHasProbesResourcesAndNonRoot(t *testing.T) {
	for _, profile := range []string{"values.yaml", "values-kind.yaml", "values-sandbox.yaml"} {
		for _, d := range render(t, "payrudder", "-f", values(t, "payrudder", profile)) {
			if d.kind() != "Deployment" {
				continue
			}
			c := containers([]obj{d})[0]
			for _, k := range []string{"livenessProbe", "readinessProbe", "resources", "securityContext"} {
				if c[k] == nil {
					t.Errorf("%s (%s): no %s", d.name(), profile, k)
				}
			}
			if d.get("spec", "template", "spec", "securityContext", "runAsNonRoot") != true {
				t.Errorf("%s (%s) may run as root", d.name(), profile)
			}
		}
	}
	// Replicated services have a PodDisruptionBudget.
	docs := render(t, "payrudder", "-f", values(t, "payrudder", "values-kind.yaml"))
	for _, name := range []string{"gateway", "ingest-svc", "aggregator", "limits-svc"} {
		if find(docs, "PodDisruptionBudget", name) == nil {
			t.Errorf("no PodDisruptionBudget for %s", name)
		}
	}
	for _, name := range []string{"gateway", "ingest-svc"} {
		if find(docs, "HorizontalPodAutoscaler", name) == nil {
			t.Errorf("no HPA for %s", name)
		}
	}
}

// The infra chart carries copies of files that live elsewhere in the repo (helm cannot read outside the chart); they must not drift.
func TestInfraFilesMatchTheirSources(t *testing.T) {
	root := repoRoot(t)
	for copyOf, src := range map[string]string{
		"deploy/helm/payrudder-infra/files/topics.sh":                "deploy/compose/redpanda/topics.sh",
		"deploy/helm/payrudder-infra/files/definitions.json":         "deploy/rabbitmq/definitions.json",
		"deploy/helm/payrudder-infra/files/low-memory.xml":           "deploy/clickhouse/low-memory.xml",
		"deploy/helm/payrudder-infra/files/payrudder-rules.yaml":     "deploy/prometheus/rules/payrudder.yaml",
		"deploy/helm/payrudder-infra/files/alertmanager.webhook.yml": "deploy/alertmanager/alertmanager.webhook.yml",
		"deploy/helm/payrudder-infra/files/object-backup.sh":         "deploy/backup/object-backup.sh",
		"deploy/helm/payrudder-infra/files/clickhouse-backup.sh":     "deploy/backup/clickhouse-backup.sh",
	} {
		a, err1 := os.ReadFile(filepath.Join(root, copyOf))
		b, err2 := os.ReadFile(filepath.Join(root, src))
		if err1 != nil || err2 != nil {
			t.Fatalf("%s / %s: %v %v", copyOf, src, err1, err2)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs from %s: copy it again (make helm-sync)", copyOf, src)
		}
	}
}

func TestRolePasswordsAreOverridable(t *testing.T) {
	docs := render(t, "payrudder", "--set", "postgres.passwords.plans_app=s3cr3t/with space")
	for _, d := range docs {
		if d.kind() == "Secret" && d.name() == "x-plans-svc-env" {
			dsn := fmt.Sprint(d.get("stringData").(map[string]any)["PAYRUDDER_PG_APP_DSN"])
			if !strings.Contains(dsn, "plans_app:s3cr3t%2Fwith+space@") {
				t.Fatalf("DSN = %s: the password is not URL-escaped", dsn)
			}
			return
		}
	}
	t.Fatal("no plans-svc secret")
}

// Provider webhooks and the operator endpoints for provider accounts reach payments-worker through the gateway only, and the two share a secret.
func TestPaymentProvidersAreWiredThroughTheGateway(t *testing.T) {
	docs := render(t, "payrudder")
	svc := find(docs, "Service", "payments-worker")
	if svc == nil {
		t.Fatal("payments-worker has no Service: the gateway cannot forward provider webhooks to it")
	}
	if ports, _ := svc.get("spec", "ports").([]any); len(ports) == 0 {
		t.Fatal("payments-worker's Service has no port")
	}
	np := find(docs, "NetworkPolicy", "allow-payments-worker")
	if np == nil {
		t.Fatal("no policy allow-payments-worker")
	}
	rules, _ := np.get("spec", "ingress").([]any)
	var from []string
	for _, f := range rules[0].(map[string]any)["from"].([]any) {
		from = append(from, obj(f.(map[string]any)).str("podSelector", "matchLabels", "app.kubernetes.io/name"))
	}
	if strings.Join(from, ",") != "gateway,plans-svc" {
		// plans-svc resets a sandbox's payments through it (phase 45 E), with the internal token.
		t.Errorf("payments-worker accepts calls from %v, want the gateway and plans-svc", from)
	}
	for _, callee := range []string{"allow-plans-svc", "allow-billing-svc"} {
		p := find(docs, "NetworkPolicy", callee)
		if p == nil {
			t.Fatalf("no policy %s", callee)
		}
		var names []string
		r, _ := p.get("spec", "ingress").([]any)
		for _, f := range r[0].(map[string]any)["from"].([]any) {
			names = append(names, obj(f.(map[string]any)).str("podSelector", "matchLabels", "app.kubernetes.io/name"))
		}
		if !strings.Contains(strings.Join(names, ","), "payments-worker") {
			t.Errorf("%s does not accept payments-worker (%v): it reads subscriptions and invoices from them", callee, names)
		}
	}
	secret := func(name string) map[string]any {
		s := find(docs, "Secret", "x-"+name+"-env")
		if s == nil {
			t.Fatalf("no secret for %s", name)
		}
		m, _ := s.get("stringData").(map[string]any)
		return m
	}
	worker, gateway := secret("payments-worker"), secret("gateway")
	for _, k := range []string{"PAYRUDDER_SECRETS_KEY", "PAYMENTS_INTERNAL_TOKEN"} {
		if worker[k] == nil || worker[k] == "" {
			t.Errorf("payments-worker's secret has no %s", k)
		}
	}
	if gateway["PAYRUDDER_PAYMENTS_INTERNAL_TOKEN"] == nil || gateway["PAYRUDDER_PAYMENTS_INTERNAL_TOKEN"] != worker["PAYMENTS_INTERNAL_TOKEN"] {
		t.Errorf("the gateway's token %v differs from payments-worker's %v", gateway["PAYRUDDER_PAYMENTS_INTERNAL_TOKEN"], worker["PAYMENTS_INTERNAL_TOKEN"])
	}
	// The sealing key and the internal token are never plain environment of a container.
	for _, c := range containers(docs) {
		for k := range envOf(c) {
			if k == "PAYRUDDER_SECRETS_KEY" || k == "PAYMENTS_INTERNAL_TOKEN" || k == "PAYRUDDER_PAYMENTS_INTERNAL_TOKEN" {
				t.Errorf("%s is a plain environment variable of a container; it belongs in the Secret", k)
			}
		}
	}
	if got := envOf(findContainer(t, docs, "gateway"))["PAYRUDDER_PAYMENTS_URL"]; got != "http://payments-worker:8095" {
		t.Errorf("gateway forwards to %q", got)
	}
}

func findContainer(t *testing.T, docs []obj, deployment string) obj {
	t.Helper()
	d := find(docs, "Deployment", deployment)
	if d == nil {
		t.Fatalf("no deployment %s", deployment)
	}
	cs, _ := d.get("spec", "template", "spec", "containers").([]any)
	if len(cs) == 0 {
		t.Fatalf("%s has no containers", deployment)
	}
	return obj(cs[0].(map[string]any))
}

// While the secrets key is replaced, payments-worker and plans-svc both get the key's id and the key it replaces with its id, in their Secrets and not in plain
// environment; a chart that is not rotating renders none of the three (docs/runbooks/secrets-key-rotation.md).
func TestSecretsKeyRotationValuesReachPaymentsWorkerAndPlansSvc(t *testing.T) {
	rotation := []string{"PAYRUDDER_SECRETS_KEY_ID", "PAYRUDDER_SECRETS_KEY_PREVIOUS", "PAYRUDDER_SECRETS_KEY_PREVIOUS_ID"}
	secret := func(docs []obj, name string) map[string]any {
		s := find(docs, "Secret", "x-"+name+"-env")
		if s == nil {
			t.Fatalf("no secret for %s", name)
		}
		m, _ := s.get("stringData").(map[string]any)
		return m
	}
	for _, name := range []string{"payments-worker", "plans-svc"} {
		for _, k := range rotation {
			if v, ok := secret(render(t, "payrudder"), name)[k]; ok {
				t.Errorf("%s has %s=%v without a rotation", name, k, v)
			}
		}
	}
	docs := render(t, "payrudder", "--set", "payments.secretsKeyId=2", "--set", "payments.secretsKeyPrevious=b2xkb2xkb2xkb2xkb2xkb2xkb2xkb2xkb2xkb2xkMDA=", "--set", "payments.secretsKeyPreviousId=1")
	for _, name := range []string{"payments-worker", "plans-svc"} {
		got := secret(docs, name)
		if got["PAYRUDDER_SECRETS_KEY_ID"] != "2" || got["PAYRUDDER_SECRETS_KEY_PREVIOUS"] != "b2xkb2xkb2xkb2xkb2xkb2xkb2xkb2xkb2xkb2xkMDA=" || got["PAYRUDDER_SECRETS_KEY_PREVIOUS_ID"] != "1" {
			t.Errorf("%s's secret = %v: the rotation values did not arrive", name, got)
		}
	}
	for _, c := range containers(docs) {
		for _, k := range rotation {
			if _, ok := envOf(c)[k]; ok {
				t.Errorf("%s is a plain environment variable of a container; it belongs in the Secret", k)
			}
		}
	}
}

// The error tracking DSN holds a key: it reaches every service through its Secret and no container's plain environment, and a chart without it renders nothing, so
// error tracking stays off (docs/08-observability.md).
func TestErrorTrackingURLReachesEveryServiceThroughItsSecret(t *testing.T) {
	const dsn = "https://publickey@errors.example.com/7"
	secrets := func(docs []obj) []obj {
		var out []obj
		for _, d := range docs {
			if d.kind() == "Secret" && strings.HasSuffix(d.name(), "-env") {
				out = append(out, d)
			}
		}
		return out
	}
	without := secrets(render(t, "payrudder"))
	if len(without) == 0 {
		t.Fatal("no service Secret rendered")
	}
	for _, s := range without {
		if m, _ := s.get("stringData").(map[string]any); m["PAYRUDDER_ERROR_TRACKING_URL"] != nil {
			t.Errorf("%s has an error tracking URL that nobody set", s.name())
		}
	}
	docs := render(t, "payrudder", "--set", "errorTracking.url="+dsn)
	with := secrets(docs)
	if len(with) < len(without) {
		t.Fatalf("%d service Secrets with the URL, %d without: some service lost its Secret", len(with), len(without))
	}
	for _, s := range with {
		m, _ := s.get("stringData").(map[string]any)
		if m["PAYRUDDER_ERROR_TRACKING_URL"] != dsn {
			t.Errorf("%s: PAYRUDDER_ERROR_TRACKING_URL = %v, want the DSN", s.name(), m["PAYRUDDER_ERROR_TRACKING_URL"])
		}
	}
	for _, c := range containers(docs) {
		if _, ok := envOf(c)["PAYRUDDER_ERROR_TRACKING_URL"]; ok {
			t.Errorf("the DSN is a plain environment variable of container %s; it belongs in the Secret", c.str("name"))
		}
	}
	for _, d := range docs {
		if d.kind() == "Secret" && strings.HasSuffix(d.name(), "-migrate") {
			if m, _ := d.get("stringData").(map[string]any); m["PAYRUDDER_ERROR_TRACKING_URL"] != nil {
				t.Errorf("the migration Job's Secret %s holds the DSN; it does not need it", d.name())
			}
		}
	}
}

// The notifier's signed listener is what the console's mail page talks to (through the gateway): without it the page says "not connected" (plan 11). Both ends get the
// same secret through their Secrets, the gateway the notifier's address, the notifier an address to listen on, the port is open to the gateway only, and an empty
// secret switches the whole thing off at both ends (the services refuse one end without the other).
func TestMailEventsListenerIsWiredBetweenTheGatewayAndTheNotifier(t *testing.T) {
	secret := func(docs []obj, name string) map[string]any {
		s := find(docs, "Secret", "x-"+name+"-env")
		if s == nil {
			t.Fatalf("no secret for %s", name)
		}
		m, _ := s.get("stringData").(map[string]any)
		return m
	}
	docs := render(t, "payrudder")
	notifier, gateway := secret(docs, "notifier"), secret(docs, "gateway")
	key := notifier["NOTIFIER_MAIL_EVENTS_SECRET"]
	if s, _ := key.(string); len(s) < 16 {
		t.Fatalf("the notifier's secret = %v: the listener refuses to start with fewer than 16 characters", key)
	}
	if gateway["PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET"] != key {
		t.Errorf("the gateway signs with %v, the notifier checks %v", gateway["PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET"], key)
	}
	if got := envOf(findContainer(t, docs, "notifier"))["NOTIFIER_EVENTS_ADDR"]; got != ":9111" {
		t.Errorf("the notifier listens on %q, want :9111", got)
	}
	if got := envOf(findContainer(t, docs, "gateway"))["PAYRUDDER_NOTIFIER_EVENTS_URL"]; got != "http://notifier:9111" {
		t.Errorf("the gateway calls %q, want http://notifier:9111", got)
	}
	svc := find(docs, "Service", "notifier")
	if svc == nil {
		t.Fatal("the notifier has no Service: the gateway cannot reach its listener")
	}
	found := false
	for _, p := range svc.get("spec", "ports").([]any) {
		if pm, _ := p.(map[string]any); pm["port"] == 9111 {
			found = true
		}
	}
	if !found {
		t.Errorf("the notifier's Service has no port 9111: %v", svc.get("spec", "ports"))
	}
	np := find(docs, "NetworkPolicy", "allow-notifier")
	if np == nil {
		t.Fatal("no policy allow-notifier")
	}
	rules, _ := np.get("spec", "ingress").([]any)
	var from []string
	for _, f := range rules[0].(map[string]any)["from"].([]any) {
		from = append(from, obj(f.(map[string]any)).str("podSelector", "matchLabels", "app.kubernetes.io/name"))
	}
	if strings.Join(from, ",") != "gateway" {
		t.Errorf("the notifier's listener accepts calls from %v, want the gateway only: it can suppress a tenant's mail", from)
	}
	// The secret is never plain environment of a container.
	for _, c := range containers(docs) {
		for k := range envOf(c) {
			if k == "NOTIFIER_MAIL_EVENTS_SECRET" || k == "PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET" {
				t.Errorf("%s is a plain environment variable of container %s; it belongs in the Secret", k, c.str("name"))
			}
		}
	}

	off := render(t, "payrudder", "--set", "mail.eventsSecret=")
	if got := envOf(findContainer(t, off, "notifier"))["NOTIFIER_EVENTS_ADDR"]; got != "" {
		t.Errorf("with no secret the notifier still listens on %q", got)
	}
	if got := envOf(findContainer(t, off, "gateway"))["PAYRUDDER_NOTIFIER_EVENTS_URL"]; got != "" {
		t.Errorf("with no secret the gateway still calls %q", got)
	}
	for _, name := range []string{"notifier", "gateway"} {
		for k := range secret(off, name) {
			if strings.Contains(k, "MAIL_EVENTS_SECRET") {
				t.Errorf("with no secret %s's Secret still holds %s", name, k)
			}
		}
	}
}

// plans-svc refuses to start outside a development environment without a pepper of its own (security review): the chart ships none, hands the public local value to a
// development environment (local, ci, kind) only, and refuses to render a production profile without one.
func TestOTPPepperIsTheOperatorsOutsideDevelopment(t *testing.T) {
	const public, own = "local-otp-pepper-change-me-0123456789", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	url := []string{"--set", "global.publicURL=" + publicURL} // the other thing a non-development environment must set (TestPublicURLIsRequiredOutsideDevelopment)
	pepper := func(docs []obj) string {
		s := find(docs, "Secret", "x-plans-svc-env")
		if s == nil {
			t.Fatal("no Secret for plans-svc")
		}
		m, _ := s.get("stringData").(map[string]any)
		v, _ := m["PAYRUDDER_OTP_PEPPER"].(string)
		return v
	}
	if v := pepper(render(t, "payrudder")); v != public {
		t.Errorf("the default (kind) environment gets %q, want the local value", v)
	}
	for _, e := range []string{"local", "ci", "kind"} {
		if v := pepper(render(t, "payrudder", "--set", "global.environment="+e)); v != public {
			t.Errorf("%s gets %q, want the local value", e, v)
		}
	}
	if v := pepper(render(t, "payrudder", append(url, "--set", "global.environment=production", "--set", "auth.otpPepper="+own)...)); v != own {
		t.Errorf("production with a pepper of its own gets %q", v)
	}
	if v := pepper(render(t, "payrudder", append(url, "-f", values(t, "payrudder", "values-small.yaml"), "--set", "auth.otpPepper="+own)...)); v != own {
		t.Errorf("values-small with a pepper of its own gets %q", v)
	}
	// The pepper is never plain environment of a container.
	for _, c := range containers(render(t, "payrudder", append(url, "--set", "global.environment=production", "--set", "auth.otpPepper="+own)...)) {
		if _, ok := envOf(c)["PAYRUDDER_OTP_PEPPER"]; ok {
			t.Errorf("the pepper is a plain environment variable of container %s", c.str("name"))
		}
	}
	// A production environment without one does not render (existing Secrets are the operator's own: nothing is rendered for them).
	for name, args := range map[string][]string{
		"production":   append(url, "--set", "global.environment=production"),
		"values-small": append(url, "-f", values(t, "payrudder", "values-small.yaml")),
		"railway":      append(url, "--set", "global.environment=railway"),
	} {
		if out, err := tryRender("payrudder", args...); err == nil || !strings.Contains(out, "auth.otpPepper is required") {
			t.Errorf("%s without a pepper rendered or failed for another reason: %v\n%s", name, err, out)
		}
	}
	if out, err := tryRender("payrudder", append(url, "--set", "global.environment=production", "--set", "secrets.existingSecrets=true")...); err != nil {
		t.Errorf("production with existingSecrets must render (the Secrets hold the pepper): %v\n%s", err, out)
	}
}

// With secrets.existingSecrets the pepper is in the operator's own Secret, but the migration Job's hook Secret is always rendered from the values and holds none. The Job
// runs in a non-development environment, so it must say it migrates and exits: plans-svc then skips the pepper check (TestMigrateOnlyRunNeedsNoPepper in plans-svc), and
// does not crash-loop to the Job's deadline. A serving pod, on the other hand, gets the operator's Secret and is checked.
func TestExistingSecretsInstallMigratesWithoutThePepper(t *testing.T) {
	docs := render(t, "payrudder", "--set", "global.environment=production", "--set", "global.publicURL="+publicURL, "--set", "secrets.existingSecrets=true")
	job := find(docs, "Job", "x-plans-svc-migrate")
	if job == nil {
		t.Fatal("no migration Job for plans-svc")
	}
	env := envOf(containers([]obj{job})[0])
	if env["PAYRUDDER_ENV"] != "production" || env["PAYRUDDER_MIGRATE_ONLY"] != "true" {
		t.Errorf("the plans-svc migration Job runs with %v: it must be a non-development environment that migrates and exits", env)
	}
	hook := find(docs, "Secret", "x-plans-svc-migrate")
	if hook == nil {
		t.Fatal("no hook Secret for the plans-svc migration Job")
	}
	if v, _ := hook.get("stringData").(map[string]any)["PAYRUDDER_OTP_PEPPER"].(string); v != "" {
		t.Errorf("the hook Secret holds a pepper (%q): with existingSecrets it is the operator's, and the Job needs none", v)
	}
	if find(docs, "Secret", "x-plans-svc-env") != nil {
		t.Error("existingSecrets still renders the runtime Secret of plans-svc")
	}
	if v := envOf(findContainer(t, docs, "plans-svc"))["PAYRUDDER_MIGRATE_ONLY"]; v == "true" {
		t.Error("the plans-svc Deployment is migrate-only: it would exit without serving, and its pepper would never be checked")
	}
}

// publicURL is the address customers reach the gateway at.
const publicURL = "https://api.example.com"

// The link in the mail that asks a customer to confirm a payment (3-D Secure) and the hosted invoice links start with global.publicURL; billing-svc sends the mail without a
// link, and says nothing, when it is empty. So a chart outside a development environment (local, ci, kind) does not render without one, and the value reaches the gateway
// and billing-svc.
func TestPublicURLIsRequiredOutsideDevelopment(t *testing.T) {
	const pepper = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	urlOf := func(docs []obj, service string) string {
		return envOf(findContainer(t, docs, service))["PAYRUDDER_PUBLIC_URL"]
	}
	for _, e := range []string{"local", "ci", "kind"} {
		docs := render(t, "payrudder", "--set", "global.environment="+e)
		for _, svc := range []string{"gateway", "billing-svc"} {
			if got := urlOf(docs, svc); got != "" {
				t.Errorf("%s: %s gets %q, want none (relative links)", e, svc, got)
			}
		}
	}
	for name, args := range map[string][]string{
		"production":   {"--set", "global.environment=production"},
		"values-small": {"-f", values(t, "payrudder", "values-small.yaml")},
		"railway":      {"--set", "global.environment=railway"},
	} {
		args = append(args, "--set", "auth.otpPepper="+pepper)
		if out, err := tryRender("payrudder", args...); err == nil || !strings.Contains(out, "global.publicURL is required") {
			t.Errorf("%s without a public URL rendered or failed for another reason: %v\n%s", name, err, out)
		}
		docs := render(t, "payrudder", append(args, "--set", "global.publicURL="+publicURL)...)
		for _, svc := range []string{"gateway", "billing-svc"} {
			if got := urlOf(docs, svc); got != publicURL {
				t.Errorf("%s: %s gets %q, want %q", name, svc, got, publicURL)
			}
		}
	}
}

// The provider's delivery webhook (bounces and complaints into the suppression list) is the gateway's PAYRUDDER_MAIL_WEBHOOK_SECRET: a secret, so it lives in the gateway's
// Secret and nowhere else, off when empty, and it needs the notifier's listener (the gateway refuses to start with the one and not the other).
func TestMailWebhookSecretReachesTheGatewaySecretOnly(t *testing.T) {
	const whsec = "whsec_dGhlLXN2aXgtc2VjcmV0LW9mLXRoZS10ZXN0cw=="
	holders := func(docs []obj) (out []string) {
		for _, d := range docs {
			if d.kind() == "Secret" {
				if _, ok := d.get("stringData").(map[string]any)["PAYRUDDER_MAIL_WEBHOOK_SECRET"]; ok {
					out = append(out, d.name())
				}
			}
		}
		return out
	}
	if got := holders(render(t, "payrudder")); len(got) != 0 {
		t.Errorf("by default %v hold a mail webhook secret", got)
	}
	docs := render(t, "payrudder", "--set", "mail.webhookSecret="+whsec)
	if got := holders(docs); len(got) != 1 || got[0] != "x-gateway-env" {
		t.Errorf("the secret is in %v, want the gateway's Secret only", got)
	}
	if v, _ := find(docs, "Secret", "x-gateway-env").get("stringData").(map[string]any)["PAYRUDDER_MAIL_WEBHOOK_SECRET"].(string); v != whsec {
		t.Errorf("the gateway's Secret holds %q, want the value given", v)
	}
	for _, c := range containers(docs) {
		if v, ok := envOf(c)["PAYRUDDER_MAIL_WEBHOOK_SECRET"]; ok {
			t.Errorf("the secret is plain environment (%q) of container %s; it belongs in the Secret", v, c.str("name"))
		}
	}
	if out, err := tryRender("payrudder", "--set", "mail.webhookSecret="+whsec, "--set", "mail.eventsSecret="); err == nil || !strings.Contains(out, "mail.webhookSecret needs mail.eventsSecret") {
		t.Errorf("a webhook secret without the notifier's listener rendered or failed for another reason: %v\n%s", err, out)
	}
	// Your own Secrets: nothing is rendered for them, so the value is not either.
	if got := holders(render(t, "payrudder", "--set", "mail.webhookSecret="+whsec, "--set", "secrets.existingSecrets=true")); len(got) != 0 {
		t.Errorf("with existingSecrets %v still hold the secret", got)
	}
}

// tryRender is render that hands the failure back.
func tryRender(chart string, args ...string) (string, error) {
	wd, _ := os.Getwd()
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	cmd := exec.Command(filepath.Join(root, "bin", "helm"), append([]string{"template", "x", filepath.Join(root, "deploy", "helm", chart), "--namespace", "payrudder"}, args...)...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}
