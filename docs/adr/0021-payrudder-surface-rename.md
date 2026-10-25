# ADR 0021: Payrudder is a surface rename; `PAYRUDDER_*` aliases `QUOTIFY_*`

Status: accepted (phase 48). Stage 1 (this decision: surface rename and the `PAYRUDDER_*` alias) ships first; the owner then widened the scope to a full rename, recorded in the section "Stage 2" when that change lands.

## Context

The product is now named Payrudder. The brand files and rules live in `payrudder-brand/` (`DESIGN.md` is the source of truth for tokens and logos). The code base is called `quotify` everywhere: Go module path `github.com/0furkancolak/quotify`, proto packages, metric names, table names, `QUOTIFY_*` variables, `qk_` / `qs_` token prefixes, `X-Quotify-*` and `Quotify-Signature` headers, Helm chart and compose names.

## Decision

1. **Only what people read changes.** The console, hosted pages, e-mails, invoice PDFs, the README and docs headings say Payrudder. The default of `BRAND_NAME` (`pkg/brand`, `web/admin/src/shared/lib/brand.ts`) is now `Payrudder`.
2. **Everything a machine reads keeps its name.** Module path, proto packages, metrics, tables, token prefixes `qk_` / `qs_`, container and chart names, the `Quotify-*` webhook headers and `X-Quotify-*` request headers. `brand.Slug()` stays `quotify` while the name is the default, so the wire format of existing integrations does not change; an operator who sets another `BRAND_NAME` still gets the derived slug as before. Renaming any of these is a large breaking change with no value for users.
3. **`PAYRUDDER_*` alias.** `pkg/config` accepts `PAYRUDDER_X` wherever `QUOTIFY_X` is read, and `PAYRUDDER_X` wins when both are set (`config.AliasPrefix`, applied in `Load`; `config.Getenv` for code that reads the environment directly; `quotifyctl` has the same rule). YAML defaults keep their `QUOTIFY_` names. The console's nginx template (`QUOTIFY_DEMO`, envsubst) is not aliased.
4. **Legal and compatibility notes keep "Quotify"**: places that must name the old wire format (for example "verify the `Quotify-Signature` header"). They are listed in the allow-list of the test `tests/brand_test.go` (locale files and e-mail templates), which fails on any other "Quotify" in a user-visible string.

## Consequences

- A deployment that sets `BRAND_NAME=Quotify` keeps the old name; the console replaces the default name in its texts at run time.
- Two names exist in the code base on purpose. New user-visible text says Payrudder; new identifiers say `quotify` until a separate, single rename PR changes them all.
