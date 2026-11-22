#!/bin/bash
# Migration: give the ml-recommendation queue a 24h consumer timeout.
#
# ml-k8s-server acks each rightsizing message only when its scan ends, and scans
# on large clusters run for hours. Under the broker-wide consumer_timeout (1h in
# the chart, shorter on some installs) the broker would close the channel of
# every longer scan: the pod drops out of the queue's consumer list for the rest
# of the scan (RabbitmqNoQueueConsumer fires on single-replica installs), and the
# message is redelivered and run a second time while the first is still going.
# 24h covers one daily run.
#
# A policy, not an x-consumer-timeout queue argument: the queue already exists
# without that argument, and redeclaring it with one answers PRECONDITION_FAILED
# (406) on every reconnect. A policy applies to the existing queue with no
# redeclare. RabbitMQ applies one policy per queue, the highest priority match.
#
# Safe to re-run: PUT on a policy is a full replace, so re-running converges.

BASE="http://${RABBIT_MQ_HOST}:15672/api"
VH="%2F"

echo "--- 003_ml_recommendation_consumer_timeout: start ---"

resp=$(curl -sSf -u "$RABBIT_MQ_USERNAME:$RABBIT_MQ_PASSWORD" \
  -H "content-type: application/json" \
  -X PUT "$BASE/policies/$VH/nb-ml-recommendation" -d '{
  "pattern": "^ml-recommendation$",
  "apply-to": "queues",
  "priority": 1,
  "definition": {
    "consumer-timeout": 86400000
  }
}' 2>&1)

if [ $? -eq 0 ]; then
  echo "applied policy: nb-ml-recommendation"
else
  # Loud, but exit 0 for the same reason as 002: a non-zero exit fails the whole
  # install or upgrade with no retry. Until this is re-run, scans that outlast the
  # broker-wide timeout lose their consumer for the rest of the scan, as above.
  echo "FAILED to apply policy: nb-ml-recommendation | response: $resp" >&2
  echo "WARNING: rightsizing scans that outlast the broker consumer_timeout lose their consumer until this migration is re-run." >&2
fi

echo "--- 003_ml_recommendation_consumer_timeout: done ---"
