alter table "public"."event_log_analysis"
  add constraint "event_log_analysis_status_fkey"
  foreign key ("status")
  references "public"."event_log_analysis_status"
  ("value") on update restrict on delete restrict;
