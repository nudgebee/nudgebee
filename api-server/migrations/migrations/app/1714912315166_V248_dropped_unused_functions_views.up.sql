

DROP FUNCTION "public"."auto_playbook_groupings"("pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."json");

DROP FUNCTION "public"."cloud_resource_metrics_groupings"("pg_catalog"."_text", "pg_catalog"."json", "pg_catalog"."text", "pg_catalog"."int4", "pg_catalog"."int4", "pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."json");

DROP FUNCTION "public"."event_groupings"("pg_catalog"."_text", "pg_catalog"."json", "pg_catalog"."text", "pg_catalog"."int4", "pg_catalog"."int4", "pg_catalog"."int4", "pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."json");

DROP FUNCTION "public"."recommendation_groupings"("pg_catalog"."_text", "pg_catalog"."json", "pg_catalog"."int4", "pg_catalog"."int4", "pg_catalog"."json");

DROP FUNCTION "public"."spend_groupings"("pg_catalog"."_text", "pg_catalog"."json", "pg_catalog"."text", "pg_catalog"."int4", "pg_catalog"."int4", "pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."json");

DROP FUNCTION "public"."search_auto_playbook"("pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."int4", "pg_catalog"."int4", "pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."json");

DROP FUNCTION "public"."ticket_groupings"("pg_catalog"."_text", "pg_catalog"."json", "pg_catalog"."json");

DROP FUNCTION "public"."k8s_pod_groupings"("pg_catalog"."_text", "pg_catalog"."json", "pg_catalog"."text", "pg_catalog"."int4", "pg_catalog"."int4", "pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."json");

DROP VIEW "public"."ticket_groupings_type";

DROP VIEW "public"."spend_groupings_type";

DROP VIEW "public"."recommendation_groupings_type";

DROP VIEW "public"."event_groupings_type";

DROP VIEW "public"."cloud_resource_metrics_groupings_type";

DROP VIEW "public"."auto_playbook_groupings_type";

DROP VIEW "public"."k8s_pod_groupings_type";

DROP VIEW "public"."cloud_resource_metrics_daily_aggregate";

DROP VIEW "public"."cloud_services_aggregate";
