-- Align the stored availability SLI queries with coroot's failed-request
-- definition (model.IsRequestStatusFailed: 5xx, "failed", non-OK gRPC).
--
-- slo_config stores the PromQL at create time, so fixing the Go query builder
-- only affects newly created or updated configs. Existing rows keep measuring
-- 2xx/(2xx+5xx) -- with 3xx and 4xx counted in neither the numerator nor the
-- denominator -- until they are rewritten here.
--
-- Prometheus regexes are fully anchored, so grpc:OK is excluded by requiring
-- the character after "grpc:O" to be something other than K.

UPDATE "public"."slo_config"
SET filter_good_query = format(
        'container_http_requests_total{destination_workload_namespace="%s", destination_workload_name="%s", status!~"5..|failed|grpc:[^O].*|grpc:O[^K].*"}',
        workload_namespace, workload_name),
    filter_bad_query = format(
        'container_http_requests_total{destination_workload_namespace="%s", destination_workload_name="%s", status=~"5..|failed|grpc:[^O].*|grpc:O[^K].*"}',
        workload_namespace, workload_name)
WHERE lower("name") = 'availability'
  AND filter_good_query LIKE '%container_http_requests_total%';

-- Drop the dead destination_workload_namespace!="external" matcher: the same
-- label is already pinned to an exact namespace by the preceding matcher, so it
-- never excluded anything, but it made latency and availability look like they
-- measured different request populations.
UPDATE "public"."slo_config"
SET histogram_query = format(
        'container_http_requests_duration_seconds_total_bucket{destination_workload_namespace="%s", destination_workload_name="%s"}',
        workload_namespace, workload_name)
WHERE lower("name") = 'latency'
  AND histogram_query LIKE '%container_http_requests_duration_seconds_total_bucket%';
