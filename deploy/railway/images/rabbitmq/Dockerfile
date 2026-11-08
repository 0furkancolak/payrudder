# RabbitMQ for Railway (no Railway-native broker). Build context: the repo root. Mount a volume at /var/lib/rabbitmq and set RAILWAY_RUN_UID=0.
# Health: the Prometheus endpoint, so the service variable PORT must be 15692.
# No management plugin (its UI and stats database cost ~50 MB and nothing here uses them); the Prometheus plugin needs only the agent.
FROM rabbitmq:4.2.9-alpine
# Erlang for a small, mostly idle broker: one scheduler, few async threads, allocators that hand memory back (RSS follows the load).
ENV RABBITMQ_SERVER_ADDITIONAL_ERL_ARGS="+S 1:1 +SDio 1 +A 4 +MBas ageffcbf +MHas ageffcbf +MBlmbcs 512 +MHlmbcs 512 +MMmcs 30"
# The node name defaults to the container's hostname, which Railway changes on every deploy; a changed name would orphan the data directory.
ENV RABBITMQ_NODENAME=rabbit@localhost
RUN rabbitmq-plugins enable --offline rabbitmq_prometheus
COPY deploy/rabbitmq/rabbitmq.conf /etc/rabbitmq/conf.d/20-payrudder.conf
COPY deploy/railway/images/rabbitmq/railway.conf /etc/rabbitmq/conf.d/30-railway.conf
COPY deploy/rabbitmq/definitions.json /etc/rabbitmq/definitions.template.json
COPY deploy/railway/images/rabbitmq/entrypoint.sh /railway-entrypoint.sh
ENTRYPOINT ["/bin/sh", "/railway-entrypoint.sh"]
CMD ["rabbitmq-server"]
