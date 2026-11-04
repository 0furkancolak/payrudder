// Command gateway is Payrudder's single public entry point: HTTP/JSON in, gRPC out.
package main

import (
	"context"
	_ "embed" // config.default.yaml
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	analyticsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/analytics/v1"
	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	auditv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/audit/v1"
	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/app"
	"github.com/0furkancolak/payrudder/pkg/config"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/health"
	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/oauth"
	"github.com/0furkancolak/payrudder/pkg/safehttp"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	_ "github.com/0furkancolak/payrudder/pkg/tzdata" // IANA zones for time.LoadLocation in distroless images
	"github.com/0furkancolak/payrudder/services/gateway/internal/auth"
	"github.com/0furkancolak/payrudder/services/gateway/internal/httpapi"
	"github.com/0furkancolak/payrudder/services/gateway/internal/mailadmin"
	"github.com/0furkancolak/payrudder/services/gateway/internal/oidc"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

//go:embed config.default.yaml
var defaults []byte

type Config struct {
	config.Base
	HTTPAddr       string `env:"PAYRUDDER_HTTP_ADDR"`
	PlansAddr      string `env:"PAYRUDDER_PLANS_ADDR"`
	IngestAddr     string `env:"PAYRUDDER_INGEST_ADDR"`
	AggregatorAddr string `env:"PAYRUDDER_AGGREGATOR_ADDR"`
	LimitsAddr     string `env:"PAYRUDDER_LIMITS_ADDR"`
	BillingAddr    string `env:"PAYRUDDER_BILLING_ADDR"`
	AnalyticsAddr  string `env:"PAYRUDDER_ANALYTICS_ADDR"`
	AssetsAddr     string `env:"PAYRUDDER_ASSETS_ADDR"`
	RedisURL       string `env:"PAYRUDDER_REDIS_URL"`
	OperatorToken  string `env:"PAYRUDDER_OPERATOR_TOKEN,required"`
	// PaymentsURL is payments-worker's HTTP address; empty disables provider webhooks and provider accounts.
	// NotifierURL is the notifier's events listener (NOTIFIER_EVENTS_ADDR, "http://notifier:9111") and NotifierSecret its NOTIFIER_MAIL_EVENTS_SECRET, which signs every request
	// (environment only). Both or neither: without them the suppression list, the message log and the price-change preview answer 503 gateway.mail.unavailable.
	NotifierURL    string   `env:"PAYRUDDER_NOTIFIER_EVENTS_URL"`
	NotifierSecret string   `env:"PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET"`
	PaymentsURL    string   `env:"PAYRUDDER_PAYMENTS_URL"`
	PaymentsToken  string   `env:"PAYRUDDER_PAYMENTS_INTERNAL_TOKEN"`
	Replicas       int      `env:"PAYRUDDER_GATEWAY_REPLICAS"`
	AllowedEnvs    []string `env:"PAYRUDDER_ALLOWED_ENVIRONMENTS" envSeparator:","`
	// PublicURL is the address customers reach the gateway at, for the invoice links it hands out (https://api.example.com). Empty: relative links.
	PublicURL string `env:"PAYRUDDER_PUBLIC_URL"`
	// TrustForwardedFor counts the public sign-in limits by X-Forwarded-For, for requests that come from a private address (the console's Worker or proxy).
	TrustForwardedFor bool `env:"PAYRUDDER_TRUST_FORWARDED_FOR"`
	// TrustedProxies (CIDRs, comma separated) are networks whose X-Forwarded-For is believed even from a public address. Empty by default.
	TrustedProxies []string `env:"PAYRUDDER_TRUSTED_PROXIES" envSeparator:","`
	// TrustedProxySecret is the shared secret an edge proxy (the console's Worker) sends as X-Edge-Auth to have its X-Forwarded-For believed. A secret:
	// environment only. Prefer it for Cloudflare, whose Worker egress addresses are not stable.
	TrustedProxySecret string `env:"PAYRUDDER_TRUSTED_PROXY_SECRET"`
	// OperatorEmails lists the addresses of the platform operators (comma separated). A person who signs in with one of them and has verified it may use the
	// operator console. Empty: nobody does; the machine token (PAYRUDDER_OPERATOR_TOKEN) is still the credential of the API.
	OperatorEmails []string `env:"PAYRUDDER_OPERATOR_EMAILS" envSeparator:","`
	// OperatorSupportEmails lists the read-only support operators (PAYRUDDER_OPERATOR_SUPPORT_EMAILS, comma separated): they see every operator page and change nothing. An
	// address on both lists is a support operator (the more restrictive role wins).
	OperatorSupportEmails []string `env:"PAYRUDDER_OPERATOR_SUPPORT_EMAILS" envSeparator:","`
	// OperatorRequire2FA (PAYRUDDER_OPERATOR_REQUIRE_2FA, on/off) makes an operator's session need two-factor sign-in. Unset: on unless PAYRUDDER_ENV is local, ci or kind
	// (a compose or Helm install on a real host runs as one of those: set it there).
	OperatorRequire2FA string `env:"PAYRUDDER_OPERATOR_REQUIRE_2FA"`
	// AppURL is the console's public address: the browser is sent back to it after "Continue with Google", and the Google redirect address defaults to
	// <AppURL>/gw/auth/google/callback.
	AppURL string `env:"PAYRUDDER_APP_URL"`
	// OAuthResources (PAYRUDDER_OAUTH_RESOURCES, comma separated) are the canonical addresses of the MCP servers that may ask for tokens (docs/plans/20-mcp.md, "OAuth design");
	// setting any turns the gateway into an OAuth authorization server at /oauth/*, and needs PAYRUDDER_PUBLIC_URL (the issuer, without a path), PAYRUDDER_APP_URL (the
	// consent page) and OAuthResourceSecret (at least 32 characters, environment only), which the MCP server presents to introspect and to exchange a token.
	// OAuthAllowLive (PAYRUDDER_OAUTH_ALLOW_LIVE, on/off, default off) lets a person consent to an assistant acting on a live environment.
	OAuthResources      []string `env:"PAYRUDDER_OAUTH_RESOURCES" envSeparator:","`
	OAuthResourceSecret string   `env:"PAYRUDDER_OAUTH_RESOURCE_SECRET"`
	OAuthAllowLive      string   `env:"PAYRUDDER_OAUTH_ALLOW_LIVE"`
	// GoogleClientID and GoogleClientSecret register this deployment at Google. Both set: the Google button is shown. The secret is never in YAML or a log.
	GoogleClientID     string `env:"PAYRUDDER_GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"PAYRUDDER_GOOGLE_CLIENT_SECRET"`
	// GoogleRedirectURL overrides <AppURL>/gw/auth/google/callback when the browser reaches the gateway at another address than the console.
	GoogleRedirectURL string `env:"PAYRUDDER_GOOGLE_REDIRECT_URL"`
	// GoogleIssuer is the OpenID provider's address. Google's by default; development points it at the in-repo mock (pkg/oidctest/cmd/mockoidc).
	GoogleIssuer string `env:"PAYRUDDER_GOOGLE_ISSUER"`
	// InboundDomain is the domain invoices are forwarded to (invoices+<token>@<domain>); InboundSecret is the secret of the mail provider's webhook to /inbound/invoices
	// (environment only). Without the secret the endpoint is off.
	InboundDomain string `env:"PAYRUDDER_INBOUND_DOMAIN"`
	InboundSecret string `env:"PAYRUDDER_INBOUND_SECRET"`
	// MailWebhookSecret is the signing secret ("whsec_...", Svix) of the mail provider's webhook of delivery events (bounces, complaints) to /inbound/mail-events, which
	// the gateway translates and hands to the notifier; it needs PAYRUDDER_NOTIFIER_EVENTS_URL.
	MailWebhookSecret string `env:"PAYRUDDER_MAIL_WEBHOOK_SECRET"`

	// Saas is Payrudder as a SaaS (phase 55, ADR 0024): on shows organisations their plan and limits their ingest volume, off (the default) does neither.
	// Set it the same as plans-svc.
	Saas string `env:"PAYRUDDER_SAAS"`

	// Region pins this gateway to one region (plan 17, docs/runbooks/scaling.md): it serves only the tenants whose home region (tenants.region) it is, and answers
	// 421 gateway.region.wrong to the others. Empty (the default) serves every tenant. RegionURLs gives the public address of the gateway of each region, for the
	// hint header of that answer: eu=https://eu.api.example.com,us=https://us.api.example.com.
	Region     string            `env:"PAYRUDDER_REGION"`
	RegionURLs map[string]string `env:"PAYRUDDER_REGION_URLS" envSeparator:"," envKeyValSeparator:"="`
}

// validateOAuth checks the OAuth settings: all or none. The issuer is the public address with no path (RFC 8414 puts the metadata at the root of it).
func (c *Config) validateOAuth() (errs []error) {
	if _, err := config.ParseSwitch("PAYRUDDER_OAUTH_ALLOW_LIVE", c.OAuthAllowLive); err != nil {
		errs = append(errs, err)
	}
	if len(c.OAuthResources) == 0 {
		return errs
	}
	if len(c.OAuthResourceSecret) < 32 {
		errs = append(errs, errors.New("PAYRUDDER_OAUTH_RESOURCE_SECRET must be at least 32 characters when PAYRUDDER_OAUTH_RESOURCES is set"))
	}
	for name, v := range map[string]string{"PAYRUDDER_PUBLIC_URL": c.PublicURL, "PAYRUDDER_APP_URL": c.AppURL} {
		if u, err := url.Parse(v); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			errs = append(errs, fmt.Errorf("%s must be an http(s) address when PAYRUDDER_OAUTH_RESOURCES is set", name))
		}
	}
	if u, err := url.Parse(c.PublicURL); err == nil && strings.Trim(u.Path, "/") != "" {
		errs = append(errs, errors.New("PAYRUDDER_PUBLIC_URL must have no path when PAYRUDDER_OAUTH_RESOURCES is set: it is the issuer"))
	}
	for _, r := range c.OAuthResources {
		if _, ok := oauth.Resource(r); !ok {
			errs = append(errs, fmt.Errorf("PAYRUDDER_OAUTH_RESOURCES: %q is not an https address without a fragment or query", r))
		}
	}
	return errs
}

