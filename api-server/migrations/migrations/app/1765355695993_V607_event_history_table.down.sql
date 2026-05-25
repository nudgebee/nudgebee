
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- ALTER TABLE event_history ADD CONSTRAINT event_history_change_type_check
-- CHECK (change_type IN ('priority', 'status', 'urgency', 'evidences', 'labels', 'ends_at', 'updated_at'));

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP INDEX idx_event_history_event;
-- CREATE INDEX idx_event_history_event ON event_history(event_id, changed_at DESC);

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP INDEX idx_event_history_event;
-- CREATE INDEX idx_event_history_event ON event_history(event_id, changed_at DESC);

DROP INDEX IF EXISTS "public"."idx_event_history_type";

DROP INDEX IF EXISTS "public"."idx_event_history_tenant";

DROP INDEX IF EXISTS "public"."idx_event_history_event";

DROP TABLE "public"."event_history";
