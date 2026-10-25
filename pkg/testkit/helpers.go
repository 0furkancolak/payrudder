package testkit

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
)

// MigratedDB creates the service's database and roles (CreateServiceDB) and applies its migrations as the migrator
// role, the way the service does at start. Call it once per service and test package.
func MigratedDB(t testing.TB, pg Postgres, svc string, migrations fs.FS) ServiceDB {
	t.Helper()
	db := CreateServiceDB(t, pg, svc)
	if err := pgxx.Migrate(context.Background(), db.Migrator, migrations, nil); err != nil {
		t.Fatalf("testkit: migrate %s: %v", svc, err)
	}
	return db
}

// Logger is the logger of an integration test: silent unless ITEST_VERBOSE is set.
func Logger() *slog.Logger {
	if os.Getenv("ITEST_VERBOSE") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// pollEvery is how often Eventually looks at its condition.
const pollEvery = 50 * time.Millisecond

// Eventually polls cond until it is true and fails the test after timeout. what says what is waited for.
func Eventually(t testing.TB, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(pollEvery)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// DockerPause freezes (pause) or thaws (unpause) a container: the outage of a database or broker, without losing its state.
func DockerPause(t testing.TB, containerID string, pause bool) {
	t.Helper()
	verb := "unpause"
	if pause {
		verb = "pause"
	}
	if out, err := exec.Command("docker", verb, containerID).CombinedOutput(); err != nil { //nolint:gosec // a container id from a started test container
		t.Fatalf("docker %s %s: %v: %s", verb, containerID, err, out)
	}
}
