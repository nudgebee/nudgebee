
alter table "public"."user_history" drop constraint "module_check";
alter table "public"."user_history" add constraint "module_check" check (module = ANY (ARRAY['log_query_azure_app_insights'::text, 'log_query_observe'::text, 'log_query_datadog'::text, 'log_query_loggly'::text, 'log_query_signoz'::text, 'log_query_es'::text, 'log_query_loki'::text, 'metrics_query_prometheus'::text]));
