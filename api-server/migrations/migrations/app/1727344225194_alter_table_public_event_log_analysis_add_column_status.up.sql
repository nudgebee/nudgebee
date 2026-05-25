alter table "public"."event_log_analysis" add column "status" text
 not null default 'COMPLETED';
