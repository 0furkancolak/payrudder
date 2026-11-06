# Vector for Railway: receives the environment's logs from Locomotive (JSON lines over HTTP, :8080) and ships them to Loki with the labels the
# dashboards use. Build context: the repo root. Health: the API on :8686 (the service variable PORT).
FROM timberio/vector:0.50.0-alpine
COPY deploy/railway/images/vector/vector.yaml /etc/vector/vector.yaml
CMD ["--config", "/etc/vector/vector.yaml", "--quiet"]