// oauth is the authorization server's settings for the HTTP layer; nil with no resources.
func (c *Config) oauth() *httpapi.OAuth {
	if len(c.OAuthResources) == 0 {
		return nil
	}
	resources := make([]string, 0, len(c.OAuthResources))
	for _, r := range c.OAuthResources {
		canonical, _ := oauth.Resource(strings.TrimSpace(r))
		resources = append(resources, canonical)
	}
	live, _ := config.ParseSwitch("PAYRUDDER_OAUTH_ALLOW_LIVE", c.OAuthAllowLive)
	return &httpapi.OAuth{Issuer: strings.TrimRight(c.PublicURL, "/"), AppURL: strings.TrimRight(c.AppURL, "/"), Resources: resources, ResourceSecret: c.OAuthResourceSecret, AllowLive: live,
		Client: safehttp.NewClient(5*time.Second, false)}
}

// regionName is the shape of a region (and a cell) name in plans-svc's directory.
var regionName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// operatorRequireTwoFactor is whether operators must have two-factor sign-in on: the setting, or by default everything but a development, test or kind install.
func (c *Config) operatorRequireTwoFactor() bool {
	if strings.TrimSpace(c.OperatorRequire2FA) != "" {
		on, _ := config.ParseSwitch("PAYRUDDER_OPERATOR_REQUIRE_2FA", c.OperatorRequire2FA)
		return on
	}
	switch c.Env {
	case config.EnvLocal, config.EnvCI, config.EnvKind:
		return false
	}
	return true
}

