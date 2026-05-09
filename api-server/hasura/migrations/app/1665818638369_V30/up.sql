
CREATE OR REPLACE VIEW "public"."compliance_check_findings_count_aggregate" AS 
 SELECT ccf.tenant,
    (ccf.created_at)::date AS created_at_date,
    count(*) AS count,
    ccf.business_unit,
    cc.project,
    cc.account
   FROM compliance_check_findings ccf
   JOIN compliance_check cc on cc.id = ccf.compliance_check
  GROUP BY ccf.tenant, ccf.business_unit, cc.project, cc.account, ((ccf.created_at)::date)
  ORDER BY ((ccf.created_at)::date);
