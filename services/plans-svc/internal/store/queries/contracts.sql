-- Contracts for the seller's customers (internal/contracts). Tenant-scoped, run as plans_app inside pgxx.InTenantTx; the explicit tenant_id = app_tenant_id()
-- predicates are belt and braces next to row-level security.

-- name: NextContractVersionNumber :one
SELECT (COALESCE(max(number), 0) + 1)::int FROM contract_versions WHERE tenant_id = app_tenant_id() AND kind = @kind AND audience = @audience;

-- name: InsertContractVersion :one
INSERT INTO contract_versions (tenant_id, id, kind, audience, number, material, bodies, sha256, note, author)
VALUES (app_tenant_id(), @id, @kind, @audience, @number, @material, @bodies, @sha256, @note, @author)
RETURNING *;

-- name: GetContractVersion :one
SELECT * FROM contract_versions WHERE tenant_id = app_tenant_id() AND id = $1;

-- name: ListContractVersions :many
SELECT * FROM contract_versions
WHERE tenant_id = app_tenant_id() AND (@kind::text = '' OR kind = @kind) AND (@audience::text = '' OR audience = @audience)
ORDER BY kind, audience, number DESC;

-- The newest version of each series and the number of the newest material one: a customer who accepted at least that number is still covered.
-- name: ListContractCurrent :many
SELECT v.tenant_id, v.id, v.kind, v.audience, v.number, v.material, v.bodies, v.sha256, v.note, v.author, v.created_at,
  (SELECT max(m.number) FROM contract_versions m WHERE m.tenant_id = v.tenant_id AND m.kind = v.kind AND m.audience = v.audience AND m.material)::int AS material_number
FROM (SELECT DISTINCT ON (kind, audience) * FROM contract_versions WHERE tenant_id = app_tenant_id() ORDER BY kind, audience, number DESC) v;

-- name: GetContractSettings :one
SELECT * FROM contract_settings WHERE tenant_id = app_tenant_id();

-- name: UpsertContractSettings :one
INSERT INTO contract_settings (tenant_id, required_kinds, immediate_start, retention_months, withdrawal_refund, renewal_notice_days, renewal_cancel_texts, withdrawal_function, updated_at)
VALUES (app_tenant_id(), @required_kinds::text[], @immediate_start, @retention_months, @withdrawal_refund, @renewal_notice_days, @renewal_cancel_texts, @withdrawal_function, now())
ON CONFLICT (tenant_id) DO UPDATE SET required_kinds = EXCLUDED.required_kinds, immediate_start = EXCLUDED.immediate_start,
  retention_months = EXCLUDED.retention_months, withdrawal_refund = EXCLUDED.withdrawal_refund, renewal_notice_days = EXCLUDED.renewal_notice_days,
  renewal_cancel_texts = EXCLUDED.renewal_cancel_texts, withdrawal_function = EXCLUDED.withdrawal_function, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: InsertContractAcceptance :one
INSERT INTO contract_acceptances (tenant_id, id, group_id, customer_id, version_id, kind, audience, version_number, version_sha256, locale_requested, locale_served, body_sha256,
  context_kind, context_ref, source, observed, checkbox_text, immediate_start_consent, immediate_start_text, withdrawal_waiver_acknowledged, withdrawal_waiver_text,
  variables, snapshot_sha256, ip, user_agent, accepted_at)
VALUES (app_tenant_id(), @id, @group_id, @customer_id, @version_id, @kind, @audience, @version_number, @version_sha256, @locale_requested, @locale_served, @body_sha256,
  @context_kind, @context_ref, @source, @observed, @checkbox_text, @immediate_start_consent, @immediate_start_text, @withdrawal_waiver_acknowledged, @withdrawal_waiver_text,
  @variables, @snapshot_sha256, @ip, @user_agent, @accepted_at)
RETURNING *;

-- The highest version number of each series a customer accepted (any language), and the highest of those accepted with both the immediate-start request and the
-- waiver acknowledgement: what the gate compares with the series' material number.
-- name: CustomerAcceptedNumbers :many
SELECT kind, audience, max(version_number)::int AS number, COALESCE(max(version_number) FILTER (WHERE immediate_start_consent AND withdrawal_waiver_acknowledged), 0)::int AS consent_number,
  max(accepted_at)::timestamptz AS accepted_at
