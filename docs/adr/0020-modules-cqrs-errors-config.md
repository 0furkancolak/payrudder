# ADR 0020: Modules with separate commands and queries, error keys, YAML defaults

Status: accepted; applied to new code now and to old code as it is touched.

## Context

Services grew by feature (`grpcapi/changes.go`, `grpcapi/coupons.go`…) with handlers that validate, decide, write and shape the response in one function. Errors are a gRPC code and a sentence. Defaults are scattered over `envDefault` tags.

## Decision

1. **Modules.** A service is a set of modules, one directory per capability (`webhooks`, `coupons`, `creditnotes`, `dunning`). A module has `command/` (changes state, returns an id or an error), `query/` (reads, never writes), its own `store` queries and one file that registers its gRPC handlers. Modules call each other only through their exported command and query functions. No shared "service" or "utils" package.
2. **Handlers are thin.** A handler authenticates (already done by the interceptor), maps the request to a command or query, and maps the result. Rules live in the command.
3. **Error keys.** Every error a client can see has a stable, dotted, lower-case `key` (`billing.credit_note.not_paid`, `plans.subscription.coupon_conflict`) besides the gRPC code and an English message. The key travels as `errdetails.ErrorInfo.reason` through gRPC and as `error.key` in the HTTP body; the console and SDKs translate keys, never messages. Keys are listed in `docs/error-keys.md`, generated from the code, and a test fails if a returned key is not in the list.
4. **Configuration.** Each service embeds `config.default.yaml` with every setting that is public and has a sensible default; environment variables override it; secrets (`*_DSN`, tokens, keys) are never in YAML and are required from the environment. The YAML files are what the docs list as "settings".

## Consequences

- New code is written in this shape from phase 24; the previous phases' files are moved into modules when a change touches them, to keep each pull request reviewable.
- A rename of an error key is a breaking change and follows the API's deprecation rule.
