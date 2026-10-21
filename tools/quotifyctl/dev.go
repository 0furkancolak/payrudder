package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// `quotifyctl dev` runs the Quotify services on the host, against the infrastructure containers, and restarts a service when its code changes. The
// services' settings are the compose file's, read as `docker compose config`, with every container address turned into the published host port: there is
// one list of settings, not one for Docker and one for the host. Application images are for CI and releases; nothing here builds one.

type composeFile struct {
	Services map[string]composeService `json:"services"`
}

type composeService struct {
	Build       *struct{ Dockerfile string } `json:"build"`
	Environment map[string]string            `json:"environment"`
	Ports       []struct {
		Target    int    `json:"target"`
		Published string `json:"published"`
	} `json:"ports"`
}

// hostService is one service to run on the host.
type hostService struct {
	Name string
	Dir  string // module directory, e.g. services/plans-svc
	Main string // package to build, e.g. ./cmd/plans-svc
	Env  map[string]string
}

// startOrder lists the services that others depend on first; the rest start in name order.
var startOrder = []string{"plans-svc", "mockpsp", "billing-svc", "aggregator"}

// hostServices picks the application services of a compose file and gives each its settings for a process on the host.
func hostServices(c composeFile) []hostService {
	published := map[string]string{} // "name:containerPort" -> "localhost:hostPort"
	for name, s := range c.Services {
		for _, p := range s.Ports {
			if p.Published != "" {
				published[fmt.Sprintf("%s:%d", name, p.Target)] = "localhost:" + p.Published
			}
		}
	}
	// Redpanda has two listeners: containers use 9092, the host uses the external listener, whose container port is 42092 (the port it advertises as localhost:42092).
	if h, ok := published["redpanda:42092"]; ok {
		published["redpanda:9092"] = h
	}
	var out []hostService
	for name, s := range c.Services {
		if s.Build == nil || (!strings.HasPrefix(s.Build.Dockerfile, "services/") && !strings.HasPrefix(s.Build.Dockerfile, "tools/")) {
			continue
		}
		dir := filepath.Dir(s.Build.Dockerfile)
		env := map[string]string{}
		for k, v := range s.Environment {
			env[k] = toHost(v, published)
			// A listen address (":7001") moves to the port the container publishes, so a service on the host sits where the stack expects it.
			if m := listenRe.FindStringSubmatch(v); m != nil {
				if h, ok := published[name+":"+m[1]]; ok {
					env[k] = ":" + strings.TrimPrefix(h, "localhost:")
				}
			}
		}
		// Tracing needs the observability stack; on the host it is off unless the collector's port is published.
		if _, ok := published["otel-collector:4317"]; !ok {
			env["QUOTIFY_OTLP_ENDPOINT"] = ""
		}
		out = append(out, hostService{Name: name, Dir: dir, Main: "./cmd/" + filepath.Base(dir), Env: env})
	}
	sort.Slice(out, func(i, j int) bool {
		pi, pj := slices.Index(startOrder, out[i].Name), slices.Index(startOrder, out[j].Name)
		switch {
		case pi >= 0 && pj >= 0:
			return pi < pj
		case pi >= 0:
			return true
		case pj >= 0:
			return false
		}
		return out[i].Name < out[j].Name
	})
	return out
}

var listenRe = regexp.MustCompile(`^:([0-9]{2,5})$`)

var addrRe = regexp.MustCompile(`([A-Za-z][A-Za-z0-9-]*):([0-9]{2,5})`)

// toHost rewrites "postgres:5432" and "dns:///plans-svc:7001" in a setting to the host port the container publishes. A name the compose file does not
// publish (a metrics-only port, an unknown host) is left alone.
func toHost(v string, published map[string]string) string {
	v = strings.ReplaceAll(v, "dns:///", "")
	return addrRe.ReplaceAllStringFunc(v, func(m string) string {
		if h, ok := published[m]; ok {
			return h
		}
		return m
	})
}

