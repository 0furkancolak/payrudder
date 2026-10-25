//go:build chaos

package chaos

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Monkey injects the faults of docs/09-testing-strategy.md#chaos-schedule while the workload runs:
//   - every 10-30 s, SIGKILL one random replica of ingest-svc, aggregator, limits-svc or billing-svc;
//   - every 60 s, a network fault of 5-15 s (Toxiproxy latency or resets on compose, a deny-all NetworkPolicy on Kubernetes);
//   - once, a Redpanda broker stopped for 30 s (deleted, on Kubernetes);
//   - once, Redis flushed and restarted (all limit counters lost; the reconcile loop must rebuild them).
//
// Everything it does is counted, because a green run without real chaos proves nothing. The platform it acts on is
// QUOTIFY_CHAOS_PLATFORM: "compose" (default) or "kubernetes".
type Monkey struct {
	rng *rand.Rand
	p   platform

	mu     sync.Mutex
	Report MonkeyReport

	wg     sync.WaitGroup
	cancel context.CancelFunc
	// boost shortens the pause between faults; Meet sets it when the workload ended before the required amount of chaos was injected.
	boost atomic.Bool
}

// MonkeyReport is what the monkey actually did.
type MonkeyReport struct {
	Platform      string         `json:"platform"`
	Kills         int            `json:"kills"`
	KillsByTarget map[string]int `json:"kills_by_target"`
	Faults        int            `json:"network_faults"`
	FaultsByKind  map[string]int `json:"network_faults_by_kind"`
	BrokerStops   int            `json:"broker_stops"`
	RedisRestarts int            `json:"redis_restarts"`
	Errors        []string       `json:"errors,omitempty"`
}

var killTargets = []string{"ingest-svc", "aggregator", "limits-svc", "billing-svc"}

// platform is where the faults are injected.
type platform interface {
	name() string
	// instances lists the containers or pods of a workload.
	instances(workload string) []string
	// kill ends an instance abruptly; revive brings it back where the platform does not (docker does not restart a
	// container killed through its API; Kubernetes does).
	kill(instance string) error
	revive(instance string)
	// stopBroker takes a Redpanda broker away and returns what brings it back.
	stopBroker() (back func(), err error)
	wipeRedis() error
	// fault starts a network fault and returns its description and how to end it.
	fault(pick func(n int) int) (kind string, undo func(), err error)
	// restart abruptly restarts a stateful workload (RabbitMQ) in the middle of something.
	restartAll(workload string) error
	healAll()
}

func newPlatform() platform {
	if envOr("QUOTIFY_CHAOS_PLATFORM", "compose") == "kubernetes" {
		return &k8sPlatform{ns: envOr("QUOTIFY_K8S_NAMESPACE", "quotify"), ctx: envOr("QUOTIFY_K8S_CONTEXT", "kind-quotify")}
	}
	return &composePlatform{project: "quotify", toxi: toxiproxyURL}
}

// StartMonkey begins injecting faults. Call Stop to end them and wait for the stack to recover. total is the expected
// duration of the workload, used to place the one-off faults inside it.
func StartMonkey(parent context.Context, seed uint64, total time.Duration) *Monkey {
	ctx, cancel := context.WithCancel(parent)
	p := newPlatform()
	m := &Monkey{
		rng: rand.New(rand.NewPCG(seed^0x6d6f6e6b, seed)), //nolint:gosec // fault schedule, not security
		p:   p, cancel: cancel,
		Report: MonkeyReport{Platform: p.name(), KillsByTarget: map[string]int{}, FaultsByKind: map[string]int{}},
	}
	m.wg.Add(3)
	go func() { defer m.wg.Done(); m.killLoop(ctx) }()
	go func() { defer m.wg.Done(); m.faultLoop(ctx) }()
	go func() { defer m.wg.Done(); m.oneOffs(ctx, total) }()
	return m
}

// Meet keeps the faults coming, at a faster pace, until at least minKills containers were killed and minFaults network
// faults injected, or the budget is spent. The workload can finish sooner than the schedule assumes (a fast runner), and the
// chaos requirement must not depend on how fast the stack is: the backlog is still being processed, so the faults still land
// on a working system. It returns whether both minimums were reached.
func (m *Monkey) Meet(minKills, minFaults int, budget time.Duration) bool {
	met := func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.Report.Kills >= minKills && m.Report.Faults >= minFaults
	}
	if met() {
		return true
	}
	m.boost.Store(true)
	defer m.boost.Store(false)
	for deadline := time.Now().Add(budget); time.Now().Before(deadline); time.Sleep(time.Second) {
		if met() {
			return true
		}
	}
	return met()
}

// Stop ends the faults, heals the network and brings back whatever is still down.
func (m *Monkey) Stop() MonkeyReport {
	m.cancel()
	m.wg.Wait()
	m.p.healAll()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Report
}

func (m *Monkey) pick(n int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rng.IntN(n)
}

