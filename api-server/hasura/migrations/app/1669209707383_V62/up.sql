
CREATE
OR REPLACE VIEW "public"."spends_project_monthly_aggregate" AS
SELECT
  s.tenant,
  date_part('year', s."date") as date_year,
  date_part('month', s."date") as date_month, 
  p.id AS project_id,
  p.name AS project_name,
  sum(s.amount) AS sum
FROM
  (
    spends s
    JOIN (
      (
        cloud_accounts c
        JOIN project_accounts pa ON ((pa.account_id = c.id))
      )
      JOIN projects p ON ((p.id = pa.project_id))
    ) ON ((c.id = s.cloud_account))
  )
GROUP BY
  s.tenant,
  p.id,
  p.name,
  date_part('year', s."date"),
  date_part('month', s."date")
order by
  s.tenant,
  p.id,
  date_part('year', s."date"),
  date_part('month', s."date");