func dev(args []string) error {
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	composePath := fs.String("compose", "deploy/compose/compose.yaml", "the compose file that describes the stack")
	only := fs.String("only", "", "run only these services (comma separated)")
	skip := fs.String("skip", "", "do not run these services (comma separated)")
	list := fs.Bool("list", false, "print the services and their settings, run nothing")
	root := fs.String("root", ".", "the repository root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.Chdir(*root); err != nil {
		return err
	}
	out, err := exec.Command("docker", "compose", "-f", *composePath, "--profile", "app", "config", "--format", "json").Output() //nolint:gosec // a fixed command on a path the developer gave
	if err != nil {
		return fmt.Errorf("docker compose config: %w (is Docker running?)", err)
	}
	var cf composeFile
	if err := json.Unmarshal(out, &cf); err != nil {
		return err
	}
	svcs := filterServices(hostServices(cf), split(*only), split(*skip))
	if len(svcs) == 0 {
		return errors.New("no services selected")
	}
	if *list {
		for _, s := range svcs {
			fmt.Fprintf(os.Stdout, "%-16s %s\n", s.Name, s.Dir)
			keys := make([]string, 0, len(s.Env))
			for k := range s.Env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(os.Stdout, "    %s=%s\n", k, s.Env[k])
			}
		}
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	repo, _ := os.Getwd()
	var wg sync.WaitGroup
	for i, s := range svcs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-time.After(time.Duration(i) * 700 * time.Millisecond): // dependencies first, gently
			case <-ctx.Done():
				return
			}
			runWatched(ctx, repo, s)
		}()
	}
	fmt.Fprintf(os.Stdout, "running %d services on the host; Ctrl-C stops them\n", len(svcs))
	wg.Wait()
	return nil
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func filterServices(all []hostService, only, skip []string) []hostService {
	var out []hostService
	for _, s := range all {
		if (len(only) > 0 && !slices.Contains(only, s.Name)) || slices.Contains(skip, s.Name) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// runWatched builds and runs a service, and rebuilds and restarts it when a source file under its module or pkg/ changes.
func runWatched(ctx context.Context, root string, s hostService) {
	bin := filepath.Join(root, "bin", "dev", s.Name)
	logw := prefixWriter(s.Name, os.Stdout)
	for ctx.Err() == nil {
		stamp := newestSource(root, s.Dir)
		build := exec.CommandContext(ctx, "go", "build", "-o", bin, s.Main) //nolint:gosec // paths of this repository
		build.Dir = filepath.Join(root, s.Dir)
		build.Stdout, build.Stderr = logw, logw
		if err := build.Run(); err != nil {
			fmt.Fprintf(logw, "build failed: %v; waiting for a change\n", err)
			waitForChange(ctx, root, s.Dir, stamp)
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		cmd := exec.CommandContext(runCtx, bin) //nolint:gosec // the binary just built
		cmd.Dir = filepath.Join(root, s.Dir)
		cmd.Env = append(os.Environ(), envList(s.Env)...)
		cmd.Stdout, cmd.Stderr = logw, logw
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = 10 * time.Second
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(logw, "start failed: %v\n", err)
			cancel()
			waitForChange(ctx, root, s.Dir, stamp)
			continue
		}
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		changed := make(chan struct{})
		go func() { waitForChange(runCtx, root, s.Dir, stamp); close(changed) }()
		select {
		case err := <-exited:
			fmt.Fprintf(logw, "exited: %v; waiting for a change\n", err)
			cancel()
			waitForChange(ctx, root, s.Dir, stamp)
		case <-changed:
			fmt.Fprintln(logw, "source changed, restarting")
			cancel()
			<-exited //nolint:errcheck // the exit status of a process we just stopped
		case <-ctx.Done():
			cancel()
			<-exited //nolint:errcheck // the exit status of a process we just stopped
		}
	}
}

func envList(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// newestSource is the modification time of the newest Go, SQL, YAML or proto-generated file in the module and the shared directories.
func newestSource(root, dir string) time.Time {
	var newest time.Time
	for _, d := range []string{dir, "pkg", "gen/go"} {
		_ = filepath.WalkDir(filepath.Join(root, d), func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if e.IsDir() {
				if n := e.Name(); n == "node_modules" || n == ".git" || n == "itest" {
					return filepath.SkipDir
				}
				return nil
			}
			if ext := filepath.Ext(path); ext == ".go" || ext == ".sql" || ext == ".yaml" {
				if info, err := e.Info(); err == nil && info.ModTime().After(newest) {
					newest = info.ModTime()
				}
			}
			return nil
		})
	}
	return newest
}

func waitForChange(ctx context.Context, root, dir string, since time.Time) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if newestSource(root, dir).After(since) {
				time.Sleep(300 * time.Millisecond) // let an editor finish writing
				return
			}
		}
	}
}

var colors = []string{"36", "32", "33", "35", "34", "96", "92", "93", "95", "94"}

// prefixWriter writes each line with the service's name in front, in a colour of its own.
func prefixWriter(name string, w io.Writer) io.Writer {
	pr, pw := io.Pipe()
	color := colors[int(name[0])%len(colors)]
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			var b bytes.Buffer
			fmt.Fprintf(&b, "\x1b[%sm%-14s\x1b[0m %s\n", color, name, sc.Text())
			_, _ = w.Write(b.Bytes())
		}
	}()
	return pw
}
