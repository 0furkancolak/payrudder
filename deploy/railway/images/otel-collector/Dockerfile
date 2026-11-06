# OpenTelemetry Collector for Railway: deploy/otel/collector.yaml listening on IPv6 too (the private network), exporting to the environment's
# Tempo. Build context: the repo root. Health: :13133 (the service variable PORT).
FROM busybox:1.37 AS cfg
COPY deploy/otel/collector.yaml /collector.yaml
RUN sed -i -e 's#0.0.0.0:\([0-9]*\)#"[::]:\1"#g' -e 's#endpoint: tempo:4317#endpoint: tempo.railway.internal:4317#' -e 's#host: 0.0.0.0#host: "::"#' /collector.yaml \
 && grep -q 'tempo.railway.internal' /collector.yaml && ! grep -q '0.0.0.0' /collector.yaml

FROM otel/opentelemetry-collector-contrib:0.163.1
COPY --from=cfg /collector.yaml /etc/otelcol/collector.yaml
CMD ["--config=/etc/otelcol/collector.yaml"]
