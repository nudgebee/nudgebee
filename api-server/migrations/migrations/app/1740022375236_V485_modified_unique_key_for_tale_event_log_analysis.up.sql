alter table "public"."event_log_analysis" drop constraint "event_log_analysis_event_id_key";
alter table "public"."event_log_analysis" add constraint "event_log_analysis_event_id_analysis_type_key" unique ("event_id", "analysis_type");
