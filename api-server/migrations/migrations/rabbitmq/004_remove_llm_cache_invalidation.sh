#!/bin/bash
# Migration: remove the llm_cache_invalidation fanout exchange
# Reason: its last publisher (relay-server) and consumer (llm-server) moved to
# the Redis replica broadcast nb_replica_broadcast (#40807). The per-pod queues
# (llm_cache_invalidation_<pod>) were auto-delete + exclusive and are already
# gone; only the durable exchange remains.
# Safe to re-run: delete is idempotent (logs skip if already deleted). An old
# relay pod still publishing during a rollout re-declares the exchange; the next
# run removes it again.

BASE="http://${RABBIT_MQ_HOST}:15672/api"
VH="%2F"

# Never fails the deploy: anything other than deleted / already gone is a
# warning, and the script re-runs on the next deploy.
delete_exchange() {
  status=$(curl -sS --max-time 30 -u "$RABBIT_MQ_USERNAME:$RABBIT_MQ_PASSWORD" -o /dev/null -w "%{http_code}" -X DELETE "$BASE/exchanges/$VH/$1")
  rc=$?
  if [ $rc -ne 0 ]; then
    echo "WARN: could not reach RabbitMQ to delete exchange $1 (curl exit $rc)" >&2
  elif [ "$status" = "204" ]; then
    echo "deleted exchange: $1"
  elif [ "$status" = "404" ]; then
    echo "skipped exchange (not found or already deleted): $1"
  else
    echo "WARN: deleting exchange $1 returned HTTP $status" >&2
  fi
}

echo "--- 004_remove_llm_cache_invalidation: start ---"

delete_exchange "llm_cache_invalidation"

echo "--- 004_remove_llm_cache_invalidation: done ---"
