-- One-shot purge of the pre-V805 AUTOPILOT_UPDATE audit backlog: delete rows whose prev/current
-- state differ ONLY in the 4 scheduler bookkeeping fields (pure churn); genuine user edits are kept.
DELETE FROM audit
WHERE event_type = 'AUTOPILOT_UPDATE'
  AND event_action = 'UPDATE'
  AND event_prev_state IS NOT NULL
  AND event_state IS NOT NULL
  AND (event_prev_state::jsonb - '{execution_status,last_schedule_time,last_executed_time,next_schedule_time}'::text[])
    = (event_state::jsonb - '{execution_status,last_schedule_time,last_executed_time,next_schedule_time}'::text[]);
