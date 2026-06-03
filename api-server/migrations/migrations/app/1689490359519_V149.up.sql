
CREATE OR REPLACE VIEW "public"."cloud_services_aggregate" AS 
 SELECT cr.tenant,
    cr.region,
    cr.account,
    cr.service_name,
    count(*) AS resource_count,
    s.date AS spend_date,
    sum(s.amount) AS spend_amount,
    sum(r.estimated_savings) AS resource_estimated_saving,
    a.account_name as account_name,
    a.cloud_provider as account_cloud_provider
   FROM ((cloud_resourses cr
     LEFT JOIN ( SELECT recommendation.resource_id,
            sum(recommendation.estimated_savings) AS estimated_savings
           FROM recommendation
          GROUP BY recommendation.resource_id) r ON ((cr.id = r.resource_id)))
     JOIN ( SELECT spends.cloud_resource_id,
            spends.date,
            sum(spends.amount) AS amount
           FROM spends
          GROUP BY spends.cloud_resource_id, spends.date) s ON ((s.cloud_resource_id = cr.id)))
     JOIN cloud_accounts a on a.id = cr.account
  GROUP BY cr.tenant, cr.region, cr.account, cr.service_name, s.date, a.account_name, a.cloud_provider;

CREATE OR REPLACE VIEW "public"."cloud_account_service_groupings_type" AS 
 SELECT cloud_services_aggregate.tenant AS tenant_id,
    cloud_services_aggregate.account AS account_id,
    cloud_services_aggregate.service_name,
    cloud_services_aggregate.region,
    cloud_services_aggregate.spend_date,
    sum(cloud_services_aggregate.resource_count) AS resource_count,
    sum(cloud_services_aggregate.spend_amount) AS spend_amount,
    sum(cloud_services_aggregate.resource_estimated_saving) AS resource_estimated_saving,
    cloud_services_aggregate.account_name AS account_name,
    cloud_services_aggregate.account_cloud_provider AS account_cloud_provider
   FROM cloud_services_aggregate
  WHERE false
  GROUP BY cloud_services_aggregate.tenant, cloud_services_aggregate.account, cloud_services_aggregate.service_name, cloud_services_aggregate.region, cloud_services_aggregate.spend_date, cloud_services_aggregate.account_name, cloud_services_aggregate.account_cloud_provider;

CREATE OR REPLACE FUNCTION public.cloud_account_service_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF cloud_account_service_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
SELECT
  (
    CASE
      WHEN 'tenant_id' = ANY(group_by) THEN tenant
      ELSE NULL
    END
  ) AS tenant_id,
  (
    CASE
      WHEN 'account_id' = ANY(group_by) THEN account
      ELSE NULL
    END
  ) AS account_id,
  (
    CASE
      WHEN 'service_name' = ANY(group_by) THEN service_name
      ELSE NULL
    END
  ) AS service_name,
  (
    CASE
      WHEN 'region' = ANY(group_by) THEN region
      ELSE NULL
    END
  ) AS region,
  (
    CASE
      WHEN 'spend_date' = ANY(group_by) THEN spend_date
      ELSE NULL
    END
  ) AS spend_date,
  sum(resource_count) as resource_count,
  sum(spend_amount) as spend_amount,
  sum(resource_estimated_saving) as resource_estimated_saving,
  (
    CASE
      WHEN 'account_name' = ANY(group_by) THEN account_name
      ELSE NULL
    END
  ) AS account_name,
  (
    CASE
      WHEN 'account_cloud_provider' = ANY(group_by) THEN account_cloud_provider
      ELSE NULL
    END
  ) AS account_cloud_provider
from
  cloud_services_aggregate
where
  (
    "hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL
    OR (
      "tenant" = ("hasura_session" ->> 'x-hasura-user-tenant-id') :: uuid
    )
  )
  AND (
    "where" #>> '{account_id,_eq}' IS NULL
    OR (
      "account" = ("where" #>> '{account_id,_eq}') :: uuid
    )
  )
  AND (
    "where" #>> '{account_name,_eq}' IS NULL
    OR (
      "account_name" = ("where" #>> '{account_name,_eq}')
    )
  )
  AND (
    "where" #>> '{account_cloud_provider,_eq}' IS NULL
    OR (
      "account_cloud_provider" = ("where" #>> '{account_cloud_provider,_eq}')
    )
  )
  AND (
    "where" #>> '{region,_eq}' IS NULL
    OR (
      "region" = ("where" #>> '{region,_eq}')
    )
  )
  AND (
    "where" #>> '{service_name,_eq}' IS NULL
    OR (
      "service_name" = ("where" #>> '{service_name,_eq}')
    )
  )
  AND (
    "where" #>> '{spend_date,_eq}' IS NULL
    OR (
      "spend_date"::date = ("where" #>> '{spend_date,_eq}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_gt}' IS NULL
    OR (
      "spend_date"::date > ("where" #>> '{spend_date,_eq}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_lt}' IS NULL
    OR (
      "spend_date"::date < ("where" #>> '{spend_date,_eq}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_le}' IS NULL
    OR (
      "spend_date"::date <= ("where" #>> '{spend_date,_eq}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_ge}' IS NULL
    OR (
      "spend_date"::date >= ("where" #>> '{spend_date,_eq}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_between,le}' IS NULL
    OR (
      "spend_date"::date <= ("where" #>> '{spend_date,_between,le}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_between,lt}' IS NULL
    OR (
      "spend_date"::date < ("where" #>> '{spend_date,_between,lt}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_between,gt}' IS NULL
    OR (
      "spend_date"::date > ("where" #>> '{spend_date,_between,gt}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_between,ge}' IS NULL
    OR (
      "spend_date"::date >= ("where" #>> '{spend_date,_between,ge}')::date
    )
  )
group by
  (
    CASE
      WHEN 'tenant_id' = ANY(group_by) THEN tenant
    END
  ),
  (
    CASE
      WHEN 'account_id' = ANY(group_by) THEN account
    END
  ),
  (
    CASE
      WHEN 'service_name' = ANY(group_by) THEN service_name
    END
  ),
  (
    CASE
      WHEN 'account_name' = ANY(group_by) THEN account_name
    END
  ),
  (
    CASE
      WHEN 'account_cloud_provider' = ANY(group_by) THEN account_cloud_provider
    END
  ),
  (
    CASE
      WHEN 'region' = ANY(group_by) THEN region
    END
  ),
  (
    CASE
      WHEN 'spend_date' = ANY(group_by) THEN spend_date
    END
  )
  $function$;
