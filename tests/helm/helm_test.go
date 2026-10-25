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
	if got := strings.Join(callers("allow-ingest-svc"), ","); got != "gateway,billing-svc,plans-svc" {
		// plans-svc is there for the sandbox reset (phase 45 E): it asks every service that keeps tenant data to empty a sandbox.
		t.Errorf("ingest-svc accepts calls from %q, want gateway,billing-svc,plans-svc", got)
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
		"deploy/helm/payrudder-infra/files/topics.sh":        "deploy/compose/redpanda/topics.sh",
		"deploy/helm/payrudder-infra/files/definitions.json": "deploy/rabbitmq/definitions.json",
		"deploy/helm/payrudder-infra/files/low-memory.xml":   "deploy/clickhouse/low-memory.xml",
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
