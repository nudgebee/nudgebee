
-- Only the constraint half is reversible. Restores V594's allowlist, kept
-- idempotent + NOT VALID for the same reasons as the .up.sql.
--
-- The purged rows are NOT restored and cannot be: they held only the filter
-- array they were built from, and nothing else in the schema retains that
-- content, so there is no source to recover them from. Rolling this migration
-- back leaves the table as the purge left it.
alter table "public"."user_history" drop constraint if exists "module_check";
alter table "public"."user_history" add constraint "module_check" check (module = ANY (ARRAY['log_query_azure_app_insights'::text, 'log_query_observe'::text, 'log_query_datadog'::text, 'log_query_loggly'::text, 'log_query_signoz'::text, 'log_query_es'::text, 'log_query_loki'::text, 'metrics_query_prometheus'::text, 'metrics_query_datadog'::text])) not valid;