FROM contract_acceptances WHERE tenant_id = app_tenant_id() AND customer_id = @customer_id
GROUP BY kind, audience;

-- The same for a guest: the acceptances made for one invoice.
-- name: ReferenceAcceptedNumbers :many
SELECT kind, audience, max(version_number)::int AS number, COALESCE(max(version_number) FILTER (WHERE immediate_start_consent AND withdrawal_waiver_acknowledged), 0)::int AS consent_number,
  max(accepted_at)::timestamptz AS accepted_at
FROM contract_acceptances WHERE tenant_id = app_tenant_id() AND customer_id IS NULL AND context_ref = @context_ref
GROUP BY kind, audience;

-- name: ListCustomerContractAcceptances :many
SELECT a.*, c.external_id AS customer_external_id FROM contract_acceptances a LEFT JOIN customers c ON c.tenant_id = a.tenant_id AND c.id = a.customer_id
WHERE a.tenant_id = app_tenant_id() AND a.customer_id = @customer_id
ORDER BY a.accepted_at DESC, a.kind;

-- name: GetContractAcceptance :one
SELECT * FROM contract_acceptances WHERE tenant_id = app_tenant_id() AND id = $1;

-- name: ListContractAcceptanceGroup :many
SELECT a.*, c.external_id AS customer_external_id FROM contract_acceptances a LEFT JOIN customers c ON c.tenant_id = a.tenant_id AND c.id = a.customer_id
WHERE a.tenant_id = app_tenant_id() AND a.group_id = $1
ORDER BY a.kind, a.id;

-- name: InsertContractSnapshot :exec
INSERT INTO contract_snapshots (tenant_id, group_id, html, sha256) VALUES (app_tenant_id(), @group_id, @html, @sha256);

-- name: GetContractSnapshotOfGroup :one
SELECT * FROM contract_snapshots WHERE tenant_id = app_tenant_id() AND group_id = $1;

-- name: InsertContractWithdrawal :one
INSERT INTO contract_withdrawals (tenant_id, id, customer_id, subscription_id, invoice_id, statement, source, received_at)
VALUES (app_tenant_id(), @id, @customer_id, @subscription_id, @invoice_id, @statement, @source, @received_at)
RETURNING *;

-- name: GetContractWithdrawalForUpdate :one
SELECT * FROM contract_withdrawals WHERE tenant_id = app_tenant_id() AND id = $1 FOR UPDATE;

-- name: DecideContractWithdrawal :one
UPDATE contract_withdrawals SET status = @status, decided_at = @decided_at, decided_by = @decided_by, decision_note = @decision_note
WHERE tenant_id = app_tenant_id() AND id = @id RETURNING *;

-- name: ListContractWithdrawals :many
SELECT w.*, c.external_id AS customer_external_id FROM contract_withdrawals w JOIN customers c ON c.tenant_id = w.tenant_id AND c.id = w.customer_id
WHERE w.tenant_id = app_tenant_id() AND (@customer_id::uuid = '00000000-0000-0000-0000-000000000000'::uuid OR w.customer_id = @customer_id) AND (@status::text = '' OR w.status = @status)
ORDER BY w.received_at DESC, w.id DESC LIMIT 200;

-- The refund of an accepted withdrawal (the caller holds the row lock of GetContractWithdrawalForUpdate and has checked the state).
-- name: SetContractWithdrawalRefund :one
UPDATE contract_withdrawals SET refund_state = @state, credit_note_id = @credit_note_id, refund_note = @note, refund_at = @now
WHERE tenant_id = app_tenant_id() AND id = @id RETURNING *;

-- name: GetContractWithdrawalCustomer :one
SELECT w.*, c.external_id AS customer_external_id FROM contract_withdrawals w JOIN customers c ON c.tenant_id = w.tenant_id AND c.id = w.customer_id
WHERE w.tenant_id = app_tenant_id() AND w.id = $1;

-- The seller's own facts for the variables of the texts.
-- name: ContractSeller :one
SELECT name, address, legal FROM current_tenant();
