
CREATE OR REPLACE VIEW "public"."compliance_account_execution" AS 
 SELECT min(e.status) AS status,
    max(e.last_execution) AS last_execution,
    e.compliance_id,
    e.account_id,
    c.name AS compliance_name,
    ca.account_name,
    count(*) AS rule_count,
    ca.tenant
   FROM ((compliance_rule_executions e
     JOIN compliance c ON ((c.id = e.compliance_id)))
     JOIN cloud_accounts ca ON ((e.account_id = ca.id)))
  GROUP BY c.name, ca.account_name, e.compliance_id, e.account_id, ca.tenant;
