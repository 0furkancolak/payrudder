package httpapi

import "net/http"

// opClass says whether an /admin/v1 route reads or writes, for the role operator_support (read-only). A GET or HEAD route reads. Every other route is listed here by
// "METHOD path" and classed on purpose: a POST is not always a write (a preview is a read), so the method alone does not decide. TestEveryOperatorRouteIsClassed fails for
// a route of the table that is not here; at run time an unlisted route counts as a write, so a support operator is refused it.
type opClass int

const (
	opWrite opClass = iota + 1
	opRead
)

var operatorClasses = map[string]opClass{
	"POST /admin/v1/accounts":                                                                              opWrite,
	"POST /admin/v1/accounts/{id}/apps":                                                                    opWrite,
	"DELETE /admin/v1/api-keys/{id}":                                                                       opWrite,
	"POST /admin/v1/appeals/{id}/decision":                                                                 opWrite,
	"POST /admin/v1/billing/close-period":                                                                  opWrite,
	"POST /admin/v1/blocklist":                                                                             opWrite,
	"DELETE /admin/v1/blocklist/{id}":                                                                      opWrite,
	"POST /admin/v1/credit-notes/{id}/resolve-refund":                                                      opWrite,
	"POST /admin/v1/credit-notes/{id}/retry-refund":                                                        opWrite,
	"POST /admin/v1/dead-letters/redrive":                                                                  opWrite,
	"POST /admin/v1/invoices/{id}/credit-notes":                                                            opWrite,
	"POST /admin/v1/invoices/{id}/payments":                                                                opWrite,
	"POST /admin/v1/invoices/{id}/retry":                                                                   opWrite,
	"POST /admin/v1/invoices/{id}/void":                                                                    opWrite,
	"DELETE /admin/v1/organizations/{account_id}/overrides":                                                opWrite,
	"PUT /admin/v1/organizations/{account_id}/plan":                                                        opWrite,
	"PUT /admin/v1/price-change-notice":                                                                    opWrite,
	"PUT /admin/v1/price-change-pause":                                                                     opWrite,
	"PUT /admin/v1/support/apps/{app_id}/holidays":                                                         opWrite,
	"POST /admin/v1/support/canned-replies":                                                                opWrite,
	"DELETE /admin/v1/support/canned-replies/{id}":                                                         opWrite,
	"PUT /admin/v1/support/canned-replies/{id}":                                                            opWrite,
	"PATCH /admin/v1/support/tickets/{id}":                                                                 opWrite,
	"POST /admin/v1/support/tickets/{id}/merge":                                                            opWrite,
	"POST /admin/v1/support/tickets/{id}/messages/{message_id}/attachments":                                opWrite,
	"POST /admin/v1/support/tickets/{id}/replies":                                                          opWrite,
	"PATCH /admin/v1/tenants/{id}":                                                                         opWrite,
	"POST /admin/v1/tenants/{id}/api-keys":                                                                 opWrite,
	"DELETE /admin/v1/tenants/{id}/scheduled-status":                                                       opWrite,
	"PUT /admin/v1/tenants/{id}/scheduled-status":                                                          opWrite,
	"PUT /admin/v1/tenants/{id}/status":                                                                    opWrite,
	"POST /admin/v1/tenants/{tenant_id}/billing-runs/{period}/rerating":                                    opWrite,
	"POST /admin/v1/tenants/{tenant_id}/checkouts":                                                         opWrite,
	"DELETE /admin/v1/tenants/{tenant_id}/e-invoice-accounts/{environment}":                                opWrite,
	"PUT /admin/v1/tenants/{tenant_id}/e-invoice-accounts/{environment}":                                   opWrite,
	"POST /admin/v1/tenants/{tenant_id}/e-invoices/{id}/cancel":                                            opWrite,
	"POST /admin/v1/tenants/{tenant_id}/e-invoices/{id}/retry":                                             opWrite,
	"POST /admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/transfer/receipts":                           opWrite,
	"POST /admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/transfer/reject":                             opWrite,
	"DELETE /admin/v1/tenants/{tenant_id}/provider-accounts/{provider}":                                    opWrite,
	"PUT /admin/v1/tenants/{tenant_id}/provider-accounts/{provider}":                                       opWrite,
	"DELETE /admin/v1/tenants/{tenant_id}/provider-accounts/{provider}/failover":                           opWrite,
	"PUT /admin/v1/tenants/{tenant_id}/provider-accounts/{provider}/failover":                              opWrite,
	"POST /admin/v1/tenants/{tenant_id}/provider-migrations":                                               opWrite,
	"POST /admin/v1/tenants/{tenant_id}/provider-migrations/{migration_id}/abort":                          opWrite,
	"POST /admin/v1/tenants/{tenant_id}/providers/{provider}/catalogue/sync":                               opWrite,
	"POST /admin/v1/tenants/{tenant_id}/providers/{provider}/customers/{external_id}":                      opWrite,
	"POST /admin/v1/tenants/{tenant_id}/providers/{provider}/customers/{external_id}/payment-method-setup": opWrite,
	"POST /admin/v1/tenants/{tenant_id}/providers/{provider}/customers/{external_id}/portal":               opWrite,
	"POST /admin/v1/tenants/{tenant_id}/providers/{provider}/discounts":                                    opWrite,
	"POST /admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/change":                    opWrite,
	"POST /admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/change-preview":            opRead,
	"POST /admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/pause":                     opWrite,
	"POST /admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/payment-method-link":       opWrite,
	"POST /admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/resume":                    opWrite,
	"POST /admin/v1/tenants/{tenant_id}/reconciliation/discrepancies/resolve":                              opWrite,
	"POST /admin/v1/tenants/{tenant_id}/reconciliation/discrepancies/{id}/assign":                          opWrite,
	"POST /admin/v1/tenants/{tenant_id}/reconciliation/discrepancies/{id}/explain":                         opWrite,
	"POST /admin/v1/tenants/{tenant_id}/reconciliation/discrepancies/{id}/resolve":                         opWrite,
	"POST /admin/v1/tenants/{tenant_id}/reconciliation/runs":                                               opWrite,
	"POST /admin/v1/tenants/{tenant_id}/replays":                                                           opWrite,
	"POST /admin/v1/tenants/{tenant_id}/replays/{job_id}/discard":                                          opWrite,
	"POST /admin/v1/tenants/{tenant_id}/replays/{job_id}/promote":                                          opWrite,
	"DELETE /admin/v1/tenants/{tenant_id}/settlement/fee-rules":                                            opWrite,
	"PUT /admin/v1/tenants/{tenant_id}/settlement/fee-rules":                                               opWrite,
	"POST /admin/v1/tenants/{tenant_id}/settlement/import":                                                 opWrite,
	"POST /admin/v1/tenants/{tenant_id}/settlement/sync":                                                   opWrite,
	"POST /admin/v1/users/two-factor/reset":                                                                opWrite,
}

// operatorWrites says whether an operator route changes something (and so needs the role operator_admin).
func operatorWrites(method, path string) bool {
	if method == http.MethodGet || method == http.MethodHead {
		return false
	}
	return operatorClasses[method+" "+path] != opRead
}
