#!/bin/bash
# Migration: remove auto-pilot queues and exchanges
# Reason: auto-pilot module removed in PR #24914
# Safe to re-run: delete is idempotent (logs skip if already deleted)

BASE="http://${RABBIT_MQ_HOST}:15672/api"
VH="%2F"

delete_queue() {
  resp=$(curl -sf -u "$RABBIT_MQ_USERNAME:$RABBIT_MQ_PASSWORD" -X DELETE "$BASE/queues/$VH/$1" 2>&1)
  if [ $? -eq 0 ]; then
    echo "deleted queue: $1 | response: $resp"
  else
    echo "skipped queue (not found or already deleted): $1 | response: $resp"
  fi
}

delete_exchange() {
  resp=$(curl -sf -u "$RABBIT_MQ_USERNAME:$RABBIT_MQ_PASSWORD" -X DELETE "$BASE/exchanges/$VH/$1" 2>&1)
  if [ $? -eq 0 ]; then
    echo "deleted exchange: $1 | response: $resp"
  else
    echo "skipped exchange (not found or already deleted): $1 | response: $resp"
  fi
}

echo "--- 001_remove_autopilot_queues: start ---"

delete_queue "auto_pilot-task"
delete_queue "auto_pilot-task.dlq"
delete_queue "auto_playbook_pending_task"
delete_queue "auto_playbook_pending_task.dlq"
delete_queue "auto_playbook_resolved_task"
delete_queue "auto_playbook_resolved_task.dlq"
delete_queue "auto_playbook_task"
delete_queue "auto_playbook_task.dlq"

delete_exchange "autopilot"
delete_exchange "auto_playbook_pending_task"
delete_exchange "auto_playbook_resolved_task"
delete_exchange "auto_playbook_task"

echo "--- 001_remove_autopilot_queues: done ---"
