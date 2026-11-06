# Loki for Railway: deploy/loki/loki.yaml with 7 days of logs. Build context: the repo root. Mount a volume at /loki and set RAILWAY_RUN_UID=0.
FROM busybox:1.37 AS cfg
COPY deploy/loki/loki.yaml /loki.yaml
RUN sed -i -e 's#retention_period: 72h#retention_period: 168h#' -e 's#reject_old_samples_max_age: 72h#reject_old_samples_max_age: 168h#' /loki.yaml \
 && grep -q 'retention_period: 168h' /loki.yaml

FROM grafana/loki:3.5.5
COPY --from=cfg /loki.yaml /etc/loki/loki.yaml
CMD ["-config.file=/etc/loki/loki.yaml"]
