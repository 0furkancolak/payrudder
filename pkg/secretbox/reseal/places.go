package reseal

import "github.com/0furkancolak/payrudder/pkg/payments"

// The columns that hold boxes, by service, and the associated data each was sealed with. The data is written twice, here and where the service seals: the integration
// tests of payments-worker and plans-svc store a box through the service's own writer and have the job open it, so a service that changes its data breaks them, not a
// rotation. A new place that calls Seal needs its line here and a case in those tests (docs/runbooks/secrets-key-rotation.md).
var (
	tenantID    = KeyColumn{"tenant_id", "uuid"}
	environment = KeyColumn{"environment", "text"}
	provider    = KeyColumn{"provider", "text"}
)

// Payments are the boxes of payments-worker. The provider column holds the account key ("stripe", "stripe~eu").
var Payments = []Column{
	{Table: "provider_accounts", Column: "settings_enc", Tenant: true, Key: []KeyColumn{tenantID, provider, environment},
		AAD: func(k []string) []byte { return []byte(k[0] + "|" + k[1] + "|" + k[2]) }},
	{Table: "einvoice_accounts", Column: "settings_enc", Tenant: true, Key: []KeyColumn{tenantID, environment, provider},
		AAD: func(k []string) []byte {
			if payments.AccountLabel(k[2]) == "" {
				return []byte(k[0] + "|einvoice|" + k[1]) // the form of the one account there was before labels
			}
			return []byte(k[0] + "|einvoice|" + k[1] + "|" + k[2])
		}},
}

// Plans are the boxes of plans-svc. user_totp belongs to a person, not to a tenant: the pooled database only.
var Plans = []Column{
	{Table: "webhook_endpoints", Column: "secret_enc", Tenant: true, Key: []KeyColumn{tenantID, {"id", "uuid"}}, AAD: webhookAAD},
	{Table: "webhook_endpoints", Column: "prev_secret_enc", Tenant: true, Key: []KeyColumn{tenantID, {"id", "uuid"}}, AAD: webhookAAD},
	{Table: "tenant_mail_integrations", Column: "api_key_enc", Tenant: true, Key: []KeyColumn{tenantID, provider},
		AAD: func(k []string) []byte {
			if payments.AccountLabel(k[1]) == "" {
				return []byte("mail-integration:" + k[0]) // the form of the one account there was before labels
			}
			return []byte("mail-integration:" + k[0] + ":" + k[1])
		}},
	{Table: "user_totp", Column: "secret_enc", Key: []KeyColumn{{"user_id", "uuid"}}, AAD: func(k []string) []byte { return []byte("totp|" + k[0]) }},
}

func webhookAAD(k []string) []byte { return []byte("webhook:" + k[1]) }
