package testkit

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PgBouncerImage is the pooler of deploy/compose/compose.yaml (profile pooler): edoburu/pgbouncer v1.25.2-p0, pinned by digest. PgBouncer 1.21 and later tracks
// the named prepared statements of the extended protocol (max_prepared_statements), which pgx uses by default.
const PgBouncerImage = "edoburu/pgbouncer@sha256:7d7a27d9e90985cab5cf42256f5c13a3120baa4b055b69df37beb272b89b2340"

// PoolerEnv, when set (ITEST_POOLER=1), puts every service's app-role DSN that CreateServiceDB and CreateCellDB return behind a transaction-mode PgBouncer, so the
// whole integration suite of a service runs the way a deployment with a pooler does. The system and migrator DSNs stay direct: docs/runbooks/scaling.md says why.
const PoolerEnv = "ITEST_POOLER"

// StartNetwork creates a Docker network that lives as long as the test; containers on it reach each other by alias.
func StartNetwork(t testing.TB) *testcontainers.DockerNetwork {
	t.Helper()
	ctx := startCtx(t)
	nw, err := network.New(ctx)
	if err != nil {
		t.Fatalf("testkit: create network: %v", err)
	}
	t.Cleanup(func() { _ = nw.Remove(ctx) })
	return nw
}

// StartPostgresOn is StartPostgres on a network, under the alias, so that a pooler on the same network can reach it. A second call with another alias is a second
// cluster.
func StartPostgresOn(t testing.TB, nw *testcontainers.DockerNetwork, alias string) Postgres {
	t.Helper()
	pg := startPostgres(t, network.WithNetwork([]string{alias}, nw))
	pg.Alias = alias
	return pg
}

// pgbouncerINI is the config of the test pooler: deploy/compose/pgbouncer/pgbouncer.ini with the databases, pool size and prepared-statement setting filled in.
const pgbouncerINI = `[databases]
@DATABASES@

[pgbouncer]
listen_addr = 0.0.0.0
listen_port = 6432
auth_type = scram-sha-256
auth_file = /etc/pgbouncer/userlist.txt
auth_user = payrudder
auth_query = SELECT usename, passwd FROM pg_shadow WHERE usename = $1
pool_mode = transaction
default_pool_size = @POOL_SIZE@
max_client_conn = 500
max_prepared_statements = @PREPARED@
admin_users = payrudder
ignore_startup_parameters = extra_float_digits
`

// PoolerOptions configure StartPgBouncer.
type PoolerOptions struct {
	// Routes maps the name of a database to the Postgres that holds it; "*" stands for every other database.
	Routes map[string]Postgres
	// PoolSize is the number of server connections per user and database (default 2: small, so that clients really share them).
	PoolSize int
	// MaxPreparedStatements is PgBouncer's max_prepared_statements. 0 turns the tracking off, which is how PgBouncer before 1.21 behaves.
	MaxPreparedStatements int
}

// Pooler is a running PgBouncer in transaction mode.
type Pooler struct {
	// Host and Port are where the test process reaches it.
	Host, Port  string
	ContainerID string
	// AdminDSN is the admin console (database pgbouncer: SHOW POOLS, SHOW SERVERS), which speaks the simple protocol only.
	AdminDSN string
}

// StartPgBouncer starts a transaction-mode PgBouncer on nw in front of the Postgres containers of opts.Routes (each started with StartPostgresOn). Clients log in
// with their own role and password: PgBouncer asks the cluster for the verifier (auth_query, as the superuser) and passes the SCRAM exchange through.
func StartPgBouncer(t testing.TB, nw *testcontainers.DockerNetwork, opts PoolerOptions) Pooler {
	t.Helper()
	if len(opts.Routes) == 0 {
		t.Fatal("testkit: pgbouncer without routes")
	}
	if opts.PoolSize <= 0 {
		opts.PoolSize = 2
	}
	var dbs []string
	for name, pg := range opts.Routes {
		if pg.Alias == "" {
			t.Fatalf("testkit: pgbouncer route %q: the Postgres is not on a network (StartPostgresOn)", name)
		}
		dbs = append(dbs, fmt.Sprintf("%s = host=%s port=5432", name, pg.Alias))
	}
	slices.Sort(dbs)
	ini := strings.NewReplacer("@DATABASES@", strings.Join(dbs, "\n"), "@POOL_SIZE@", strconv.Itoa(opts.PoolSize),
		"@PREPARED@", strconv.Itoa(opts.MaxPreparedStatements)).Replace(pgbouncerINI)
	g := startGeneric(t, "pgbouncer", testcontainers.ContainerRequest{
		Image:        PgBouncerImage,
		ExposedPorts: []string{"6432/tcp"},
		Files: []testcontainers.ContainerFile{
			{Reader: strings.NewReader(ini), ContainerFilePath: "/etc/pgbouncer/pgbouncer.ini", FileMode: 0o644},
			{Reader: strings.NewReader("\"payrudder\" \"payrudder\"\n"), ContainerFilePath: "/etc/pgbouncer/userlist.txt", FileMode: 0o644},
		},
		Networks:   []string{nw.Name},
		WaitingFor: wait.ForListeningPort("6432/tcp").WithStartupTimeout(startTimeout),
	})
	port := g.port("6432/tcp")
	return Pooler{Host: g.host, Port: port, ContainerID: g.c.GetContainerID(),
		AdminDSN: fmt.Sprintf("postgres://payrudder:payrudder@%s/pgbouncer?sslmode=disable&default_query_exec_mode=simple_protocol", net.JoinHostPort(g.host, port))}
}

// DSN is dsn, a direct postgres:// address, pointed at the pooler instead (same role, password and database).
func (p Pooler) DSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		panic("testkit: pooler dsn: " + err.Error())
	}
	u.Host = net.JoinHostPort(p.Host, p.Port)
	return u.String()
}

// Via returns the service database with its app-role DSN behind the pooler. The system role keeps the direct address (its relay LISTENs and its replay and billing
// run take session advisory locks), and so does the migrator (goose's session lock, the search_path of a tenant migration).
func (s ServiceDB) Via(p Pooler) ServiceDB {
	s.App = p.DSN(s.App)
	return s
}

// poolerRequested reports whether ITEST_POOLER asks for every test database to sit behind a pooler.
func poolerRequested() bool { return os.Getenv(PoolerEnv) != "" }
