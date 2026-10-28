package httpapi

import (
	"slices"

	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

func (s *Server) routes() []Route {
	return slices.Concat(
		s.ingestAndCatalogRoutes(),
		s.subscriptionTaxAndCouponRoutes(),
		s.promotionRoutes(),
		s.affiliateRoutes(),
		s.affiliatePayoutRoutes(),
		s.affiliateLinkRoutes(),
		s.affiliatePortalRoutes(),
		s.webhookAndInvoiceLinkRoutes(),
		s.paymentLinkRoutes(),
		s.hostedPublicRoutes(),
		s.reportRoutingAndMailRoutes(),
		s.creditRoutes(),
		s.accountingRoutes(),
		s.inboxRoutes(),
		s.assetRoutes(),
		s.billingReadAndSandboxRoutes(),
		s.appRoutes(),
		s.onboardingRoutes(),
		s.goLiveRoutes(),
		s.integrationRoutes(),
		s.sandboxRoutes(),
		s.sandboxProviderRoutes(),
		s.memberRoutes(),
		s.geoRoutes(),
		s.operatorProviderRoutes(),
		s.operatorAdminRoutes(),
		s.saasRoutes(),
	)
}

// ingestAndCatalogRoutes is the table for usage events, customers, meters and plans.
func (s *Server) ingestAndCatalogRoutes() []Route {
	return []Route{
		// Usage events
		{"POST", "/v1/events", AccessTenant, ratelimit.ClassIngest, s.ingestEvents},
		// Customers
		{"POST", "/v1/customers", AccessTenant, ratelimit.ClassWrite, s.createCustomer},
		{"GET", "/v1/customers", AccessTenant, ratelimit.ClassRead, s.listCustomers},
		{"GET", "/v1/customers/{external_id}", AccessTenant, ratelimit.ClassRead, s.getCustomer},
		{"GET", "/v1/customers/{external_id}/usage", AccessTenant, ratelimit.ClassRead, s.getCustomerUsage},
		{"GET", "/v1/customers/{external_id}/entitlements", AccessTenant, ratelimit.ClassRead, s.listEntitlements},
		{"GET", "/v1/customers/{external_id}/entitlements/{meter}", AccessTenant, ratelimit.ClassRead, s.getEntitlement},
		{"GET", "/v1/customers/{external_id}/entitlement-overrides", AccessTenant, ratelimit.ClassRead, s.listEntitlementOverrides},
		{"PUT", "/v1/customers/{external_id}/entitlement-overrides/{meter}", AccessTenant, ratelimit.ClassWrite, s.setEntitlementOverride},
		{"DELETE", "/v1/customers/{external_id}/entitlement-overrides/{meter}", AccessTenant, ratelimit.ClassWrite, s.deleteEntitlementOverride},
		{"PUT", "/v1/customers/{external_id}/payment-method", AccessTenant, ratelimit.ClassWrite, s.setPaymentMethod},
		{"PUT", "/v1/customers/{external_id}/address", AccessTenant, ratelimit.ClassWrite, s.setCustomerAddress},
		{"PUT", "/v1/customers/{external_id}/contact", AccessTenant, ratelimit.ClassWrite, s.setCustomerContact},
		{"PUT", "/v1/customers/{external_id}/tax", AccessTenant, ratelimit.ClassWrite, s.setCustomerTax},
		// Meters
		{"POST", "/v1/meters", AccessTenant, ratelimit.ClassWrite, s.createMeter},
		{"GET", "/v1/meters", AccessTenant, ratelimit.ClassRead, s.listMeters},
		{"GET", "/v1/meters/{code}", AccessTenant, ratelimit.ClassRead, s.getMeter},
		// Plans
		{"POST", "/v1/plans", AccessTenant, ratelimit.ClassWrite, s.createPlan},
		{"GET", "/v1/plans", AccessTenant, ratelimit.ClassRead, s.listPlans},
		{"POST", "/v1/plans/{code}/versions", AccessTenant, ratelimit.ClassWrite, s.publishPlanVersion},
		{"GET", "/v1/plans/{code}/versions/{version}", AccessTenant, ratelimit.ClassRead, s.getPlanVersion},
	}
}

// subscriptionTaxAndCouponRoutes is the table for subscriptions, tax rates and coupons.
func (s *Server) subscriptionTaxAndCouponRoutes() []Route {
	return []Route{
		// Subscriptions
		{"POST", "/v1/subscriptions", AccessTenant, ratelimit.ClassWrite, s.createSubscription},
		{"GET", "/v1/subscriptions", AccessTenant, ratelimit.ClassRead, s.listSubscriptions},
		{"GET", "/v1/subscriptions/{id}", AccessTenant, ratelimit.ClassRead, s.getSubscription},
		{"DELETE", "/v1/subscriptions/{id}", AccessTenant, ratelimit.ClassWrite, s.cancelSubscription},
		{"POST", "/v1/subscriptions/{id}/pause", AccessTenant, ratelimit.ClassWrite, s.pauseSubscription},
		{"POST", "/v1/subscriptions/{id}/resume", AccessTenant, ratelimit.ClassWrite, s.resumeSubscription},
		{"POST", "/v1/subscriptions/{id}/cancel-at-period-end", AccessTenant, ratelimit.ClassWrite, s.scheduleCancellation},
		{"DELETE", "/v1/subscriptions/{id}/cancel-at-period-end", AccessTenant, ratelimit.ClassWrite, s.withdrawCancellation},
		{"POST", "/v1/subscriptions/{id}/change-plan", AccessTenant, ratelimit.ClassWrite, s.changePlan},
		{"POST", "/v1/subscriptions/{id}/change-preview", AccessTenant, ratelimit.ClassRead, s.previewSubscriptionChange},
		{"POST", "/v1/subscriptions/{id}/change", AccessTenant, ratelimit.ClassWrite, s.changeSubscription},
		{"POST", "/v1/subscriptions/{id}/coupon", AccessTenant, ratelimit.ClassWrite, s.applyCoupon},
		{"DELETE", "/v1/subscriptions/{id}/coupon", AccessTenant, ratelimit.ClassWrite, s.removeCoupon},
		{"GET", "/v1/tax-rates", AccessTenant, ratelimit.ClassRead, s.getTaxRates},
		{"PUT", "/v1/tax-rates", AccessTenant, ratelimit.ClassWrite, s.setTaxRates},
		{"POST", "/v1/coupons", AccessTenant, ratelimit.ClassWrite, s.createCoupon},
		{"GET", "/v1/coupons", AccessTenant, ratelimit.ClassRead, s.listCoupons},
		{"DELETE", "/v1/coupons/{code}", AccessTenant, ratelimit.ClassWrite, s.deactivateCoupon},
		{"DELETE", "/v1/subscriptions/{id}/scheduled-change", AccessTenant, ratelimit.ClassWrite, s.cancelScheduledChange},
	}
}

// webhookAndInvoiceLinkRoutes is the table for webhook endpoints and deliveries, and the hosted invoice page.
func (s *Server) webhookAndInvoiceLinkRoutes() []Route {
	return []Route{
		// Webhooks: where Payrudder sends what happens in the account
		{"POST", "/v1/webhook-endpoints", AccessTenant, ratelimit.ClassWrite, s.createWebhookEndpoint},
		{"GET", "/v1/webhook-endpoints", AccessTenant, ratelimit.ClassRead, s.listWebhookEndpoints},
		{"PUT", "/v1/webhook-endpoints/{id}", AccessTenant, ratelimit.ClassWrite, s.updateWebhookEndpoint},
		{"DELETE", "/v1/webhook-endpoints/{id}", AccessTenant, ratelimit.ClassWrite, s.deleteWebhookEndpoint},
		{"POST", "/v1/webhook-endpoints/{id}/rotate-secret", AccessTenant, ratelimit.ClassWrite, s.rotateWebhookSecret},
		{"POST", "/v1/webhook-endpoints/{id}/test", AccessTenant, ratelimit.ClassWrite, s.sendTestWebhook},
		{"GET", "/v1/webhook-deliveries", AccessTenant, ratelimit.ClassRead, s.listWebhookDeliveries},
		{"POST", "/v1/webhook-deliveries/{id}/redeliver", AccessTenant, ratelimit.ClassWrite, s.redeliverWebhook},
		// Hosted invoice page: links the tenant makes, and the public pages they open
		{"POST", "/v1/invoices/{id}/links", AccessTenant, ratelimit.ClassWrite, s.createInvoiceLink},
		{"DELETE", "/v1/invoices/{id}/links", AccessTenant, ratelimit.ClassWrite, s.revokeInvoiceLinks},
	}
}

// reportRoutingAndMailRoutes is the table for reports, dunning settings and payment provider routing.
func (s *Server) reportRoutingAndMailRoutes() []Route {
	return []Route{
		// Reports
		{"GET", "/v1/reports/revenue", AccessTenant, ratelimit.ClassRead, s.revenueReport},
		{"GET", "/v1/dunning-settings", AccessTenant, ratelimit.ClassRead, s.getDunningSettings},
		{"PUT", "/v1/dunning-settings", AccessTenant, ratelimit.ClassWrite, s.setDunningSettings},
		{"GET", "/v1/reports/dunning", AccessTenant, ratelimit.ClassRead, s.dunningReport},
		{"GET", "/v1/reports/subscriptions", AccessTenant, ratelimit.ClassRead, s.subscriptionReport},
		// Payment provider routing
		{"GET", "/v1/routing-rules", AccessTenant, ratelimit.ClassRead, s.getRoutingRules},
		{"PUT", "/v1/routing-rules", AccessTenant, ratelimit.ClassWrite, s.setRoutingRules},
		{"POST", "/v1/routing-rules/preview", AccessTenant, ratelimit.ClassRead, s.previewRouting},
		// Mail switch
		{"GET", "/v1/mail-settings", AccessTenant, ratelimit.ClassRead, s.getMailSettings},
		{"PUT", "/v1/mail-settings", AccessTenant, ratelimit.ClassWrite, s.setMailSettings},
	}
}

// assetRoutes is the table for asset uploads and files.
func (s *Server) assetRoutes() []Route {
	return []Route{
		// Assets: direct multipart sessions, a streamed form upload, and the files themselves
		{"POST", "/v1/assets/uploads", AccessTenant, ratelimit.ClassWrite, s.createUploadSession},
		{"POST", "/v1/assets/uploads/{session}/files/{asset}/parts", AccessTenant, ratelimit.ClassWrite, s.presignParts},
		{"GET", "/v1/assets/uploads/{session}/files/{asset}", AccessTenant, ratelimit.ClassRead, s.getUploadFile},
		{"POST", "/v1/assets/uploads/{session}/files/{asset}/complete", AccessTenant, ratelimit.ClassWrite, s.completeUploadFile},
		{"DELETE", "/v1/assets/uploads/{session}/files/{asset}", AccessTenant, ratelimit.ClassWrite, s.abortUploadFile},
		{"DELETE", "/v1/assets/uploads/{session}", AccessTenant, ratelimit.ClassWrite, s.abortUploadSession},
		{"PUT", "/v1/tenant/invoice-logo", AccessTenant, ratelimit.ClassWrite, s.setInvoiceLogo},
		{"DELETE", "/v1/tenant/invoice-logo", AccessTenant, ratelimit.ClassWrite, s.clearInvoiceLogo},
		{"POST", "/v1/assets", AccessTenant, ratelimit.ClassWrite, s.uploadAssets},
		{"GET", "/v1/assets", AccessTenant, ratelimit.ClassRead, s.listAssets},
		{"GET", "/v1/assets/{id}", AccessTenant, ratelimit.ClassRead, s.getAsset},
		{"GET", "/v1/assets/{id}/download", AccessTenant, ratelimit.ClassRead, s.downloadAsset},
		{"DELETE", "/v1/assets/{id}", AccessTenant, ratelimit.ClassWrite, s.deleteAsset},
	}
}

// billingReadAndSandboxRoutes is the table for analytics, invoices, credit notes and the sandbox.
func (s *Server) billingReadAndSandboxRoutes() []Route {
	return []Route{
		// Analytics (estimates: invoices come from billing)
		{"GET", "/v1/analytics/usage", AccessTenant, ratelimit.ClassRead, s.getAnalyticsUsage},
		{"GET", "/v1/analytics/top-customers", AccessTenant, ratelimit.ClassRead, s.getTopCustomers},
		// Invoices
		{"GET", "/v1/invoices", AccessTenant, ratelimit.ClassRead, s.listInvoices},
		{"GET", "/v1/invoices/{id}", AccessTenant, ratelimit.ClassRead, s.getInvoice},
		{"GET", "/v1/credit-notes", AccessTenant, ratelimit.ClassRead, s.listCreditNotes},
		{"GET", "/v1/credit-notes/{id}", AccessTenant, ratelimit.ClassRead, s.getCreditNote},
		{"GET", "/v1/invoices/{id}/pdf", AccessTenant, ratelimit.ClassRead, s.getInvoicePDF},
		// The payment ledger as the tenant reads it (charges and refunds, folded); the operator's /admin/v1/tenants/{id}/ledger stays operator-only.
		{"GET", "/v1/payments", AccessTenant, ratelimit.ClassRead, s.listPayments},
		{"GET", "/v1/e-invoices", AccessTenant, ratelimit.ClassRead, s.listEInvoices},
		{"GET", "/v1/e-invoices/{id}", AccessTenant, ratelimit.ClassRead, s.getEInvoice},
		{"GET", "/v1/e-invoices/{id}/files/{kind}", AccessTenant, ratelimit.ClassRead, s.getEInvoiceFile},
		// Sandbox
		{"POST", "/v1/sandbox/close-period", AccessSandbox, ratelimit.ClassWrite, s.sandboxClosePeriod},
		{"POST", "/v1/sandbox/customers/{external_id}/payment-scenario", AccessSandbox, ratelimit.ClassWrite, s.setPaymentScenario},
	}
}

// operatorProviderRoutes is the table for operator accounts, provider accounts, provider webhooks and the provider API.
func (s *Server) operatorProviderRoutes() []Route {
	return []Route{
		// Operator
		{"POST", "/admin/v1/accounts", AccessOperator, "", s.createAccount},
		{"POST", "/admin/v1/accounts/{id}/apps", AccessOperator, "", s.createApp},
		{"GET", "/admin/v1/accounts/{id}/apps", AccessOperator, "", s.listApps},
		{"GET", "/admin/v1/tenants", AccessOperator, "", s.adminListTenants},
		{"GET", "/admin/v1/tenants/{tenant_id}/invoices", AccessOperator, "", s.adminListInvoices},
		{"GET", "/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}", AccessOperator, "", s.adminGetInvoice},
		{"GET", "/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/pdf", AccessOperator, "", s.adminInvoicePDF},
		{"GET", "/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/saga", AccessOperator, "", s.adminInvoiceSaga},
		{"GET", "/admin/v1/tenants/{tenant_id}/provider-accounts", AccessOperator, "", s.proxy("/admin/tenants/{tenant_id}/provider-accounts")},
		{"PUT", "/admin/v1/tenants/{tenant_id}/provider-accounts/{provider}", AccessOperator, "", s.proxy("/admin/tenants/{tenant_id}/provider-accounts/{provider}")},
		{"DELETE", "/admin/v1/tenants/{tenant_id}/provider-accounts/{provider}", AccessOperator, "", s.proxy("/admin/tenants/{tenant_id}/provider-accounts/{provider}")},
		{"GET", "/admin/v1/tenants/{tenant_id}/ledger", AccessOperator, "", s.proxy("/admin/tenants/{tenant_id}/ledger")},
		{"GET", "/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/transfer", AccessOperator, "", s.invoiceTransfer()},
		{"POST", "/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/transfer/receipts", AccessOperator, "", s.invoiceTransfer()},
		{"POST", "/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/transfer/reject", AccessOperator, "", s.invoiceTransfer()},
		{"GET", "/admin/v1/tenants/{tenant_id}/reconciliation", AccessOperator, "", s.proxy("/admin/tenants/{tenant_id}/reconciliation")},
		{"POST", "/webhooks/{provider}", AccessPublic, "", s.proxy("/webhooks/{provider}")},
		{"POST", "/webhooks/{provider}/{tenant_id}/{environment}", AccessPublic, "", s.proxy("/webhooks/{provider}/{tenant_id}/{environment}")},
		{"GET", "/admin/v1/tenants/{tenant_id}/provider-subscriptions", AccessOperator, "", s.proxy("/admin/tenants/{tenant_id}/provider-subscriptions")},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/catalogue/sync", AccessOperator, "", s.providerAPI()},
		{"GET", "/admin/v1/tenants/{tenant_id}/providers/{provider}/catalogue", AccessOperator, "", s.providerAPI()},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/customers/{external_id}", AccessOperator, "", s.providerAPI()},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/customers/{external_id}/portal", AccessOperator, "", s.providerAPI()},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/change-preview", AccessOperator, "", s.providerAPI()},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/change", AccessOperator, "", s.providerAPI()},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/pause", AccessOperator, "", s.providerAPI()},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/resume", AccessOperator, "", s.providerAPI()},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/payment-method-link", AccessOperator, "", s.providerAPI()},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/discounts", AccessOperator, "", s.providerAPI()},
		{"GET", "/admin/v1/tenants/{tenant_id}/providers/{provider}/transactions/{ref}/invoice", AccessOperator, "", s.providerAPI()},
		{"POST", "/admin/v1/tenants/{tenant_id}/checkouts", AccessOperator, "", s.proxy("/admin/tenants/{tenant_id}/checkouts")},
		{"POST", "/admin/v1/tenants/{tenant_id}/provider-migrations", AccessOperator, "", s.proxy("/admin/tenants/{tenant_id}/migrations")},
		{"POST", "/admin/v1/tenants/{tenant_id}/provider-migrations/{migration_id}/abort", AccessOperator, "", s.proxy("/admin/tenants/{tenant_id}/migrations/{migration_id}/abort")},
	}
}

