# Tempo for Railway: deploy/tempo/tempo.yaml listening on IPv6 too (the private network) and writing span metrics to the environment's
# Prometheus, 7 days of traces. Build context: the repo root. Mount a volume at /var/tempo and set RAILWAY_RUN_UID=0.
FROM busybox:1.37 AS cfg
COPY deploy/tempo/tempo.yaml /tempo.yaml
RUN sed -i -e 's#0.0.0.0:4317#"[::]:4317"#' -e 's#http://prometheus:9090#http://prometheus.railway.internal:9090#' -e 's#block_retention: 48h#block_retention: 168h#' /tempo.yaml \
 && grep -q 'prometheus.railway.internal' /tempo.yaml && grep -q '168h' /tempo.yaml

FROM grafana/tempo:2.10.8
COPY --from=cfg /tempo.yaml /etc/tempo/tempo.yaml
CMD ["-config.file=/etc/tempo/tempo.yaml"]
