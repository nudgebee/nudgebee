
alter table "public"."event_log_analysis" add column "summary" text
 null;

alter table "public"."event_log_analysis" add column "event_fingerprint" text
 null;

alter table "public"."event_log_analysis" add column "updated_at" timestamp
 null default now();
