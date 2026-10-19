//go:build chaos

package chaos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Monkey injects the faults of docs/09-testing-strategy.md#chaos-schedule while the workload runs:
//   - every 10-30 s, SIGKILL one random replica of ingest-svc, aggregator, limits-svc or billing-svc (compose restarts it);
//   - every 60 s, a Toxiproxy fault of 5-15 s on the Postgres, Kafka or Redis path (500 ms latency or connection resets);
//   - once, Redpanda stopped for 30 s;
//   - once, Redis flushed and restarted (all limit counters lost; the reconcile loop must rebuild them).
//
// Everything it does is counted, because a green run without real chaos proves nothing.
type Monkey struct {
	rng     *rand.Rand
	toxi    string
	project string

	mu     sync.Mutex
	Report MonkeyReport

	wg     sync.WaitGroup
	cancel context.CancelFunc
}

// MonkeyReport is what the monkey actually did.
type MonkeyReport struct {
	Kills         int            `json:"kills"`
	KillsByTarget map[string]int `json:"kills_by_target"`
	Faults        int            `json:"network_faults"`
	FaultsByKind  map[string]int `json:"network_faults_by_kind"`
	BrokerStops   int            `json:"broker_stops"`
	RedisRestarts int            `json:"redis_restarts"`
	Errors        []string       `json:"errors,omitempty"`
}

var killTargets = []string{"ingest-svc", "aggregator", "limits-svc", "billing-svc"}

// StartMonkey begins injecting faults. Call Stop to end them and wait for the stack to recover. total is the expected
// duration of the workload, used to place the one-off faults inside it.
func StartMonkey(parent context.Context, seed uint64, toxiproxyURL string, total time.Duration) *Monkey {
	ctx, cancel := context.WithCancel(parent)
	m := &Monkey{
		rng:  rand.New(rand.NewPCG(seed^0x6d6f6e6b, seed)), //nolint:gosec // fault schedule, not security
		toxi: toxiproxyURL, project: "quotify", cancel: cancel,
		Report: MonkeyReport{KillsByTarget: map[string]int{}, FaultsByKind: map[string]int{}},
	}
	m.wg.Add(3)
	go func() { defer m.wg.Done(); m.killLoop(ctx) }()
	go func() { defer m.wg.Done(); m.faultLoop(ctx) }()
	go func() { defer m.wg.Done(); m.oneOffs(ctx, total) }()
	return m
}

// Stop ends the faults, removes any toxic and starts what is stopped.
func (m *Monkey) Stop() MonkeyReport {
	m.cancel()
	m.wg.Wait()
	m.clearToxics()
	// Bring back whatever is still down: a killed container whose restart was cut short, a stopped broker.
	for _, svc := range append([]string{"redpanda", "redis"}, killTargets...) {
		for _, c := range m.containers(svc, true) {
			_ = docker("start", c)
		}
	}
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

func docker(args ...string) error {
	out, err := exec.Command("docker", args...).CombinedOutput() //nolint:gosec // test tool: args are built from fixed service names
	if err != nil {
		return fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// containers lists the compose containers of a service (running ones, or all when all is true).
func (m *Monkey) containers(service string, all bool) []string {
	args := []string{"ps", "--filter", "label=com.docker.compose.project=" + m.project, "--filter", "label=com.docker.compose.service=" + service, "--format", "{{.Names}}"}
	if all {
		args = append(args, "-a")
	}
	out, err := exec.Command("docker", args...).Output() //nolint:gosec // test tool: args are built from fixed service names
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

func (m *Monkey) killLoop(ctx context.Context) {
	for sleepCtx(ctx, time.Duration(10+m.pick(21))*time.Second) {
		svc := killTargets[m.pick(len(killTargets))]
		cs := m.containers(svc, false)
		if len(cs) == 0 {
			continue
		}
		victim := cs[m.pick(len(cs))]
		if err := docker("kill", "-s", "KILL", victim); err != nil {
			m.record(func(r *MonkeyReport) { r.Errors = append(r.Errors, err.Error()) })
			continue
		}
		m.record(func(r *MonkeyReport) { r.Kills++; r.KillsByTarget[svc]++ })
		// docker does not restart a container that was killed through its API, so play the orchestrator: bring it back after
		// a short outage, as Kubernetes' kubelet does for a crashed pod.
		go func() {
			time.Sleep(time.Duration(2+m.pick(4)) * time.Second)
			if err := docker("start", victim); err != nil {
				m.record(func(r *MonkeyReport) { r.Errors = append(r.Errors, err.Error()) })
			}
		}()
	}
}

type toxic struct{ proxy, kind string }

var faultMenu = []toxic{
	{"postgres", "latency"}, {"postgres", "reset"}, {"kafka", "latency"}, {"kafka", "reset"}, {"redis", "latency"}, {"redis", "reset"},
}

func (m *Monkey) toxiCall(method, path string, body any) error {
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, m.toxi+path, r)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("toxiproxy %s %s: %d", method, path, resp.StatusCode)
	}
	return nil
}

func (m *Monkey) faultLoop(ctx context.Context) {
	for sleepCtx(ctx, 60*time.Second) {
		f := faultMenu[m.pick(len(faultMenu))]
		spec := map[string]any{"name": "chaos", "stream": "downstream", "toxicity": 1.0}
		switch f.kind {
		case "latency":
			spec["type"], spec["attributes"] = "latency", map[string]any{"latency": 500, "jitter": 100}
		default:
			// reset_peer with timeout 0 resets each connection as soon as data flows.
			spec["type"], spec["attributes"] = "reset_peer", map[string]any{"timeout": 0}
		}
		if err := m.toxiCall("POST", "/proxies/"+f.proxy+"/toxics", spec); err != nil {
			m.record(func(r *MonkeyReport) { r.Errors = append(r.Errors, err.Error()) })
			continue
		}
		m.record(func(r *MonkeyReport) { r.Faults++; r.FaultsByKind[f.proxy+"/"+f.kind]++ })
		sleepCtx(ctx, time.Duration(5+m.pick(11))*time.Second)
		_ = m.toxiCall("DELETE", "/proxies/"+f.proxy+"/toxics/chaos", nil)
	}
}

func (m *Monkey) clearToxics() {
	for _, f := range faultMenu {
		_ = m.toxiCall("DELETE", "/proxies/"+f.proxy+"/toxics/chaos", nil)
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
			for _, c := range m.containers("redpanda", false) {
				if err := docker("stop", "-t", "1", c); err != nil {
					m.record(func(r *MonkeyReport) { r.Errors = append(r.Errors, err.Error()) })
					continue
				}
				m.record(func(r *MonkeyReport) { r.BrokerStops++ })
				sleepCtx(context.WithoutCancel(ctx), 30*time.Second)
				_ = docker("start", c)
			}
		}
		if !done["redis"] && el >= redisAt {
			done["redis"] = true
			for _, c := range m.containers("redis", false) {
				_ = docker("exec", c, "redis-cli", "FLUSHALL")
				if err := docker("restart", "-t", "1", c); err != nil {
					m.record(func(r *MonkeyReport) { r.Errors = append(r.Errors, err.Error()) })
					continue
				}
				m.record(func(r *MonkeyReport) { r.RedisRestarts++ })
			}
		}
	}
}
