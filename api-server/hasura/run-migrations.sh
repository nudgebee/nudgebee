#!/bin/bash

set -e

echo "Running Deploy..."
hasura deploy --endpoint "$HASURA_GRAPHQL_ENDPOINT" --admin-secret "$HASURA_GRAPHQL_ADMIN_SECRET" --skip-update-check

# echo "Check current migration status..."
# hasura migrate status --database-name app --endpoint "$HASURA_GRAPHQL_ENDPOINT" --admin-secret "$HASURA_GRAPHQL_ADMIN_SECRET" --skip-update-check || true

echo "Running migrations..."
hasura migrate apply --database-name app --endpoint "$HASURA_GRAPHQL_ENDPOINT" --admin-secret "$HASURA_GRAPHQL_ADMIN_SECRET" --skip-update-check

echo "Applying metadata..."
hasura metadata apply --endpoint "$HASURA_GRAPHQL_ENDPOINT" --admin-secret "$HASURA_GRAPHQL_ADMIN_SECRET" --skip-update-check

echo "Reloading metadata..."
hasura metadata reload --endpoint "$HASURA_GRAPHQL_ENDPOINT" --admin-secret "$HASURA_GRAPHQL_ADMIN_SECRET" --skip-update-check

echo "Loading Agent Playbook..."
curl -X POST $SERVICE_API_SERVER_URL/hasura-cron -d '{
        "comment": "Load Agent Playbook",
        "name": "Load Agent Playbook",
        "payload": {}
    }' -v -H "X-ACTION-TOKEN: $ACTION_API_SERVER_TOKEN"

if [[ $CLICKHOUSE_ENABLED == "true" ]]; then
    click_hostname="${CLICKHOUSE_HOST##*://}"
    click_hostname="${click_hostname%%:*}"
    echo "running clickhouse migrations on host: $click_hostname"
    migrate -path ./migrations/clickhouse -database "clickhouse://$click_hostname:9000?username=$CLICKHOUSE_USER&password=$CLICKHOUSE_PASSWORD&database=default&x-multi-statement=true&x-cluster-name=default" up
fi

hasura metadata inconsistency status --endpoint "$HASURA_GRAPHQL_ENDPOINT" --admin-secret "$HASURA_GRAPHQL_ADMIN_SECRET" --skip-update-check

echo "Running RabbitMQ migrations..."
until curl -sf -u "$RABBIT_MQ_USERNAME:$RABBIT_MQ_PASSWORD" "http://$RABBIT_MQ_HOST:15672/api/overview" > /dev/null; do
  echo "Waiting for RabbitMQ management API..."
  sleep 3
done
for script in ./migrations/rabbitmq/*.sh; do
  echo "running: $script"
  sh "$script"
done

