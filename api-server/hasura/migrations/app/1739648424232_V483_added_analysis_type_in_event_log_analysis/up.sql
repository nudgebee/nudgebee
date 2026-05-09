alter table "public"."event_log_analysis" add column IF NOT EXISTS "analysis_type" varchar
 not null default 'log_analysis';
