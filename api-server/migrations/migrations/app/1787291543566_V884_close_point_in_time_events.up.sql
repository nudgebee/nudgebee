-- Close the backlog of point-in-time configuration_change events that no
-- closer can ever reach (#36597). They describe a moment that already
-- passed, not a recoverable condition; the discovery sweep that used to
-- bulk-close them was removed (#36551/#36567) because it was also closing
-- live incidents, and this kind has had no closer since.
--
-- One statement, one transaction, no index build. Bounded by status='FIRING'
-- plus a narrow finding_type predicate (tens to low thousands of rows per
-- install). Expected lock window: sub-second. No CONCURRENTLY, so nothing to
-- pre-apply out-of-band.
UPDATE events
   SET status     = 'CLOSED',
       ends_at    = COALESCE(ends_at, starts_at),
       nb_status  = CASE
                      WHEN nb_status IN ('OPEN', 'ACKNOWLEDGED', 'INVESTIGATING', 'ACTION_REQUIRED')
                      THEN 'RESOLVED' ELSE nb_status
                    END,
       updated_at = now()
 WHERE status = 'FIRING'
   AND finding_type = 'configuration_change';
