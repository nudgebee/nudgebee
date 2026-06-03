INSERT INTO "public"."event_log_analysis_status"("description", "value") VALUES (E'Log analysis is in processing state', E'IN_PROGRESS') ON CONFLICT DO NOTHING;
INSERT INTO "public"."event_log_analysis_status"("description", "value") VALUES (E'Log analysis has completed', E'COMPLETED') ON CONFLICT DO NOTHING;

