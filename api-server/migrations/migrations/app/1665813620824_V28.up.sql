CREATE
OR REPLACE VIEW "public"."spends_project_aggregate" AS
SELECT
  s.tenant,
  s.cloud_account,
  sum(s.amount),
  p.id AS project_id,
  p.name AS project_name
FROM
  spends s 
  inner join 
  cloud_accounts c
  inner join project_accounts pa on pa.account_id = c.id
  inner join projects p on p.id = pa.project_id
  on c.id = s.cloud_account
GROUP BY
  s.tenant,
  s.cloud_account,
  p.id ,
  p.name;
