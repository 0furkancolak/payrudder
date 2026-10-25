// Package testkit starts real infrastructure in containers for integration tests (testcontainers-go).
//
// Every Start function registers cleanup on t, so a test only asks for what it needs:
//
//	pg := testkit.StartPostgres(t)
//	brokers := testkit.StartRedpanda(t)
//
// Image versions match deploy/compose/compose.yaml.
package testkit

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Images used by the helpers. Keep in sync with deploy/compose/compose.yaml.
const (
	PostgresImage = "postgres:17.11-alpine"
	RedpandaImage = "redpandadata/redpanda:v26.2.5"
	RabbitMQImage = "rabbitmq:4.2.9-management-alpine"
	RedisImage    = "redis:7.4.11-alpine"
)

// startTimeout bounds one container start. A busy Docker host (many projects, slow image pulls) needs minutes, not seconds.
const startTimeout = 6 * time.Minute

func startCtx(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	t.Cleanup(cancel)
	return ctx
}

// Postgres is a running Postgres container.
type Postgres struct {
	// DSN connects as the superuser to the default database.
	DSN string
	// ContainerID lets chaos tests pause and unpause the database with `docker pause`.
	ContainerID string
}

// StartPostgres starts Postgres and waits until it accepts connections.
func StartPostgres(t testing.TB) Postgres {
	t.Helper()
	ctx := startCtx(t)
	c, err := postgres.Run(ctx, PostgresImage,
		postgres.WithDatabase("payrudder"),
		postgres.WithUsername("payrudder"),
		postgres.WithPassword("payrudder"),
		postgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("testkit: start postgres: %v", err)
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("testkit: postgres dsn: %v", err)
	}
	return Postgres{DSN: dsn, ContainerID: c.GetContainerID()} //nolint:gosec // test container
}

// Redpanda is a running Redpanda container.
type Redpanda struct {
	// Brokers is the Kafka seed address (host:port).
	Brokers string
	// ContainerID lets chaos tests pause and unpause the broker with `docker pause`.
	ContainerID string
}

// StartRedpanda starts a single Redpanda broker and returns its Kafka seed address (host:port).
func StartRedpanda(t testing.TB) string {
	t.Helper()
	return StartRedpandaContainer(t).Brokers
}

// StartRedpandaContainer is StartRedpanda plus the container id.
func StartRedpandaContainer(t testing.TB) Redpanda {
	t.Helper()
	ctx := startCtx(t)
	c, err := redpanda.Run(ctx, RedpandaImage, redpanda.WithAutoCreateTopics())
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("testkit: start redpanda: %v", err)
	}
	seed, err := c.KafkaSeedBroker(ctx)
	if err != nil {
		t.Fatalf("testkit: redpanda seed broker: %v", err)
	}
	return Redpanda{Brokers: seed, ContainerID: c.GetContainerID()} //nolint:gosec // test container
}

// StartRabbitMQ starts RabbitMQ and returns its AMQP URL.
func StartRabbitMQ(t testing.TB) string {
	t.Helper()
	ctx := startCtx(t)
	c, err := rabbitmq.Run(ctx, RabbitMQImage)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("testkit: start rabbitmq: %v", err)
	}
	url, err := c.AmqpURL(ctx)
	if err != nil {
		t.Fatalf("testkit: rabbitmq url: %v", err)
	}
	return url
}

// StartRedis starts Redis and returns its connection URL (redis://host:port).
func StartRedis(t testing.TB) string {
	t.Helper()
	ctx := startCtx(t)
	c, err := redis.Run(ctx, RedisImage)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("testkit: start redis: %v", err)
	}
	url, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("testkit: redis url: %v", err)
	}
	return url
}

// MinioImage is the S3-compatible server of Compose, pinned by digest (docs/adr/0013-s3-compatible-object-storage.md).
const MinioImage = "cgr.dev/chainguard/minio@sha256:c93745c56fccdaf0e5dcb88999a96a65a136428beb045ef5d546255a80d747a7"

