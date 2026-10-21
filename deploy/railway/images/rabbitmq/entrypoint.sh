#!/bin/sh
# Renders the broker definitions (deploy/rabbitmq/definitions.json: the user, exchanges and quorum queues generated from pkg/rabbitx) with the
# password of the `quotify` user taken from RABBITMQ_PASSWORD, then starts RabbitMQ through the image's own entrypoint.
set -eu

case "${RABBITMQ_PASSWORD:-}" in
  *[!A-Za-z0-9]* | "") echo "RABBITMQ_PASSWORD must be set and alphanumeric (it goes into an amqp:// URL): use openssl rand -hex 24" >&2; exit 1 ;;
esac
sed "s/\"password\": \"quotify_local\"/\"password\": \"${RABBITMQ_PASSWORD}\"/" /etc/rabbitmq/definitions.template.json >/tmp/definitions.json
if grep -q quotify_local /tmp/definitions.json; then echo "definitions still carry the local password" >&2; exit 1; fi
exec docker-entrypoint.sh "$@"
