# Locomotive (Railway's log forwarder template, MIT, github.com/brody192/locomotive) built from a pinned commit with one patch: it parses its
# Railway token as a UUID, and Railway's account tokens are now rlwy_acct_... (token-string.patch reads it as a string; the token is only ever
# sent as a Bearer header). Drop the patch, and go back to the template's image, once upstream takes the new form. Build context: the repo root.
FROM golang:1.26.1-alpine AS build
RUN apk add --no-cache git
WORKDIR /src
RUN git clone https://github.com/brody192/locomotive . && git checkout c059a98654ad68fafbaa5892ad23ca4e64d06229
COPY deploy/railway/images/locomotive/token-string.patch /tmp/token-string.patch
RUN git apply /tmp/token-string.patch && CGO_ENABLED=0 go build -ldflags "-w -s" -o /locomotive .

FROM gcr.io/distroless/static
COPY --from=build /locomotive /app/main
ENTRYPOINT ["/app/main"]
