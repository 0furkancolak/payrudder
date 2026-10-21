# syntax=docker/dockerfile:1.7
# The development stand-in for Google (pkg/oidctest). Build context: repo root. Standard library only.
FROM golang:1.27.2-alpine AS build
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0 GOFLAGS=-trimpath
COPY pkg/go.mod pkg/go.sum pkg/
RUN --mount=type=cache,target=/go/pkg/mod cd pkg && go mod download
COPY pkg pkg
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd pkg && go build -ldflags "-s -w" -o /out/mockoidc ./oidctest/cmd/mockoidc

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mockoidc /mockoidc
USER nonroot:nonroot
EXPOSE 8099
ENTRYPOINT ["/mockoidc"]
