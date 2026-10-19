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
make dev          # whole stack in Docker + the console on :3001 with hot reload
make logs         # follow all service logs (make logs S=gateway for one)
make test         # unit tests
make test-integration
make lint
```

If ports 5432 or 8080 are taken, set `QUOTIFY_PG_HOST_PORT` / `QUOTIFY_CONSOLE_HOST_PORT` (see `.env.example`).

## Adding a service

1. `services/<name>/go.mod` (module `github.com/0furkancolak/quotify/services/<name>`) with `replace` directives to `../../pkg`
   and `../../gen/go`, then `go work use ./services/<name>` and add it to `MODULES` in the Makefile.
2. Copy `services/plans-svc/Dockerfile` and change the paths.
3. Add the service to the compose `app` profile and to `deploy/prometheus/prometheus.yml`.
