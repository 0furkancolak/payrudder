# syntax=docker/dockerfile:1.7
# The image of every Go program in the repo (services/*, tools/mockpsp, pkg/oidctest/cmd/mockoidc). Build context: the repo root.
#   docker build -f deploy/docker/go.Dockerfile --build-arg SERVICE_DIR=services/plans-svc .
# SERVICE_DIR   the module directory; the binary is built from <dir>/cmd/<basename of dir> unless MAIN_PKG names another package (relative to the module).
# GO_TAGS       build tags, e.g. "chaos" for the chaos overlay of the compose file.
# Built with GOWORK=off: the module's go.mod points at ../../pkg and ../../gen/go via replace directives, so only the modules it needs are copied.
# deploy/railway/go.Dockerfile is this file without the BuildKit cache mounts, which Railway does not accept without an id of its own.
FROM golang:1.27.2-alpine AS build
ARG SERVICE_DIR
ARG MAIN_PKG=
ARG GO_TAGS=
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0 GOFLAGS=-trimpath

RUN test -n "$SERVICE_DIR" || { echo "SERVICE_DIR is not set: pass --build-arg SERVICE_DIR=services/<name>" >&2; exit 1; }
COPY pkg/go.mod pkg/go.sum pkg/
COPY gen/go/go.mod gen/go/go.sum gen/go/
COPY ${SERVICE_DIR}/go.mod ${SERVICE_DIR}/go.sum ${SERVICE_DIR}/
RUN --mount=type=cache,target=/go/pkg/mod cd ${SERVICE_DIR} && go mod download

COPY pkg pkg
COPY gen/go gen/go
COPY ${SERVICE_DIR} ${SERVICE_DIR}
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd ${SERVICE_DIR} && go build -tags "$GO_TAGS" -ldflags "-s -w" -o /out/service "${MAIN_PKG:-./cmd/$(basename ${SERVICE_DIR})}"

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/service /service
USER nonroot:nonroot
ENTRYPOINT ["/service"]
