# Contributing

## Workflow

- Branch from `main`: `feat/<phase>-<topic>`, `fix/<topic>`, `docs/<topic>`, `chore/<topic>`.
- One phase (or part of one) per pull request. The phase file's checklist is ticked in the PR that completes it.
- CI must be green: lint, `buf lint` + `buf breaking`, generated code up to date, unit and integration tests, compose stack healthy.
- Design changes discovered while building are written back into `docs/` in the same PR. Code and docs must not disagree.

## Commits

[Conventional Commits](https://www.conventionalcommits.org/): `type(scope): summary`

| Type | Use |
|---|---|
| `feat` | new behaviour |
| `fix` | bug fix |
| `docs` | documentation only |
| `test` | tests only |
| `refactor` | no behaviour change |
| `build` / `ci` / `chore` | tooling, pipelines, housekeeping |

Scope is the module or area: `feat(ingest-svc): …`, `ci: …`, `docs(adr): …`. Breaking API or event-schema changes add `!`
and a `BREAKING CHANGE:` footer.

## Local setup

```bash
make tools        # pinned tools into ./bin (first run takes a few minutes)
make up-infra     # infrastructure only
make setup        # once: tools, the infrastructure containers, the console's packages
make dev          # every day: the services on your machine with reload, the console on :3001 (ONLY=gateway,plans-svc to run some)
make logs         # follow all service logs (make logs S=gateway for one)
make test         # unit tests
make test-integration
make lint
```

If ports 5432 or 8080 are taken, set `PAYRUDDER_PG_HOST_PORT` / `PAYRUDDER_CONSOLE_HOST_PORT` (see `.env.example`).

## Adding a service

1. `services/<name>/go.mod` (module `github.com/0furkancolak/payrudder/services/<name>`) with `replace` directives to `../../pkg`
   and `../../gen/go`, then `go work use ./services/<name>` and add it to `MODULES` in the Makefile.
2. No Dockerfile: every Go image is built by `deploy/docker/go.Dockerfile`; the compose service names its module with `build.args.SERVICE_DIR`.
3. Add the service to the compose `app` profile and to `deploy/prometheus/prometheus.yml`.
