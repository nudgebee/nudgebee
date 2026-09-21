#!/bin/bash
# Migration: bound the k8s-collector agent queues by age and by size.
#
# Why a policy and not a queue argument: the collector's declare has never
# actually carried its arguments. rabbitmq_client.py builds its queues with
# kombu's Queue(arguments=...), but kombu's attribute is `queue_arguments` and
# it drops unknown kwargs silently -- so x-message-ttl and x-dead-letter-*
# were never sent, and the live queues confirm it (`arguments: {}` on
# k8s_agent_discovery, against relay's queues on the same broker which do carry
# theirs). The practical effect is that NOTHING has ever removed a message from
# these queues except a successful consume: no expiry, no cap, and -- with no
# dead-letter-exchange -- reject(requeue=False) discards rather than parks.
# A collector that stalls therefore grows the broker's disk until the volume is
# gone, which is what it did on a customer install.
#
# Fixing the declare is necessary but cannot apply here: these queues already
# exist without those arguments, so declaring them with arguments now answers
# with PRECONDITION_FAILED (406) and, because _declare_topology retries forever
# from inside the consumer constructor at gunicorn import time, would leave
# every collector pod unable to boot. A policy applies to queues that already
# exist, needs no redeclare, and covers queues created later -- including on
# installs that never get the code change.
#
# Safe to re-run: PUT on a policy is a full replace, so re-running converges.

BASE="http://${RABBIT_MQ_HOST}:15672/api"
VH="%2F"

failures=0

put_policy() {
  name=$1
  body=$2
  # -S alongside -s: keep the progress meter quiet but let curl still print why
  # it failed, so `resp` carries a reason. Without it `-s` swallows the message
  # and the failure logs as "response:" with nothing after it.
  resp=$(curl -sSf -u "$RABBIT_MQ_USERNAME:$RABBIT_MQ_PASSWORD" \
    -H "content-type: application/json" \
    -X PUT "$BASE/policies/$VH/$name" -d "$body" 2>&1)
  if [ $? -eq 0 ]; then
    echo "applied policy: $name"
    return 0
  fi
  echo "FAILED to apply policy: $name | response: $resp" >&2
  failures=$((failures + 1))
  return 1
}

echo "--- 002_collector_queue_limits: start ---"

# Agent ingest queues. 1h expiry restores the bound the declare intended;
# 512MB is the hard stop for a burst that arrives faster than 1h of drain.
# drop-head sheds the oldest first, which is the right end for these: the
# reconcile is gated on a complete batch sequence (batch_reconcile.py), so an
# incomplete snapshot fails closed -- it skips its cleanup rather than
# deactivating resources it did not see.
put_policy "nb-collector-agent-queues" '{
  "pattern": "^k8s_agent_(discovery|events|spend)$",
  "apply-to": "queues",
  "priority": 1,
  "definition": {
    "message-ttl": 3600000,
    "max-length-bytes": 536870912,
    "overflow": "drop-head"
  }
}'

# The .dlq queues are unreachable today (no dead-letter-exchange is set on the
# queues that would feed them). Bounding them now means that whenever the
# declare is fixed and dead-lettering starts working, the graveyard is capped
# from its first message rather than after the next incident.
put_policy "nb-collector-dlq" '{
  "pattern": "^k8s_agent_.*\\.dlq$",
  "apply-to": "queues",
  "priority": 1,
  "definition": {
    "message-ttl": 86400000,
    "max-length-bytes": 134217728,
    "overflow": "drop-head"
  }
}'

if [ "$failures" -ne 0 ]; then
  # Loud, but deliberately still exit 0. run-migrations.sh runs under `set -e`
  # and the migration Job is a post-install,post-upgrade Helm hook with
  # backoffLimit: 0, so a non-zero exit here fails the whole install or upgrade
  # -- after the Postgres migrations have already applied, and with no retry.
  # A queue policy that did not land is worth shouting about and is fixed by
  # re-running the job; it is not worth leaving a customer mid-upgrade. The
  # queues stay unbounded until then, which is the state they are in today.
  echo "WARNING: $failures queue-limit policy/policies did not apply. The k8s_agent_* queues" >&2
  echo "are UNBOUNDED until this migration is re-run. Check the RabbitMQ credentials above." >&2
fi

echo "--- 002_collector_queue_limits: done ---"
