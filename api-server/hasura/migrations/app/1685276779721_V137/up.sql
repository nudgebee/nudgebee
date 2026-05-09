
CREATE OR REPLACE VIEW "public"."cloud_services_aggregate" AS 
 SELECT cr.tenant,
    cr.region,
    cr.account,
    cr.service_name,
    count(*) AS resource_count,
    s.date AS spend_date,
    sum(s.amount) AS spend_amount,
    sum(r.estimated_savings) AS resource_estimated_saving
   FROM ((cloud_resourses cr
     LEFT JOIN (select cloud_resource_id, date, sum(amount) as amount from spends group by cloud_resource_id, date) s ON ((s.cloud_resource_id = cr.id)))
     LEFT JOIN (select resource_id, sum(estimated_savings) as estimated_savings from recommendation group by resource_id) r ON ((cr.id = r.resource_id)))
  GROUP BY cr.tenant, cr.region, cr.account, cr.service_name, s.date;

CREATE OR REPLACE VIEW "public"."cloud_services_aggregate" AS 
 SELECT cr.tenant,
    cr.region,
    cr.account,
    cr.service_name,
    count(*) AS resource_count,
    s.date AS spend_date,
    sum(s.amount) AS spend_amount,
    sum(r.estimated_savings) AS resource_estimated_saving
   FROM cloud_resourses cr
   LEFT JOIN ( SELECT recommendation.resource_id,
            sum(recommendation.estimated_savings) AS estimated_savings
           FROM recommendation
          GROUP BY recommendation.resource_id) r ON (cr.id = r.resource_id)
   JOIN ( SELECT spends.cloud_resource_id,
            spends.date,
            sum(spends.amount) AS amount
           FROM spends
          GROUP BY spends.cloud_resource_id, spends.date) s ON (s.cloud_resource_id = cr.id)
  GROUP BY cr.tenant, cr.region, cr.account, cr.service_name, s.date;
