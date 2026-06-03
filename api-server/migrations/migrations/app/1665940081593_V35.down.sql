
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE
-- OR REPLACE VIEW "public"."compliance_standard_finding_status_count_aggregate" AS
--     select cs.id, cs.name, cs.tenant, cs.business_unit, cs.project, ccf.status, count(*)
--     from compliance_check_findings ccf
--     join compliance_standard_check_mappings cscm on ccf.compliance_check = cscm.check_id
--     left outer join compliance_standard cs on cscm.standard_id = cs.id
--     group by cs.id, cs.name, cs.tenant, cs.business_unit, cs.project, ccf.status;
