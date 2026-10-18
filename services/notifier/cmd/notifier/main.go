// Command notifier sends transactional email: billing notifications (RabbitMQ) and usage limit alerts (Kafka).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/config"
	"github.com/0furkancolak/quotify/pkg/grpcx"
	"github.com/0furkancolak/quotify/pkg/health"
	"github.com/0furkancolak/quotify/pkg/kafkax"
	"github.com/0furkancolak/quotify/pkg/logx"
	"github.com/0furkancolak/quotify/pkg/otelx"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/rabbitx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/pkg/workerx"
	"github.com/0furkancolak/quotify/services/notifier/internal/handler"
	"github.com/0furkancolak/quotify/services/notifier/internal/limits"
	"github.com/0furkancolak/quotify/services/notifier/internal/mail"
	"github.com/0furkancolak/quotify/services/notifier/internal/store"
	"github.com/0furkancolak/quotify/services/notifier/migrations"
)

type Config struct {
	config.Base
	PGSystemDSN    string        `env:"QUOTIFY_PG_SYSTEM_DSN,required"`
	PGMigratorDSN  string        `env:"QUOTIFY_PG_MIGRATOR_DSN"`
	MigrateOnStart bool          `env:"QUOTIFY_MIGRATE_ON_START" envDefault:"false"`
	RabbitMQURL    string        `env:"QUOTIFY_RABBITMQ_URL,required"`
	KafkaBrokers   []string      `env:"QUOTIFY_KAFKA_BROKERS,required" envSeparator:","`
	PlansAddr      string        `env:"QUOTIFY_PLANS_ADDR" envDefault:"dns:///localhost:7001"`
	SMTPAddr       string        `env:"SMTP_ADDR" envDefault:"localhost:1025"`
	SandboxSMTP    string        `env:"SANDBOX_SMTP_ADDR"`
	MailFrom       string        `env:"MAIL_FROM" envDefault:"Quotify <billing@quotify.local>"`
	SendingStale   time.Duration `env:"NOTIFIER_SENDING_STALE" envDefault:"5m"`
	Group          string        `env:"QUOTIFY_CONSUMER_GROUP" envDefault:"notifier"`
	DLQTopic       string        `env:"QUOTIFY_DLQ_TOPIC" envDefault:"usage.limit-exceeded.dlq.v1"`
	SessionTimeout time.Duration `env:"QUOTIFY_CONSUMER_SESSION_TIMEOUT" envDefault:"10s"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	if c.MigrateOnStart && c.PGMigratorDSN == "" {
		errs = append(errs, errors.New("QUOTIFY_PG_MIGRATOR_DSN is required with QUOTIFY_MIGRATE_ON_START"))
	}
	return errors.Join(errs...)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "notifier:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load[Config]()
	if err != nil {
		return err
	}
	log, err := logx.New(logx.Options{Service: cfg.ServiceName, Version: cfg.Version, Level: cfg.LogLevel, Writer: os.Stdout})
	if err != nil {
		return err
	}
	slog.SetDefault(log)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	reg := health.NewRegistry()
	providers, err := otelx.Setup(ctx, otelx.Options{
		ServiceName: cfg.ServiceName, Version: cfg.Version, Environment: string(cfg.Env), OTLPEndpoint: cfg.OTLPEndpoint, Registry: reg,
	})
	if err != nil {
		return err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = providers.Shutdown(c)
	}()

	if cfg.MigrateOnStart {
		if err := pgxx.Migrate(ctx, cfg.PGMigratorDSN, migrations.FS, log); err != nil {
			return err
		}
	}
	pool, err := pgxx.NewPool(ctx, cfg.PGSystemDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	notes := store.Notifications{DB: pool, Stale: cfg.SendingStale}
	mailer := &handler.Mailer{Default: mail.SMTP{Addr: cfg.SMTPAddr}, From: cfg.MailFrom, Notes: notes}
	if cfg.SandboxSMTP != "" {
		mailer.Sandbox = mail.SMTP{Addr: cfg.SandboxSMTP}
	}
	w := &workerx.Worker{URL: cfg.RabbitMQURL, Handler: &handler.Handler{Mailer: mailer}, Inbox: notes, Log: log,
		Queues: []string{rabbitx.QueueNotificationSend}}

	plansConn, err := grpcx.NewClient(cfg.PlansAddr, grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()))
	if err != nil {
		return fmt.Errorf("plans-svc client: %w", err)
	}
	defer func() { _ = plansConn.Close() }()
	producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()
	offsets := store.Offsets{DB: pool, Group: cfg.Group, Topic: limits.Topic}
	lc := &limits.Consumer{Mailer: mailer, Plans: plansv1.NewPlansServiceClient(plansConn), Admin: plansv1.NewAdminServiceClient(plansConn),
		Offsets: offsets, DLQ: kafkax.DLQ{Client: producer, Topic: cfg.DLQTopic, Consumer: cfg.Group}, Log: log}
	consumer, err := kafkax.NewConsumer(kafkax.ConsumerConfig{Brokers: cfg.KafkaBrokers, GroupID: cfg.Group, Topic: limits.Topic,
		LoadOffsets: offsets.Load, Handler: lc, MaxPollRecords: 100, Backoff: 2 * time.Second, SessionTimeout: cfg.SessionTimeout, Log: log})
	if err != nil {
		return err
	}
	defer consumer.Close()

	hh := health.New(reg, 2*time.Second)
	hh.AddCheck("postgres", pool.Ping)
	hh.AddCheck("rabbitmq", func(context.Context) error { return w.Ping() })
	hh.AddCheck("kafka", producer.Ping)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return health.Serve(gctx, cfg.MetricsAddr, hh) })
	g.Go(func() error {
		log.Info("notifier consuming commands and limit alerts", "smtp", cfg.SMTPAddr)
		return w.Run(gctx)
	})
	g.Go(func() error { return consumer.Run(gctx) })
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Info("stopped")
	return nil
}
