# ADR 0022: The product is Payrudder everywhere

Status: accepted (phase 48). Supersedes the first version of this decision (a surface-only rename), which shipped as stage 1.

## Context

The product is named Payrudder. The brand files and rules live in `payrudder-brand/` (`DESIGN.md` is the source of truth for tokens and logos). The code base was called `quotify` in every identifier. Stage 1 renamed what people read (console, hosted pages, e-mails, README, docs headings), added the `PAYRUDDER_*` spelling of the settings and the brand tokens. The owner then decided that two names in one code base cost more than one large rename.

## Decision

1. **One name.** Everything says `payrudder` / `Payrudder` / `PAYRUDDER`: the Go module path `github.com/0furkancolak/payrudder`, the proto packages (`payrudder.plans.v1`, ...) and the generated code, `payrudderctl`, directories and files, Helm charts (`payrudder`, `payrudder-infra`), compose and Kubernetes names, dev database, role and bucket names, metric names, the `Payrudder-Signature` webhook header and the `X-Payrudder-*` request headers, the console's package name and browser storage keys, docs and CI.
2. **Environment prefix.** `PAYRUDDER_*` is the name. The old `QUOTIFY_*` spelling keeps working as a deprecated fallback: `pkg/config` copies a `QUOTIFY_X` setting to `PAYRUDDER_X` when the new name is not set (the new name wins) and logs one warning that names the deprecated variables; `payrudderctl` does the same with a warning on stderr. The fallback is removed in a later release. It covers what the services and `payrudderctl` read; compose files, Helm values, scripts and test helpers use only the new names.
3. **One tool, run again when main moves.** `scripts/rename-to-payrudder.py` does the mechanical part (paths and contents, case kept), then `make gen` regenerates protobuf and sqlc code. The generated protobuf files are never edited by hand, because their descriptors carry the package name with its length.
4. **Kept on purpose**, because they are stored data or values that other systems already hold:
   - applied migrations are not edited (a rename of a table or column is a data change, not a rename);
   - the column and JSON field `quotify_subscription_id` (sqlc maps it to `PayrudderSubscriptionID` in Go) and the provider custom-data keys `quotify_subscription_id`, `quotify_plan_code`, `quotify_customer_id`, `quotify_migration_id`, which Paddle returns for objects created before the rename;
   - ~~the subscription `owner` value `quotify`~~ (renamed afterwards, while nothing was released: `owner` is `payrudder` or `provider` in the database check and default (plans-svc), the API enum, the proto comments and the console);
   - API key and session token prefixes `qk_` / `qs_`: they are not a name.
5. **Console.** `BRAND_NAME` still overrides the displayed name; the default is `Payrudder`. A grep test (`tests/brand_test.go`) fails on the old name in the console's locale files, e-mail texts and hosted-page templates.

## Consequences

- **Breaking for integrations.** A receiver of webhooks must verify `Payrudder-Signature` instead of `Quotify-Signature`; clients that send `X-Quotify-Tenant` or `X-Quotify-Actor` send the `X-Payrudder-*` header. Metric names change (`quotify_*` to `payrudder_*`): dashboards and alert rules in `deploy/` are renamed with them; anything outside the repository that queries the old names must follow.
- **Dev stacks start fresh.** Compose volumes, database names, roles and the bucket carry the new name; run `make down` with volumes removed once. Browser storage keys change, so a signed-in console tab signs out once.
- **The GitHub repository** is renamed by the owner, not by this change. Until then the module path does not match the remote URL; `GOPRIVATE` and the workspace use local paths, so builds do not depend on it.
- The old stage-1 `PAYRUDDER_` alias helper (`AliasPrefix`) is gone; `config.UpgradeLegacy` and `config.Getenv` replace it.
