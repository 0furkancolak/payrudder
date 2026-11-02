package helm

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// dockerOrSkip skips a test that runs promtool or amtool in their images when there is no Docker (the chart tests themselves need only helm).
func dockerOrSkip(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker is not available")
	}
}

func alertNames(t *testing.T, groups any) []string {
	t.Helper()
	var out []string
	gs, _ := groups.([]any)
	for _, g := range gs {
		rules, _ := g.(map[string]any)["rules"].([]any)
		for _, r := range rules {
			if n, ok := r.(map[string]any)["alert"].(string); ok {
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Nothing of the monitoring or the object backup is installed unless asked for.
func TestMonitoringAndObjectBackupAreOffByDefault(t *testing.T) {
	for _, d := range render(t, "payrudder-infra") {
		switch {
		case d.kind() == "PrometheusRule", d.name() == "payrudder-alert-rules", d.name() == "alertmanager-payrudder", d.name() == "object-backup":
			t.Errorf("the default install renders %s %s", d.kind(), d.name())
		}
	}
}

// The chart's rules are the repository's: a PrometheusRule (and, if asked, a ConfigMap) with every alert, and promtool, run on what the chart rendered, accepts the rules and
// every unit test of deploy/prometheus/tests passes against them.
func TestPrometheusRuleIsTheRepositorysRulesAndPassesPromtool(t *testing.T) {
	root := repoRoot(t)
	docs := render(t, "payrudder-infra", "--set", "monitoring.rules.enabled=true", "--set", "monitoring.rules.configMap=true", "--set", "monitoring.rules.ruleLabels.release=kube-prometheus-stack")
	pr := find(docs, "PrometheusRule", "payrudder-alerts")
	if pr == nil {
		t.Fatal("no PrometheusRule")
	}
	if pr.str("metadata", "labels", "release") != "kube-prometheus-stack" {
		t.Errorf("the rule labels did not arrive: %v", pr.get("metadata", "labels"))
	}
	var source map[string]any
	raw, err := os.ReadFile(filepath.Join(root, "deploy", "prometheus", "rules", "payrudder.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	got, want := alertNames(t, pr.get("spec", "groups")), alertNames(t, source["groups"])
	if len(want) < 30 || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the PrometheusRule has the alerts %v, the repository's rules have %v", got, want)
	}
	cm := find(docs, "ConfigMap", "payrudder-alert-rules")
	if cm == nil || strings.TrimSpace(cm.str("data", "payrudder.yaml")) != strings.TrimSpace(string(raw)) {
		t.Fatal("the rules ConfigMap is not the repository's rules file")
	}

	dockerOrSkip(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	rendered, err := yaml.Marshal(map[string]any{"groups": pr.get("spec", "groups")})
	if err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile(filepath.Join(root, "deploy", "prometheus", "tests", "payrudder_test.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string][]byte{filepath.Join(dir, "rules", "payrudder.yaml"): rendered, filepath.Join(dir, "tests", "payrudder_test.yaml"): unit} {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"check", "rules", "/p/rules/payrudder.yaml"}, {"test", "rules", "/p/tests/payrudder_test.yaml"}} {
		cmd := exec.Command("docker", append([]string{"run", "--rm", "-v", dir + ":/p:ro", "--entrypoint", "promtool", "prom/prometheus:v3.15.0"}, args...)...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Errorf("promtool %s on the rendered rules: %v\n%s", strings.Join(args, " "), err, out.String())
		}
	}
}

// The Alertmanager Secret holds the route of the repository with the URL files under the mount of the Secret the operator creates, and amtool accepts it and routes a page,
// a warning and the Watchdog as documented.
func TestAlertmanagerSecretRoutesAsDocumented(t *testing.T) {
	docs := render(t, "payrudder-infra", "--set", "monitoring.alertmanager.enabled=true", "--set", "monitoring.alertmanager.urlsSecretName=my-urls")
	s := find(docs, "Secret", "alertmanager-payrudder")
	if s == nil {
		t.Fatal("no Alertmanager Secret")
	}
	cfg := s.str("stringData", "alertmanager.yaml")
	for _, want := range []string{"url_file: /etc/alertmanager/secrets/my-urls/webhook_url", "url_file: /etc/alertmanager/secrets/my-urls/watchdog_url", "alertname=\"Watchdog\"", "inhibit_rules:"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("the rendered config lacks %q", want)
		}
	}
	if strings.Contains(cfg, "url_file: /etc/alertmanager/webhook_url") {
		t.Error("a URL file kept the path of the compose stack")
	}
	dockerOrSkip(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alertmanager.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	amtool := func(args ...string) string {
		cmd := exec.Command("docker", append([]string{"run", "--rm", "-v", dir + ":/a:ro", "--entrypoint", "amtool", "prom/alertmanager:v0.28.1"}, args...)...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("amtool %s: %v\n%s", strings.Join(args, " "), err, out.String())
		}
		return strings.TrimSpace(out.String())
	}
	if out := amtool("check-config", "/a/alertmanager.yaml"); !strings.Contains(out, "SUCCESS") {
		t.Errorf("check-config: %s", out)
	}
	for receiver, labels := range map[string][]string{
		"on-call":  {"severity=page", "service=postgres", "alertname=BackupStale"},
		"team":     {"severity=warn", "service=postgres", "alertname=RestoreNotVerified"},
		"watchdog": {"severity=none", "alertname=Watchdog"},
	} {
		if got := amtool(append([]string{"config", "routes", "test", "--config.file=/a/alertmanager.yaml"}, labels...)...); got != receiver {
			t.Errorf("%v goes to %q, want %q", labels, got, receiver)
		}
	}
}

// The object backup is a CronJob that runs the repository's script as a non-root user with a read-only filesystem, reads the root user of this chart's MinIO unless told
// otherwise, and takes another endpoint and its credentials from a Secret.
func TestObjectBackupCronJob(t *testing.T) {
	root := repoRoot(t)
	docs := render(t, "payrudder-infra", "--set", "backup.objects.enabled=true")
	cj := find(docs, "CronJob", "object-backup")
	if cj == nil {
		t.Fatal("no CronJob")
	}
	script, err := os.ReadFile(filepath.Join(root, "deploy", "backup", "object-backup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if cm := find(docs, "ConfigMap", "object-backup"); cm == nil || cm.str("data", "object-backup.sh") != string(script) {
		t.Error("the ConfigMap is not the repository's script")
	}
	c := containers([]obj{cj})
	spec := cj.get("spec", "jobTemplate", "spec", "template", "spec")
	cs, _ := spec.(map[string]any)["containers"].([]any)
	if len(cs) != 1 {
		t.Fatalf("containers: %v", cs)
	}
	_ = c
	first := obj(cs[0].(map[string]any))
	if first.get("securityContext", "readOnlyRootFilesystem") != true || first.get("securityContext", "allowPrivilegeEscalation") != false || first["resources"] == nil {
		t.Errorf("security context or resources missing: %v", first)
	}
	if spec.(map[string]any)["securityContext"].(map[string]any)["runAsNonRoot"] != true {
		t.Error("the job may run as root")
	}
	env := map[string]any{}
	for _, e := range first["env"].([]any) {
		m := e.(map[string]any)
		env[m["name"].(string)] = m
	}
	if env["OBJ_SOURCE_URL"].(map[string]any)["value"] != "http://minio:9000" || env["OBJ_BUCKETS"].(map[string]any)["value"] != "invoices assets" {
		t.Errorf("defaults: %v", env)
	}
	ref := env["OBJ_SOURCE_USER"].(map[string]any)["valueFrom"].(map[string]any)["secretKeyRef"].(map[string]any)
	if ref["name"] != "minio" || ref["key"] != "MINIO_ROOT_USER" {
		t.Errorf("the source user comes from %v", ref)
	}
	if _, ok := env["OBJ_TARGET_URL"]; ok {
		t.Error("a target URL nobody set")
	}

	docs = render(t, "payrudder-infra", "--set", "backup.objects.enabled=true", "--set", "backup.objects.targetUrl=https://s3.other.example", "--set", "backup.objects.targetSecretName=offsite",
		"--set", "backup.objects.sourceSecretName=ours", "--set", "backup.objects.pushgatewayHostPort=pushgateway:9091")
	first = obj(find(docs, "CronJob", "object-backup").get("spec", "jobTemplate", "spec", "template", "spec").(map[string]any)["containers"].([]any)[0].(map[string]any))
	env = map[string]any{}
	for _, e := range first["env"].([]any) {
		m := e.(map[string]any)
		env[m["name"].(string)] = m
	}
	if env["OBJ_TARGET_URL"].(map[string]any)["value"] != "https://s3.other.example" || env["PUSHGATEWAY_HOST_PORT"].(map[string]any)["value"] != "pushgateway:9091" {
		t.Errorf("target: %v", env)
	}
	if k := env["OBJ_TARGET_PASSWORD"].(map[string]any)["valueFrom"].(map[string]any)["secretKeyRef"].(map[string]any); k["name"] != "offsite" || k["key"] != "OBJ_TARGET_PASSWORD" {
		t.Errorf("the target password comes from %v", k)
	}
	if k := env["OBJ_SOURCE_PASSWORD"].(map[string]any)["valueFrom"].(map[string]any)["secretKeyRef"].(map[string]any); k["name"] != "ours" || k["key"] != "OBJ_SOURCE_PASSWORD" {
		t.Errorf("the source password comes from %v", k)
	}
}
