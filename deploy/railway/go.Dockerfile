# syntax=docker/dockerfile:1.7
# One Dockerfile for every Go service on Railway. Build context: the repo root. The service names itself with SERVICE_DIR, a service variable that
# Railway passes to the build as an ARG (services/gateway, services/plans-svc, ..., tools/mockpsp); the binary is <dir>/cmd/<basename of dir>.
# It mirrors deploy/docker/go.Dockerfile (minus GO_TAGS and MAIN_PKG, which Railway does not need) without the BuildKit cache mounts: Railway accepts a cache mount only with an id of its own form
# (id=s/<service id>-<path>), which one shared Dockerfile cannot spell, so these builds are a little slower instead of broken.
FROM golang:1.27.2-alpine AS build
ARG SERVICE_DIR
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0 GOFLAGS=-trimpath

RUN test -n "$SERVICE_DIR" || { echo "SERVICE_DIR is not set: add it to the service variables (deploy/railway/README.md)" >&2; exit 1; }
COPY pkg/go.mod pkg/go.sum pkg/
COPY gen/go/go.mod gen/go/go.sum gen/go/
COPY ${SERVICE_DIR}/go.mod ${SERVICE_DIR}/go.sum ${SERVICE_DIR}/
RUN cd ${SERVICE_DIR} && go mod download

COPY pkg pkg
COPY gen/go gen/go
COPY ${SERVICE_DIR} ${SERVICE_DIR}
RUN cd ${SERVICE_DIR} && go build -ldflags "-s -w" -o /out/service ./cmd/$(basename ${SERVICE_DIR})

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/service /service
USER nonroot:nonroot
ENTRYPOINT ["/service"]
