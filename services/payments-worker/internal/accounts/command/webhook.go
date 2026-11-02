package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// RegisterWebhook has the provider create the endpoint its notifications go to (payments.WebhookRegistrar), at url, and puts the signing secret it shows into the
// stored settings of the account, sealed like the rest and never echoed back. The credentials' own mode must be the environment's, so a live endpoint is never made
// from a sandbox account or the other way round. It answers the endpoint's id. A provider whose adapter cannot do it is payments.ErrUnsupported (409).
func RegisterWebhook(ctx context.Context, d Deps, tenant uuid.UUID, provider, environment, endpointURL, actor string) (string, error) {
	if u, err := url.Parse(endpointURL); err != nil || u.Scheme != "https" || u.Host == "" {
		return "", refuse.New(http.StatusBadRequest, "payments.account.bad_settings", "the webhook address must be an https:// URL")
	}
	settings, _, err := d.Accounts.Settings(ctx, tenant, provider, environment)
	if errors.Is(err, store.ErrNoAccount) {
		return "", refuse.New(http.StatusNotFound, "payments.account.not_found", "no such account")
	}
	if err != nil {
		return "", err
	}
	built, err := d.Registry.Build(provider, settings)
	if err != nil {
		return "", refuse.New(http.StatusBadRequest, "payments.account.bad_settings", err.Error())
	}
	if err := payments.CheckLivemode(built, environment); err != nil {
		return "", refuse.New(http.StatusBadRequest, "payments.account.livemode_mismatch", err.Error())
	}
	reg, ok := built.(payments.WebhookRegistrar)
	if !ok {
		return "", &refuse.Provider{What: "registering the webhook endpoint", Err: fmt.Errorf("%w: %s", payments.ErrUnsupported, provider)}
	}
	ep, err := reg.RegisterWebhook(ctx, payments.WebhookRegistration{URL: endpointURL})
	if err != nil {
		return "", &refuse.Provider{What: "registering the webhook endpoint", Err: err}
	}
	// The secret is shown once: it is stored before anything else can fail, in the settings the account already has.
	var all map[string]any
	if err := json.Unmarshal(settings, &all); err != nil {
		return "", fmt.Errorf("stored settings of %s: %w", provider, err)
	}
	all["webhook_secret"] = ep.Secret
	merged, err := json.Marshal(all)
	if err != nil {
		return "", err
	}
	name, hasDefault, err := d.Accounts.Default(ctx, tenant, environment)
	if err != nil {
		return "", err
	}
	if err := d.Accounts.PutAudited(ctx, tenant, provider, environment, merged, hasDefault && name == provider, store.Audit{Actor: actor}); err != nil {
		d.Log.ErrorContext(ctx, "webhook endpoint created but its secret was not stored", "provider", provider, "endpoint", ep.ID, "err", err)
		return "", fmt.Errorf("provider account write failed: %w", err)
	}
	return ep.ID, nil
}
