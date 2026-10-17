# syntax=docker/dockerfile:1.7
# Service image template (copy for new services). Build context: repo root.
# Built with GOWORK=off: the service go.mod points at ../../pkg and ../../gen/go via replace directives,
# so only the modules the service needs are copied.
FROM golang:1.27.2-alpine AS build
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0 GOFLAGS=-trimpath

COPY pkg/go.mod pkg/go.sum pkg/
COPY gen/go/go.mod gen/go/go.sum gen/go/
COPY services/plans-svc/go.mod services/plans-svc/go.sum services/plans-svc/
RUN --mount=type=cache,target=/go/pkg/mod cd services/plans-svc && go mod download

COPY pkg pkg
COPY gen/go gen/go
COPY services/plans-svc services/plans-svc
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd services/plans-svc && go build -ldflags "-s -w" -o /out/service ./cmd/plans-svc

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/service /service
USER nonroot:nonroot
ENTRYPOINT ["/service"]
