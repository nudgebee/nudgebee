-- Reverse the backfill: restore every alert this migration closed to the status
-- it held before, and remove the history rows that recorded the change.
--
-- Exact, not approximate: the up migration wrote each alert's previous status
-- into event_history.old_value and its previous ends_at into
-- metadata->>'prev_ends_at', so both are restored from the record rather than
-- guessed. Alerts closed by anything other than this migration carry a
-- different change_reason and are not touched.

WITH restored AS (
    SELECT h.event_id,
           h.old_value #>> '{}'                                   AS old_status,
           (h.metadata ->> 'prev_ends_at')::timestamp             AS prev_ends_at
      FROM event_history h
     WHERE h.change_reason = 'resource_inactive_backfill'
)
UPDATE events e
   SET status     = r.old_status,
       ends_at    = r.prev_ends_at,
       updated_at = now() AT TIME ZONE 'utc'
  FROM restored r
 WHERE e.id = r.event_id
   AND e.status = 'CLOSED';

DELETE FROM event_history WHERE change_reason = 'resource_inactive_backfill';