// operatorAdminRoutes is the table for operator dead letters, tenants, API keys, billing runs, replays, rerating and invoice actions.
func (s *Server) operatorAdminRoutes() []Route {
	return []Route{
		{"GET", "/admin/v1/dead-letters", AccessOperator, "", s.adminListDeadLetters},
		{"POST", "/admin/v1/dead-letters/redrive", AccessOperator, "", s.adminRedriveDeadLetters},
		{"GET", "/admin/v1/tenants/{id}", AccessOperator, "", s.getTenant},
		{"GET", "/admin/v1/tenants/{tenant_id}/dashboard", AccessOperator, "", s.adminTenantDashboard},
		{"PATCH", "/admin/v1/tenants/{id}", AccessOperator, "", s.updateTenant},
		{"POST", "/admin/v1/tenants/{id}/api-keys", AccessOperator, "", s.createAPIKey},
		{"DELETE", "/admin/v1/api-keys/{id}", AccessOperator, "", s.revokeAPIKey},
		{"GET", "/admin/v1/analytics/ingest-stats", AccessOperator, "", s.adminIngestStats},
		{"POST", "/admin/v1/billing/close-period", AccessOperator, "", s.adminClosePeriod},
		{"GET", "/admin/v1/tenants/{tenant_id}/billing-runs/{period}", AccessOperator, "", s.adminGetBillingRun},
		{"POST", "/admin/v1/tenants/{tenant_id}/replays", AccessOperator, "", s.adminStartReplay},
		{"GET", "/admin/v1/tenants/{tenant_id}/replays/{job_id}", AccessOperator, "", s.adminGetReplay},
		{"POST", "/admin/v1/tenants/{tenant_id}/replays/{job_id}/promote", AccessOperator, "", s.adminPromoteReplay},
		{"POST", "/admin/v1/tenants/{tenant_id}/replays/{job_id}/discard", AccessOperator, "", s.adminDiscardReplay},
		{"POST", "/admin/v1/tenants/{tenant_id}/billing-runs/{period}/rerating", AccessOperator, "", s.adminStartRerating},
		{"GET", "/admin/v1/tenants/{tenant_id}/rerating-jobs/{job_id}", AccessOperator, "", s.adminGetRerating},
		{"POST", "/admin/v1/invoices/{id}/retry", AccessOperator, "", s.adminRetryInvoice},
		{"POST", "/admin/v1/invoices/{id}/void", AccessOperator, "", s.adminVoidInvoice},
		{"POST", "/admin/v1/invoices/{id}/payments", AccessOperator, "", s.adminRecordInvoicePayment},
		{"POST", "/admin/v1/invoices/{id}/credit-notes", AccessOperator, "", s.adminCreateCreditNote},
		{"POST", "/admin/v1/credit-notes/{id}/retry-refund", AccessOperator, "", s.adminRetryCreditNoteRefund},
		{"GET", "/admin/v1/tenants/{tenant_id}/e-invoices", AccessOperator, "", s.adminListEInvoices},
		{"GET", "/admin/v1/tenants/{tenant_id}/e-invoices/{id}", AccessOperator, "", s.adminGetEInvoice},
		{"GET", "/admin/v1/tenants/{tenant_id}/e-invoices/{id}/files/{kind}", AccessOperator, "", s.adminGetEInvoiceFile},
		{"POST", "/admin/v1/tenants/{tenant_id}/e-invoices/{id}/retry", AccessOperator, "", s.adminRetryEInvoice},
		{"POST", "/admin/v1/tenants/{tenant_id}/e-invoices/{id}/cancel", AccessOperator, "", s.adminCancelEInvoice},
		{"GET", "/admin/v1/tenants/{tenant_id}/e-invoice-accounts", AccessOperator, "", s.proxy("/admin/tenants/{tenant_id}/e-invoice-accounts")},
		{"PUT", "/admin/v1/tenants/{tenant_id}/e-invoice-accounts/{environment}", AccessOperator, "", s.proxy("/admin/tenants/{tenant_id}/e-invoice-accounts/{environment}")},
		{"DELETE", "/admin/v1/tenants/{tenant_id}/e-invoice-accounts/{environment}", AccessOperator, "", s.proxy("/admin/tenants/{tenant_id}/e-invoice-accounts/{environment}")},
	}
}