func (m *Monkey) record(f func(r *MonkeyReport)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f(&m.Report)
}

func (m *Monkey) fail(err error) {
	m.record(func(r *MonkeyReport) { r.Errors = append(r.Errors, err.Error()) })
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput() //nolint:gosec // test tool: args are built from fixed workload names
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (m *Monkey) killLoop(ctx context.Context) {
	for {
		pause := time.Duration(10+m.pick(21)) * time.Second
		if m.boost.Load() {
			pause = time.Duration(4+m.pick(5)) * time.Second
		}
		if !sleepCtx(ctx, pause) {
			return
		}
		svc := killTargets[m.pick(len(killTargets))]
		cs := m.p.instances(svc)
		if len(cs) == 0 {
			continue
		}
		victim := cs[m.pick(len(cs))]
		if err := m.p.kill(victim); err != nil {
			m.fail(err)
			continue
		}
		m.record(func(r *MonkeyReport) { r.Kills++; r.KillsByTarget[svc]++ })
		go func() {
			time.Sleep(time.Duration(2+m.pick(4)) * time.Second)
			m.p.revive(victim)
		}()
	}
}

func (m *Monkey) faultLoop(ctx context.Context) {
	for {
		pause := 60 * time.Second
		if m.boost.Load() {
			pause = 10 * time.Second
		}
		if !sleepCtx(ctx, pause) {
			return
		}
		kind, undo, err := m.p.fault(m.pick)
		if err != nil {
			m.fail(err)
			continue
		}
		m.record(func(r *MonkeyReport) { r.Faults++; r.FaultsByKind[kind]++ })
		sleepCtx(ctx, time.Duration(5+m.pick(11))*time.Second)
		undo()
	}
}

func (m *Monkey) oneOffs(ctx context.Context, total time.Duration) {
	// Both land in the first half of the expected run so the stack has time to recover while events still flow.
	at := func() time.Duration { return total/10 + time.Duration(m.pick(int(total/3/time.Second)+1))*time.Second }
	brokerAt, redisAt := at(), at()
	start := time.Now()
	done := map[string]bool{}
	for !done["broker"] || !done["redis"] {
		if !sleepCtx(ctx, time.Second) {
			return
		}
		el := time.Since(start)
		if !done["broker"] && el >= brokerAt {
			done["broker"] = true
			if back, err := m.p.stopBroker(); err != nil {
				m.fail(err)
			} else {
				m.record(func(r *MonkeyReport) { r.BrokerStops++ })
				sleepCtx(context.WithoutCancel(ctx), 30*time.Second)
				back()
			}
		}
		if !done["redis"] && el >= redisAt {
			done["redis"] = true
			if err := m.p.wipeRedis(); err != nil {
				m.fail(err)
			} else {
				m.record(func(r *MonkeyReport) { r.RedisRestarts++ })
			}
		}
	}
}

// ---- docker compose ---------------------------------------------------------------------------------------------

type composePlatform struct {
	project string
	toxi    string
}

func (c *composePlatform) name() string { return "compose" }

func (c *composePlatform) listAll(service string, all bool) []string {
	args := []string{"ps", "--filter", "label=com.docker.compose.project=" + c.project, "--filter", "label=com.docker.compose.service=" + service, "--format", "{{.Names}}"}
	if all {
		args = append(args, "-a")
	}
	out, err := exec.Command("docker", args...).Output() //nolint:gosec // test tool: args are built from fixed service names
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

func (c *composePlatform) instances(w string) []string { return c.listAll(w, false) }

func (c *composePlatform) kill(i string) error {
	_, err := run("docker", "kill", "-s", "KILL", i)
	return err
}

// docker does not restart a container killed through its API: play the orchestrator, as the kubelet does for a crashed pod.
func (c *composePlatform) revive(i string) { _, _ = run("docker", "start", i) }

func (c *composePlatform) stopBroker() (func(), error) {
	cs := c.instances("redpanda")
	if len(cs) == 0 {
		return nil, fmt.Errorf("no redpanda container")
	}
	if _, err := run("docker", "stop", "-t", "1", cs[0]); err != nil {
		return nil, err
	}
	return func() { _, _ = run("docker", "start", cs[0]) }, nil
}

func (c *composePlatform) wipeRedis() error {
	for _, i := range c.instances("redis") {
		_, _ = run("docker", "exec", i, "redis-cli", "FLUSHALL")
		if _, err := run("docker", "restart", "-t", "1", i); err != nil {
			return err
		}
	}
	return nil
}

func (c *composePlatform) restartAll(w string) error {
	for _, i := range c.instances(w) {
		if _, err := run("docker", "restart", "-t", "1", i); err != nil {
			return err
		}
	}
	return nil
}

var toxiMenu = [][2]string{
	{"postgres", "latency"}, {"postgres", "reset"}, {"kafka", "latency"}, {"kafka", "reset"}, {"redis", "latency"}, {"redis", "reset"},
}

func (c *composePlatform) fault(pick func(int) int) (string, func(), error) {
	f := toxiMenu[pick(len(toxiMenu))]
	spec := map[string]any{"name": "chaos", "stream": "downstream", "toxicity": 1.0}
	if f[1] == "latency" {
		spec["type"], spec["attributes"] = "latency", map[string]any{"latency": 500, "jitter": 100}
	} else {
		// reset_peer with timeout 0 resets each connection as soon as data flows.
		spec["type"], spec["attributes"] = "reset_peer", map[string]any{"timeout": 0}
	}
	// Toxiproxy's API sits behind a port forward: a dropped connection (EOF) is retried. A 409 on a retry means the first attempt did land.
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = toxiCall(c.toxi, "POST", "/proxies/"+f[0]+"/toxics", spec); err == nil || (attempt > 0 && strings.HasSuffix(err.Error(), ": 409")) {
			err = nil
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return "", nil, err
	}
	return f[0] + "/" + f[1], func() { _ = toxiCall(c.toxi, "DELETE", "/proxies/"+f[0]+"/toxics/chaos", nil) }, nil
}

func (c *composePlatform) healAll() {
	for _, f := range toxiMenu {
		_ = toxiCall(c.toxi, "DELETE", "/proxies/"+f[0]+"/toxics/chaos", nil)
	}
	// Bring back whatever is still down: a killed container whose restart was cut short, a stopped broker.
	for _, svc := range append([]string{"redpanda", "redis"}, killTargets...) {
		for _, i := range c.listAll(svc, true) {
			_, _ = run("docker", "start", i)
		}
	}
}

// ---- Kubernetes -------------------------------------------------------------------------------------------------

type k8sPlatform struct {
	ns, ctx string
}

func (k *k8sPlatform) name() string { return "kubernetes" }

func (k *k8sPlatform) kubectl(args ...string) (string, error) {
	return run("kubectl", append([]string{"--context", k.ctx, "-n", k.ns}, args...)...)
}

func (k *k8sPlatform) instances(w string) []string {
	out, err := k.kubectl("get", "pods", "-l", "app.kubernetes.io/name="+w, "--field-selector=status.phase=Running", "-o", "name")
	if err != nil {
		return nil
	}
	var pods []string
	for _, p := range strings.Fields(out) {
		pods = append(pods, strings.TrimPrefix(p, "pod/"))
	}
	return pods
}

// kill deletes the pod with no grace period: the process gets SIGKILL, and the ReplicaSet creates its replacement.
func (k *k8sPlatform) kill(i string) error {
	_, err := k.kubectl("delete", "pod", i, "--grace-period=0", "--force", "--wait=false")
	return err
}

func (k *k8sPlatform) revive(string) {}

func (k *k8sPlatform) stopBroker() (func(), error) {
	pods := k.instances("redpanda")
	if len(pods) == 0 {
		return nil, fmt.Errorf("no redpanda pod")
	}
	// Deleting a StatefulSet pod stops that broker until its replacement is up; the data stays on the volume.
	if _, err := k.kubectl("delete", "pod", pods[len(pods)-1], "--grace-period=0", "--force", "--wait=false"); err != nil {
		return nil, err
	}
	return func() {}, nil
}

func (k *k8sPlatform) wipeRedis() error {
	pods := k.instances("redis")
	for _, p := range pods {
		_, _ = k.kubectl("exec", p, "--", "redis-cli", "FLUSHALL")
		if _, err := k.kubectl("delete", "pod", p, "--grace-period=0", "--force", "--wait=false"); err != nil {
			return err
		}
	}
	return nil
}

func (k *k8sPlatform) restartAll(w string) error {
	for _, p := range k.instances(w) {
		if _, err := k.kubectl("delete", "pod", p, "--grace-period=0", "--force", "--wait=false"); err != nil {
			return err
		}
	}
	return nil
}

const partitionPolicy = `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: chaos-partition-%s
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: %s
  policyTypes: [Ingress, Egress]
`

// fault partitions a service's pods from the network (no ingress, no egress) with a deny-all NetworkPolicy.
func (k *k8sPlatform) fault(pick func(int) int) (string, func(), error) {
	svc := killTargets[pick(len(killTargets))]
	cmd := exec.Command("kubectl", "--context", k.ctx, "-n", k.ns, "apply", "-f", "-") //nolint:gosec // test tool
	cmd.Stdin = strings.NewReader(fmt.Sprintf(partitionPolicy, svc, svc))
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", nil, fmt.Errorf("apply partition: %w: %s", err, out)
	}
	return "partition/" + svc, func() { _, _ = k.kubectl("delete", "networkpolicy", "chaos-partition-"+svc, "--ignore-not-found") }, nil
}

func (k *k8sPlatform) healAll() {
	for _, svc := range killTargets {
		_, _ = k.kubectl("delete", "networkpolicy", "chaos-partition-"+svc, "--ignore-not-found")
	}
}
