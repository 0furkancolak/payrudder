# RabbitMQ for Railway (no Railway-native broker). Build context: the repo root. Mount a volume at /var/lib/rabbitmq and set RAILWAY_RUN_UID=0.
# Health: the Prometheus endpoint, so the service variable PORT must be 15692.
FROM rabbitmq:4.2.9-management-alpine
# The node name defaults to the container's hostname, which Railway changes on every deploy; a changed name would orphan the data directory.
ENV RABBITMQ_NODENAME=rabbit@localhost
RUN rabbitmq-plugins enable --offline rabbitmq_prometheus
COPY deploy/rabbitmq/rabbitmq.conf /etc/rabbitmq/conf.d/20-payrudder.conf
COPY deploy/railway/images/rabbitmq/railway.conf /etc/rabbitmq/conf.d/30-railway.conf
COPY deploy/rabbitmq/definitions.json /etc/rabbitmq/definitions.template.json
COPY deploy/railway/images/rabbitmq/entrypoint.sh /railway-entrypoint.sh
ENTRYPOINT ["/bin/sh", "/railway-entrypoint.sh"]
CMD ["rabbitmq-server"]
