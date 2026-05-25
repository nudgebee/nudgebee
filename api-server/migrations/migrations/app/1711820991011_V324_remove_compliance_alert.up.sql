

DROP table "public"."compliance_standard_check_mappings";

drop table compliance_standard cascade;

drop table compliance_rule_execution_status_type cascade;

drop table compliance_check_type cascade;

drop table compliance_check_status_type cascade;

alter table "public"."compliance_rules" drop constraint "compliance_rules_severity_fkey";

alter table "public"."compliance_rules" drop constraint "compliance_rules_compliance_id_fkey";

DROP table "public"."compliance_check_findings";

DROP table "public"."compliance_findings";

DROP table "public"."compliance_rule_executions";

DROP table "public"."compliance_check";

DROP table "public"."compliance_rules";

DROP table "public"."compliance";

DROP table "public"."compliance_check_finding_status_type";

DROP table "public"."compliance_severity_type";

DROP VIEW "public"."alert_metrices_view";

DROP table "public"."alert_history";

DROP table "public"."alert_rules";

DROP table "public"."alert_rule_status";

DROP table "public"."alert_rule_state";

DROP table "public"."alert_rule_source";
