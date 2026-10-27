// Command notifier sends transactional email: billing notifications (RabbitMQ) and usage limit alerts (Kafka).
package main

import (
	"context"
	_ "embed" // config.default.yaml
	"errors"
	"time"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/app"
	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
	"github.com/0furkancolak/payrudder/pkg/safehttp"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/notifier/internal/delivery"
	"github.com/0furkancolak/payrudder/services/notifier/internal/handler"
	"github.com/0furkancolak/payrudder/services/notifier/internal/limits"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
	"github.com/0furkancolak/payrudder/services/notifier/internal/trials"
	"github.com/0furkancolak/payrudder/services/notifier/migrations"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	pgxx.SystemSettings
	RabbitMQURL   string        `env:"PAYRUDDER_RABBITMQ_URL,required"`
	KafkaBrokers  []string      `env:"PAYRUDDER_KAFKA_BROKERS,required" envSeparator:","`
	PlansAddr     string        `env:"PAYRUDDER_PLANS_ADDR"`
	SMTPAddr      string        `env:"SMTP_ADDR"`
	SandboxSMTP   string        `env:"SANDBOX_SMTP_ADDR"`
	MailFrom      string        `env:"MAIL_FROM"`
	SendingStale  time.Duration `env:"NOTIFIER_SENDING_STALE"`
	MailSwitchTTL time.Duration `env:"NOTIFIER_MAIL_SWITCH_TTL"`
	// MailAllowPrivate lets a tenant's own e-mail provider be http:// and on a private address. For development and tests; never in production.
	MailAllowPrivate bool   `env:"NOTIFIER_MAIL_ALLOW_PRIVATE"`
	Group            string `env:"PAYRUDDER_CONSUMER_GROUP"`
	DLQTopic         string `env:"PAYRUDDER_DLQ_TOPIC"`
	// TrialDLQTopic receives the trial-ending facts that can never be mailed; the Kafka group of that consumer is the group with "-trials" appended.
	TrialDLQTopic  string        `env:"PAYRUDDER_TRIAL_DLQ_TOPIC"`
	SessionTimeout time.Duration `env:"PAYRUDDER_CONSUMER_SESSION_TIMEOUT"`
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate()}
	errs = append(errs, c.CheckMigration())
	return errors.Join(errs...)
}

func main() { app.Main("notifier", defaults, start) }

func start(ctx context.Context, cfg *Config, rt *app.Runtime) error {
	log := rt.Log
	if done, err := cfg.MigrateIfAsked(ctx, migrations.FS, cfg.MigrateOnly, log); err != nil || done {
		return err
	}
	pool, err := pgxx.NewPool(ctx, cfg.PGSystemDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	plansConn, err := grpcx.NewTenantClient("plans-svc", cfg.PlansAddr)
	if err != nil {
		return err
	}
	defer func() { _ = plansConn.Close() }()

	notes := store.Notifications{DB: pool, Stale: cfg.SendingStale}
	mailer := &delivery.Mailer{Default: mailx.SMTP{Addr: cfg.SMTPAddr}, From: cfg.MailFrom, Notes: notes}
	// A tenant's mail switch (plans-svc) is read before every send.
	mailer.Switch = &delivery.PlansSwitch{Client: plansv1.NewPlansServiceClient(plansConn), TTL: cfg.MailSwitchTTL}
	// A tenant that connected its own e-mail provider (Nafru Email) is sent through it, live mail only.
	mailer.Providers = &delivery.PlansProviders{Client: plansv1.NewMailIntegrationServiceClient(plansConn), TTL: cfg.MailSwitchTTL, HTTP: safehttp.NewClient(15*time.Second, cfg.MailAllowPrivate)}
	if cfg.SandboxSMTP != "" {
		mailer.Sandbox = mailx.SMTP{Addr: cfg.SandboxSMTP}
	}
	w := &workerx.Worker{URL: cfg.RabbitMQURL, Handler: &handler.Handler{Mailer: mailer}, Inbox: notes, Log: log,
		Queues: []string{rabbitx.QueueNotificationSend}}

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

	trialGroup := cfg.Group + "-trials"
	trialOffsets := store.Offsets{DB: pool, Group: trialGroup, Topic: trials.Topic}
	tc := &trials.Consumer{Mailer: mailer, Plans: plansv1.NewPlansServiceClient(plansConn), Admin: plansv1.NewAdminServiceClient(plansConn),
		Offsets: trialOffsets, DLQ: kafkax.DLQ{Client: producer, Topic: cfg.TrialDLQTopic, Consumer: trialGroup}, Log: log}
	trialConsumer, err := kafkax.NewConsumer(kafkax.ConsumerConfig{Brokers: cfg.KafkaBrokers, GroupID: trialGroup, Topic: trials.Topic,
		LoadOffsets: trialOffsets.Load, Handler: tc, MaxPollRecords: 100, Backoff: 2 * time.Second, SessionTimeout: cfg.SessionTimeout, Log: log})
	if err != nil {
		return err
	}
	defer trialConsumer.Close()

	hh := rt.Health()
	hh.AddCheck("postgres", pool.Ping)
	hh.AddCheck("rabbitmq", func(context.Context) error { return w.Ping() })
	hh.AddCheck("kafka", producer.Ping)
	g := app.NewGroup(ctx, log, cfg.MetricsAddr, hh)
	gctx := g.Context()
	g.Go(func() error {
		log.Info("notifier consuming commands and limit alerts", "smtp", cfg.SMTPAddr)
		return w.Run(gctx)
	})
	g.Go(func() error { return consumer.Run(gctx) })
	g.Go(func() error { return trialConsumer.Run(gctx) })
	return g.Wait()
}
