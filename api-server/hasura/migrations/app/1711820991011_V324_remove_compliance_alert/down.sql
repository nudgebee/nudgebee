
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."alert_rule_source";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."alert_rule_state";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."alert_rule_status";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."alert_rules";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."alert_history";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."alert_metrices_view";


-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."compliance_severity_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."compliance_check_finding_status_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."compliance";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."compliance_rules";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."compliance_check";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."compliance_rule_executions";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."compliance_findings";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."compliance_check_findings";

alter table "public"."compliance_rules"
  add constraint "compliance_rules_compliance_id_fkey"
  foreign key ("compliance_id")
  references "public"."compliance"
  ("id") on update cascade on delete cascade;

alter table "public"."compliance_rules"
  add constraint "compliance_rules_severity_fkey"
  foreign key ("severity")
  references "public"."compliance_severity_type"
  ("value") on update no action on delete no action;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- drop table compliance_check_status_type cascade;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- drop table compliance_check_type cascade;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- drop table compliance_rule_execution_status_type cascade;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- drop table compliance_standard cascade;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."compliance_standard_check_mappings";
