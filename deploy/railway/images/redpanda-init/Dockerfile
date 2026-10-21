# One-shot job: the Kafka topics. Build context: the repo root (the topic list is deploy/compose/redpanda/topics.sh, shared with `make topics`).
FROM redpandadata/redpanda:v26.2.5
COPY deploy/compose/redpanda/topics.sh /topics.sh
COPY deploy/railway/images/redpanda-init/init.sh /init.sh
ENTRYPOINT ["/bin/sh", "/init.sh"]
