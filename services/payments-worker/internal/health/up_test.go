package health

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// The reader is set as the global provider when the package loads, before the gauge's callback is registered with the global meter: OpenTelemetry's global delegation hands
// the callback to it.
var upReader = func() *sdkmetric.ManualReader {
	r := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(r)))
	return r
}()

// scraped is provider_account_up as a scrape would read it: account -> value, with the provider label of each.
func scraped(t *testing.T) (value map[string]int64, provider map[string]string) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := upReader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	value, provider = map[string]int64{}, map[string]string{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			g, ok := m.Data.(metricdata.Gauge[int64])
			if !ok || m.Name != "provider_account_up" {
				continue
			}
			for _, dp := range g.DataPoints {
				acc, _ := dp.Attributes.Value("account")
				prov, _ := dp.Attributes.Value("provider")
				value[acc.AsString()], provider[acc.AsString()] = dp.Value, prov.AsString()
			}
		}
	}
	return value, provider
}

// provider_account_up is 1 for a live account whose provider answered with the key accepted, 0 for one unreachable or whose key was refused, and absent for what has no
// signal (never checked, nothing to ask, a sandbox account) or no longer exists: absent must never read as healthy.
func TestProviderAccountUpFollowsTheLastCheckOfLiveAccounts(t *testing.T) {
	tenant := uuid.New()
	ref := func(provider, environment string) Ref {
		return Ref{Tenant: tenant, Provider: provider, Environment: environment}
	}
	a, b, c, d, sandbox := ref("stripe", "live"), ref("stripe~eu", "live"), ref("iyzico", "live"), ref("banktransfer", "live"), ref("stripe", "sandbox")
	t.Cleanup(func() { accountUp.keep(nil) })

	accountUp.set(a, store.HealthHealthy)
	accountUp.set(b, store.HealthDown)
	accountUp.set(c, store.HealthBadCredentials)
	accountUp.set(d, store.HealthUnknown)
	accountUp.set(sandbox, store.HealthDown)
	got, prov := scraped(t)
	want := map[string]int64{tenant.String() + "/stripe": 1, tenant.String() + "/stripe~eu": 0, tenant.String() + "/iyzico": 0}
	if len(got) != len(want) {
		t.Fatalf("series = %v, want %v (an unchecked, unchecked-able or sandbox account has none)", got, want)
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s = %v (present %v), want %d", k, g, ok, v)
		}
	}
	if prov[tenant.String()+"/stripe~eu"] != "stripe" {
		t.Errorf("provider label of a labelled account = %q, want the base provider", prov[tenant.String()+"/stripe~eu"])
	}

	accountUp.set(b, store.HealthHealthy) // it came back
	accountUp.set(a, store.HealthUnknown) // its adapter was replaced by one with no remote call: no signal, not "healthy"
	got, _ = scraped(t)
	if got[tenant.String()+"/stripe~eu"] != 1 || len(got) != 2 {
		t.Errorf("after b came back and a lost its signal: %v", got)
	}

	accountUp.keep([]Ref{b}) // the other accounts were removed
	got, _ = scraped(t)
	if len(got) != 1 || got[tenant.String()+"/stripe~eu"] != 1 {
		t.Errorf("after the removed accounts were forgotten: %v, want only stripe~eu", got)
	}
}
