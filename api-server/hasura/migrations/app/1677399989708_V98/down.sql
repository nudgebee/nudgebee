
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE OR REPLACE VIEW "public"."compliance_account_execution" AS select min(e.status) as status ,max(last_execution) as last_execution, compliance_id, account_id,c.name as compliance_name, ca.account_name as account_name, count(*) as rule_count  from compliance_rule_executions e inner join compliance c on c.id=e.compliance_id inner join cloud_accounts ca on e.account_id=ca.id   group by c.name, ca.account_name,compliance_id, account_id;
