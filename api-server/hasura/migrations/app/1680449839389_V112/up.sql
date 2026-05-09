
CREATE OR REPLACE VIEW "public"."spends_account_date_aggregate" AS 
 SELECT date_part('year'::text, spends.date) AS date_year,
    date_part('month'::text, spends.date) AS date_month,
    sum(spends.amount) AS sum,
    spends.cloud_account,
    ca.account_name,
    date_part('day'::text, spends.date) AS date_day
   FROM (spends
     JOIN cloud_accounts ca ON ((ca.id = spends.cloud_account)))
  GROUP BY spends.cloud_account, (date_part('year'::text, spends.date)), (date_part('month'::text, spends.date)), (date_part('day'::text, spends.date)), ca.account_name
  ORDER BY spends.cloud_account, (date_part('year'::text, spends.date)), (date_part('month'::text, spends.date)), (date_part('day'::text, spends.date));

CREATE OR REPLACE VIEW "public"."spends_account_date_aggregate" AS 
 SELECT date_part('year'::text, spends.date) AS date_year,
    date_part('month'::text, spends.date) AS date_month,
    sum(spends.amount) AS sum,
    spends.cloud_account,
    ca.account_name,
    date_part('day'::text, spends.date) AS date_day
   FROM (spends
     JOIN cloud_accounts ca ON ((ca.id = spends.cloud_account)))
  GROUP BY spends.cloud_account, (date_part('year'::text, spends.date)), (date_part('month'::text, spends.date)), (date_part('day'::text, spends.date)), ca.account_name
  ORDER BY spends.cloud_account, (date_part('year'::text, spends.date)), (date_part('month'::text, spends.date)), (date_part('day'::text, spends.date));

CREATE OR REPLACE VIEW "public"."spends_account_date_aggregate" AS 
 SELECT date_part('year'::text, spends.date) AS date_year,
    date_part('month'::text, spends.date) AS date_month,
    sum(spends.amount) AS sum,
    spends.cloud_account,
    ca.account_name,
    date_part('day'::text, spends.date) AS date_day,
    spends.tenant
   FROM (spends
     JOIN cloud_accounts ca ON ((ca.id = spends.cloud_account)))
  GROUP BY spends.cloud_account, (date_part('year'::text, spends.date)), (date_part('month'::text, spends.date)), (date_part('day'::text, spends.date)), ca.account_name, spends.tenant
  ORDER BY spends.cloud_account, (date_part('year'::text, spends.date)), (date_part('month'::text, spends.date)), (date_part('day'::text, spends.date));

CREATE OR REPLACE VIEW "public"."spends_account_date_aggregate" AS 
 SELECT date_part('year'::text, spends.date) AS date_year,
    date_part('month'::text, spends.date) AS date_month,
    sum(spends.amount) AS sum,
    spends.cloud_account,
    ca.account_name,
    date_part('day'::text, spends.date) AS date_day,
    spends.tenant,
    make_date(date_part('year'::text, spends.date)::int, date_part('month'::text, spends.date)::int,date_part('day'::text, spends.date)::int) as "date"
   FROM (spends
     JOIN cloud_accounts ca ON ((ca.id = spends.cloud_account)))
  GROUP BY spends.cloud_account, (date_part('year'::text, spends.date)), (date_part('month'::text, spends.date)), (date_part('day'::text, spends.date)), ca.account_name, spends.tenant, "date"
  ORDER BY spends.cloud_account, (date_part('year'::text, spends.date)), (date_part('month'::text, spends.date)), (date_part('day'::text, spends.date));
