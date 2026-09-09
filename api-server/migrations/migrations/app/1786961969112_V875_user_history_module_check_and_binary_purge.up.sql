
-- Query history is now recorded by api-server rather than the browser, and the
-- module string is derived from the RESOLVED provider instead of frontend state.
-- Two consequences, handled here.

-- 1. Every provider the backend can resolve must be allowlisted, or the insert
--    fails -- and because the write is fire-and-forget, that failure is
--    invisible to the user.
--
--    26 values = the 14 providers in getLogSource (service.go) plus the 10 in
--    getMetricsSource, lowercased so ES -> es, plus both Victoria Metrics
--    spellings (KubernetesCreateAlert.tsx lists the hyphen and the underscore
--    form, and neither resolves in getMetricsSource, so those rows are
--    FAILED-only by construction).
--
--    Deliberately NOT added: the legacy uppercase 'metrics_query_ES'. The
--    backend lowercases, so encoding that casing bug here would make it
--    permanent; QueryMetrics.tsx now lowercases its read module to match.
--
--    if exists / not valid are both load-bearing: dev has drifted and carries
--    no module_check at all, so a bare drop errors; and hundreds of
--    pre-existing rows (metrics_query_newrelic, metrics_query_ES,
--    prometheus_queries_enricher, ...) violate the list, so a validating ADD
--    CONSTRAINT would fail the migration Job. NOT VALID still enforces every
--    new INSERT, which is the point.
alter table "public"."user_history" drop constraint if exists "module_check";
alter table "public"."user_history" add constraint "module_check" check (module = ANY (ARRAY['log_query_loki'::text, 'log_query_signoz'::text, 'log_query_datadog'::text, 'log_query_observe'::text, 'log_query_loggly'::text, 'log_query_azure_app_insights'::text, 'log_query_aws_cloudwatch'::text, 'log_query_es'::text, 'log_query_newrelic'::text, 'log_query_splunk_observability_platform'::text, 'log_query_dynatrace'::text, 'log_query_solarwinds'::text, 'log_query_pinot'::text, 'log_query_hive'::text, 'metrics_query_prometheus'::text, 'metrics_query_datadog'::text, 'metrics_query_chronosphere'::text, 'metrics_query_aws_cloudwatch'::text, 'metrics_query_azure_app_insights'::text, 'metrics_query_newrelic'::text, 'metrics_query_splunk_observability_platform'::text, 'metrics_query_es'::text, 'metrics_query_dynatrace'::text, 'metrics_query_solarwinds'::text, 'metrics_query_victoria-metrics'::text, 'metrics_query_victoria_metrics'::text])) not valid;

-- 2. Purge the rows that recorded the raw filter array.
--
--    Builder-mode queries used to store the filter array they were built from,
--    e.g. [{"_binary":{"container":{"_eq":"accounting"}}}], instead of the
--    provider-native query that actually ran. Those rows are unreadable in the
--    History modal and cannot be re-run, and the backend now records the
--    resolved query, so they are dead weight rather than history.
--
--    Predicate note: strpos, not LIKE '%_binary%' -- in LIKE, `_` is a
--    single-char wildcard, so that pattern also matches any "<x>binary"
--    substring.
--
--    Verified against dev: matches 860 rows across 8 modules (log_query_loki
--    538, log_query_es 161, log_query_newrelic 46, metrics_query_ES 29,
--    log_query_observe 28, log_query_dynatrace 27, log_query_loggly 26,
--    log_query_signoz 5), all array-shaped. Rows written by the relay's
--    track_history path (prometheus_queries_enricher etc.) do not match and are
--    left untouched.
DELETE FROM public.user_history WHERE strpos(data, '"_binary"') > 0;
