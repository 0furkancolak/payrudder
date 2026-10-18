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
)

// Images used by the helpers. Keep in sync with deploy/compose/compose.yaml.
const (
	PostgresImage = "postgres:17.11-alpine"
	RedpandaImage = "redpandadata/redpanda:v26.2.5"
	RabbitMQImage = "rabbitmq:4.2.9-management-alpine"
	RedisImage    = "redis:7.4.11-alpine"
)

const startTimeout = 2 * time.Minute

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
		postgres.WithDatabase("quotify"),
		postgres.WithUsername("quotify"),
		postgres.WithPassword("quotify"),
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
	return Postgres{DSN: dsn, ContainerID: c.GetContainerID()}
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
	return Redpanda{Brokers: seed, ContainerID: c.GetContainerID()}
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
