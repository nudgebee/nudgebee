-- Restore the previous 2xx/5xx availability filters and the dead external
-- namespace matcher on the latency histogram.

UPDATE "public"."slo_config"
SET filter_good_query = format(
        'container_http_requests_total{destination_workload_namespace="%s",destination_workload_name="%s" , status=~"2.."}',
        workload_namespace, workload_name),
    filter_bad_query = format(
        'container_http_requests_total{destination_workload_namespace="%s", destination_workload_name="%s", status=~"5.."}',
        workload_namespace, workload_name)
WHERE lower("name") = 'availability'
  AND filter_good_query LIKE '%container_http_requests_total%';

UPDATE "public"."slo_config"
SET histogram_query = format(
        'container_http_requests_duration_seconds_total_bucket{destination_workload_namespace="%s", destination_workload_name="%s", destination_workload_namespace!="external"}',
        workload_namespace, workload_name)
WHERE lower("name") = 'latency'
  AND histogram_query LIKE '%container_http_requests_duration_seconds_total_bucket%';