func (c *Config) Validate() error {
	errs := []error{c.Base.Validate(), c.CheckSecret("PAYRUDDER_OPERATOR_TOKEN", c.OperatorToken), c.CheckSecret("PAYRUDDER_PAYMENTS_INTERNAL_TOKEN", c.PaymentsToken),
		c.CheckSecret("PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET", c.NotifierSecret)}
	if _, err := config.ParseSwitch("PAYRUDDER_OPERATOR_REQUIRE_2FA", c.OperatorRequire2FA); err != nil {
		errs = append(errs, err)
	}
	if len(c.OperatorToken) < 32 {
		errs = append(errs, errors.New("PAYRUDDER_OPERATOR_TOKEN must be at least 32 characters"))
	}
	if c.Replicas < 1 {
		errs = append(errs, errors.New("PAYRUDDER_GATEWAY_REPLICAS must be ≥ 1"))
	}
	if (c.GoogleClientID == "") != (c.GoogleClientSecret == "") {
		errs = append(errs, errors.New("PAYRUDDER_GOOGLE_CLIENT_ID and PAYRUDDER_GOOGLE_CLIENT_SECRET must be set together"))
	}
	if c.GoogleClientID != "" {
		for name, v := range map[string]string{"PAYRUDDER_APP_URL": c.AppURL, "PAYRUDDER_GOOGLE_ISSUER": c.GoogleIssuer} {
			if u, err := url.Parse(v); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				errs = append(errs, fmt.Errorf("%s must be an http(s) address when Google sign-in is set up", name))
			}
		}
	}
	if _, err := config.ParseSwitch("PAYRUDDER_SAAS", c.Saas); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, c.validateOAuth()...)
	if _, err := parseProxies(c.TrustedProxies); err != nil {
		errs = append(errs, err)
	}
	if c.Region != "" && !regionName.MatchString(c.Region) {
		errs = append(errs, errors.New("PAYRUDDER_REGION must be a region name: lower-case letters, digits and hyphens, at most 31 characters"))
	}
	for name, raw := range c.RegionURLs {
		if u, err := url.Parse(raw); !regionName.MatchString(name) || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs = append(errs, fmt.Errorf("PAYRUDDER_REGION_URLS: %q=%q is not a region name and an http(s) address", name, raw))
		}
	}
	if (c.NotifierURL == "") != (c.NotifierSecret == "") {
		errs = append(errs, errors.New("PAYRUDDER_NOTIFIER_EVENTS_URL and PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET are set together or not at all"))
	}
	if c.MailWebhookSecret != "" {
		if err := mailx.ValidSvixSecret(c.MailWebhookSecret); err != nil {
			errs = append(errs, fmt.Errorf("PAYRUDDER_MAIL_WEBHOOK_SECRET: %w", err))
		}
		if c.NotifierURL == "" {
			errs = append(errs, errors.New("PAYRUDDER_MAIL_WEBHOOK_SECRET needs PAYRUDDER_NOTIFIER_EVENTS_URL: a delivery event is handed to the notifier"))
		}
	}
	if c.TrustedProxySecret != "" && len(c.TrustedProxySecret) < 32 {
		errs = append(errs, errors.New("PAYRUDDER_TRUSTED_PROXY_SECRET must be at least 32 characters"))
	}
	return errors.Join(errs...)
}