// Minio is a running S3-compatible server.
type Minio struct {
	// Endpoint is the S3 API URL (http://host:port).
	Endpoint  string
	AccessKey string
	SecretKey string
}

// StartMinio starts a MinIO server and returns its S3 endpoint and credentials.
func StartMinio(t testing.TB) Minio {
	t.Helper()
	ctx := startCtx(t)
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        MinioImage,
			Cmd:          []string{"server", "/data", "--address", ":9000"},
			ExposedPorts: []string{"9000/tcp"},
			Env:          map[string]string{"MINIO_ROOT_USER": "testkey", "MINIO_ROOT_PASSWORD": "testsecret-testsecret"},
			WaitingFor:   wait.ForHTTP("/minio/health/live").WithPort("9000/tcp").WithStartupTimeout(startTimeout),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("testkit: start minio: %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("testkit: minio host: %v", err)
	}
	port, err := c.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatalf("testkit: minio port: %v", err)
	}
	return Minio{Endpoint: "http://" + host + ":" + port.Port(), AccessKey: "testkey", SecretKey: "testsecret-testsecret"}
}

// MailpitImage matches deploy/compose/compose.yaml.
const MailpitImage = "axllent/mailpit:v1.31.6"

// Mailpit is a running Mailpit (SMTP sink with an HTTP API).
type Mailpit struct {
	// SMTPAddr is host:port of the SMTP listener.
	SMTPAddr string
	// APIURL is the base URL of the HTTP API (http://host:port).
	APIURL string
}

// StartMailpit starts Mailpit.
func StartMailpit(t testing.TB) Mailpit {
	t.Helper()
	ctx := startCtx(t)
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        MailpitImage,
			ExposedPorts: []string{"1025/tcp", "8025/tcp"},
			WaitingFor:   wait.ForHTTP("/readyz").WithPort("8025/tcp").WithStartupTimeout(startTimeout),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("testkit: start mailpit: %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("testkit: mailpit host: %v", err)
	}
	smtp, err := c.MappedPort(ctx, "1025/tcp")
	if err != nil {
		t.Fatalf("testkit: mailpit smtp port: %v", err)
	}
	api, err := c.MappedPort(ctx, "8025/tcp")
	if err != nil {
		t.Fatalf("testkit: mailpit api port: %v", err)
	}
	return Mailpit{SMTPAddr: host + ":" + smtp.Port(), APIURL: "http://" + host + ":" + api.Port()}
}

// ClickHouseImage matches deploy/compose/compose.yaml.
const ClickHouseImage = "clickhouse/clickhouse-server:25.8.33.6-alpine"

// ClickHouse is a running ClickHouse server.
type ClickHouse struct {
	// Addr is host:port of the native protocol.
	Addr        string
	Database    string
	User        string
	Password    string
	ContainerID string
}

// StartClickHouse starts ClickHouse with a database and a user.
func StartClickHouse(t testing.TB) ClickHouse {
	t.Helper()
	ctx := startCtx(t)
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        ClickHouseImage,
			ExposedPorts: []string{"9000/tcp", "8123/tcp"},
			Env: map[string]string{"CLICKHOUSE_DB": "payrudder", "CLICKHOUSE_USER": "payrudder", "CLICKHOUSE_PASSWORD": "payrudder_test", //nolint:gosec // test container
				"CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1"},
			WaitingFor: wait.ForHTTP("/ping").WithPort("8123/tcp").WithStartupTimeout(startTimeout),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("testkit: start clickhouse: %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("testkit: clickhouse host: %v", err)
	}
	port, err := c.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatalf("testkit: clickhouse port: %v", err)
	}
	return ClickHouse{Addr: host + ":" + port.Port(), Database: "payrudder", User: "payrudder", Password: "payrudder_test", ContainerID: c.GetContainerID()} //nolint:gosec // test container
}