// parseProxies reads PAYRUDDER_TRUSTED_PROXIES: CIDRs, or single addresses (taken as /32 or /128).
func parseProxies(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, raw := range list {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			a, aerr := netip.ParseAddr(raw)
			if aerr != nil {
				return nil, fmt.Errorf("PAYRUDDER_TRUSTED_PROXIES: %q is not a CIDR or an address", raw)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

func main() { app.Main("gateway", defaults, start) }

func start(ctx context.Context, cfg *Config, rt *app.Runtime) error {
	log := rt.Log
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("PAYRUDDER_REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()

	up, err := dialUpstreams(cfg)
	if err != nil {
		return err
	}
	defer up.close()

	api, err := newAPI(cfg, log, rdb, up)
	if err != nil {
		return err
	}
	hh := rt.Health()
	addReadinessChecks(hh, up)
	return serve(ctx, cfg, log, api, hh)
}

// upstreams are the gateway's gRPC connections, one per backend service.
type upstreams struct {
	plans, ingest, aggregator, limits, billing, analytics, assets *grpc.ClientConn
}

// dialUpstreams connects to every backend; on failure it closes the connections already made.
func dialUpstreams(cfg *Config) (u *upstreams, err error) {
	u = &upstreams{}
	defer func() {
		if err != nil {
			u.close()
		}
	}()
	stream := grpc.WithChainStreamInterceptor(tenant.StreamClientInterceptor())
	targets := []struct {
		dst  **grpc.ClientConn
		name string
		addr string
		opts []grpc.DialOption
	}{
		{&u.plans, "plans-svc", cfg.PlansAddr, []grpc.DialOption{stream}},
		{&u.ingest, "ingest-svc", cfg.IngestAddr, nil},
		{&u.aggregator, "aggregator", cfg.AggregatorAddr, nil},
		{&u.limits, "limits-svc", cfg.LimitsAddr, nil},
		{&u.billing, "billing-svc", cfg.BillingAddr, nil},
		{&u.analytics, "analytics-svc", cfg.AnalyticsAddr, nil},
		{&u.assets, "assets-svc", cfg.AssetsAddr, []grpc.DialOption{stream}},
	}
	for _, t := range targets {
		conn, dialErr := grpcx.NewTenantClient(t.name, t.addr, t.opts...)
		if dialErr != nil {
			return u, dialErr
		}
		*t.dst = conn
	}
	return u, nil
}

// close closes the connections that were opened, in reverse dial order.
func (u *upstreams) close() {
	for _, c := range []*grpc.ClientConn{u.assets, u.analytics, u.billing, u.limits, u.aggregator, u.ingest, u.plans} {
		if c != nil {
			_ = c.Close()
		}
	}
}

// newAPI wires auth, the rate limiter and the backend clients into the HTTP handler.
func newAPI(cfg *Config, log *slog.Logger, rdb *redis.Client, up *upstreams) (*httpapi.Server, error) {
	adminClient := plansv1.NewAdminServiceClient(up.plans)
	authn, err := auth.New(auth.NewRedisCache(rdb), auth.AdminResolver{Client: adminClient}, cfg.AllowedEnvs, log)
	if err != nil {
		return nil, err
	}
	authn.WithToucher(auth.AdminToucher{Client: adminClient})
	limiter, err := ratelimit.NewLimiter(rdb, cfg.Replicas, log)
	if err != nil {
		return nil, err
	}
	var paymentsURL *url.URL
	if cfg.PaymentsURL != "" {
		if paymentsURL, err = url.Parse(cfg.PaymentsURL); err != nil {
			return nil, fmt.Errorf("PAYRUDDER_PAYMENTS_URL: %w", err)
		}
	}
	logMailWebhook(log, cfg)
	var mail httpapi.MailAdmin
	if cfg.NotifierURL != "" {
		c, err := mailadmin.New(cfg.NotifierURL, cfg.NotifierSecret, nil)
		if err != nil {
			return nil, fmt.Errorf("PAYRUDDER_NOTIFIER_EVENTS_URL: %w", err)
		}
		mail = c
	}
	proxies, err := parseProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}
	appURL := strings.TrimRight(cfg.AppURL, "/")
	var google *oidc.Client
	if cfg.GoogleClientID != "" {
		redirect := cfg.GoogleRedirectURL
		if redirect == "" {
			redirect = appURL + "/gw/auth/google/callback"
		}
		google = oidc.New(oidc.Config{Issuer: cfg.GoogleIssuer, ClientID: cfg.GoogleClientID, ClientSecret: cfg.GoogleClientSecret, RedirectURL: redirect})
	}
	var saas plansv1.SaasServiceClient
	if on, _ := config.ParseSwitch("PAYRUDDER_SAAS", cfg.Saas); on {
		saas = plansv1.NewSaasServiceClient(up.plans)
	}
	var region *httpapi.Region
	if cfg.Region != "" {
		urls := make(map[string]string, len(cfg.RegionURLs))
		for name, u := range cfg.RegionURLs {
			urls[name] = strings.TrimRight(u, "/")
		}
		region = &httpapi.Region{Name: cfg.Region, URLs: urls, Directory: tenantdir.NewGRPC(plansv1.NewDirectoryServiceClient(up.plans), tenantdir.Options{})}
	}
	return httpapi.New(httpapi.Deps{
		Region:         region,
		Saas:           saas,
		Mail:           mail,
		OperatorEmails: httpapi.NormalizeOperatorEmails(cfg.OperatorEmails), OperatorSupportEmails: httpapi.NormalizeOperatorEmails(cfg.OperatorSupportEmails), OperatorRequireTwoFactor: cfg.operatorRequireTwoFactor(), Google: google, AppURL: appURL, AuthFailures: authn.Failures(), OAuth: cfg.oauth(),
		Plans: plansv1.NewPlansServiceClient(up.plans), Admin: adminClient, Webhooks: plansv1.NewWebhookServiceClient(up.plans), MailIntegration: plansv1.NewMailIntegrationServiceClient(up.plans), Support: plansv1.NewSupportServiceClient(up.plans), AuditPlans: auditv1.NewAuditServiceClient(up.plans), AuditBilling: auditv1.NewAuditServiceClient(up.billing), Contracts: plansv1.NewContractServiceClient(up.plans), Affiliates: plansv1.NewAffiliateServiceClient(up.plans), AffiliatePub: plansv1.NewAffiliatePublicServiceClient(up.plans), AffiliatePortal: plansv1.NewAffiliatePortalServiceClient(up.plans), Ingest: ingestv1.NewIngestServiceClient(up.ingest),
		Usage: aggregatorv1.NewUsageQueryServiceClient(up.aggregator), Replay: aggregatorv1.NewReplayServiceClient(up.aggregator), Limits: limitsv1.NewLimitsServiceClient(up.limits),
		Billing: billingv1.NewBillingServiceClient(up.billing), Sandbox: billingv1.NewSandboxServiceClient(up.billing),
		BillingAdmin: billingv1.NewBillingAdminServiceClient(up.billing), Hosted: billingv1.NewHostedInvoiceServiceClient(up.billing), PublicURL: strings.TrimRight(cfg.PublicURL, "/"), Analytics: analyticsv1.NewAnalyticsServiceClient(up.analytics),
		Assets: assetsv1.NewAssetsServiceClient(up.assets),
		Auth:   authn, Limiter: limiter, OperatorToken: cfg.OperatorToken, Log: log,
		PaymentsURL: paymentsURL, PaymentsToken: cfg.PaymentsToken, Demo: cfg.Demo, InboundDomain: cfg.InboundDomain, InboundSecret: cfg.InboundSecret, MailWebhookSecret: cfg.MailWebhookSecret,
		Identity: plansv1.NewIdentityServiceClient(up.plans), TrustForwardedFor: cfg.TrustForwardedFor,
		TrustedProxies: proxies, TrustedProxySecret: cfg.TrustedProxySecret,
	}), nil
}

// logMailWebhook says at start when the mail provider's delivery webhook is off: its route answers 404 then, and nothing else shows that bounces and complaints never reach the
// suppression list.
func logMailWebhook(log *slog.Logger, cfg *Config) {
	if cfg.MailWebhookSecret == "" {
		log.Info("the mail provider's delivery webhook (/inbound/mail-events) is off, so bounces and complaints do not reach the suppression list: set PAYRUDDER_MAIL_WEBHOOK_SECRET (and PAYRUDDER_NOTIFIER_EVENTS_URL) to switch it on")
	}
}

// addReadinessChecks registers what readiness depends on.
// Readiness doesn't include Redis: the gateway serves (fail-open limits, plans-svc lookups) without it.
func addReadinessChecks(hh *health.Handler, up *upstreams) {
	adminClient := plansv1.NewAdminServiceClient(up.plans)
	hh.AddCheck("plans-svc", func(ctx context.Context) error {
		_, err := adminClient.ResolveApiKey(ctx, &plansv1.ResolveApiKeyRequest{Prefix: "00000000"})
		if err != nil && grpcCodeIsUp(err) {
			return nil
		}
		return err
	})
}

// serve runs the HTTP and metrics servers until ctx is cancelled or one of them fails, then shuts down gracefully.
func serve(ctx context.Context, cfg *Config, log *slog.Logger, api http.Handler, hh *health.Handler) error {
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: withLiveness(api, hh), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
	g := app.NewGroup(ctx, log, cfg.MetricsAddr, hh)
	gctx := g.Context()
	g.Go(func() error {
		log.Info("http listening", "addr", cfg.HTTPAddr, "plans", cfg.PlansAddr, "metrics", cfg.MetricsAddr)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	g.Go(func() error {
		<-gctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(c)
	})
	return g.Wait()
}
