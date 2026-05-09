
CREATE OR REPLACE FUNCTION public.recommendation_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, "limit" integer DEFAULT 100, "offset" integer DEFAULT 0, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF recommendation_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
SELECT
	( CASE WHEN 'tenant_id' = ANY(group_by) THEN r.tenant_id ELSE null end ) AS tenant_id,
	( CASE WHEN 'account_id' = ANY(group_by) THEN r.cloud_account_id ELSE null END) AS account_id,
	( CASE WHEN 'account_name' = ANY(group_by) THEN ca.account_name ELSE null END) AS account_name,
	( CASE WHEN 'account_cloud_provider' = ANY(group_by) THEN ca.cloud_provider ELSE null END) AS account_cloud_provider,
	( CASE WHEN 'resource_id' = ANY(group_by) THEN r.resource_id ELSE null END) AS resource_id,
	( CASE WHEN 'resource_name' = ANY(group_by) THEN cr."name" ELSE null END) AS resource_name,
	( CASE WHEN 'resource_service_name' = ANY(group_by) THEN cr.service_name ELSE null END) AS resource_service_name,
	( CASE WHEN 'resource_region' = ANY(group_by) THEN cr.region ELSE null END) AS resource_region,
	( CASE WHEN 'spend_date' = ANY(group_by) THEN s."date" ELSE null END) AS spend_date,
	( CASE WHEN 'recommendation_category' = ANY(group_by) THEN r.category ELSE null END) AS recommendation_category,
	( CASE WHEN 'recommendation_rule_name' = ANY(group_by) THEN r.rule_name ELSE null END) AS recommendation_rule_name,
	( CASE WHEN 'recommendation_status' = ANY(group_by) THEN r.status ELSE null END) AS recommendation_status,
	count(distinct cr.id) AS resource_count,
	count(distinct r.id) AS recommendation_count,
	sum(s.amount) AS spend_amount,
	avg(r.estimated_savings) AS estimated_saving
FROM recommendation r
left join cloud_resourses cr on cr.id = r.resource_id 
left join cloud_accounts ca on ca.id = r.cloud_account_id
left join spends s on s.cloud_resource_id = r.resource_id and s.cloud_account = r.cloud_account_id 
WHERE ("hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL OR ( r.tenant_id = ("hasura_session" ->> 'x-hasura-user-tenant-id') :: uuid))
	AND ("where" #>> '{account_id,_eq}' IS null OR (r.cloud_account_id = ("where" #>> '{account_id,_eq}') :: uuid))
	AND ("where" #>> '{account_name,_eq}' IS null OR ( ca.account_name = ("where" #>> '{account_name,_eq}')))
	AND ("where" #>> '{account_cloud_provider,_eq}' IS NULL OR (ca.cloud_provider = ("where" #>> '{account_cloud_provider,_eq}')))
	AND ("where" #>> '{resource_region,_eq}' IS null OR (cr.region = ("where" #>> '{resource_region,_eq}')))
	AND ("where" #>> '{resource_id,_eq}' IS null OR ( r.resource_id = ("where" #>> '{resource_id,_eq}') :: uuid ))
	AND ("where" #>> '{resource_name,_eq}' IS null OR ( cr."name" = ("where" #>> '{resource_name,_eq}')))
	AND ("where" #>> '{resource_service_name,_eq}' IS null OR ( cr.service_name = ("where" #>> '{resource_service_name,_eq}')))
	AND ("where" #>> '{resource_service_name,_ne}' IS null OR ( cr.service_name != ("where" #>> '{resource_service_name,_ne}')))
	AND ("where" #>> '{recommendation_category,_eq}' IS null OR ( r.category = ("where" #>> '{recommendation_category,_eq}')))
	AND ("where" #>> '{recommendation_rule_name,_eq}' IS null OR ( r.rule_name = ("where" #>> '{recommendation_rule_name,_eq}')))
	AND ("where" #>> '{recommendation_status,_eq}' IS null OR ( r.status = ("where" #>> '{recommendation_status,_eq}')))
	AND ("where" #>> '{spend_date,_eq}' IS NULL OR (s."date" :: date = ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_gt}' IS NULL OR (s."date" :: date > ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_lt}' IS NULL OR (s."date" :: date < ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_le}' IS NULL OR (s."date" :: date <= ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_ge}' IS NULL OR (s."date" :: date >= ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_between,le}' IS NULL OR (s."date" :: date <= ("where" #>> '{spend_date,_between,le}')::date))
	AND ("where" #>> '{spend_date,_between,lt}' IS null OR (s."date" :: date < ("where" #>> '{spend_date,_between,lt}')::date))
	AND ("where" #>> '{spend_date,_between,gt}' IS NULL OR (s."date" :: date > ("where" #>> '{spend_date,_between,gt}')::date))
	AND ("where" #>> '{spend_date,_between,ge}' IS null OR (s."date" :: date >= ("where" #>> '{spend_date,_between,ge}')::date))
	AND ("where" #>> '{estimated_saving,_gt}' IS null OR (r."estimated_savings" > ("where" #>> '{estimated_saving,_gt}')::float))
	AND ("where" #>> '{estimated_saving,_ge}' IS null OR (r."estimated_savings" >= ("where" #>> '{estimated_saving,_ge}')::float))
	AND ("where" #>> '{estimated_saving,_lt}' IS null OR (r."estimated_savings" < ("where" #>> '{estimated_saving,_lt}')::float))
	AND ("where" #>> '{estimated_saving,_le}' IS null OR (r."estimated_savings" <= ("where" #>> '{estimated_saving,_le}')::float))
GROUP BY
	(CASE WHEN 'tenant_id' = ANY(group_by) THEN r.tenant_id END),
	(CASE WHEN 'account_id' = ANY(group_by) THEN r.cloud_account_id END),
	(CASE WHEN 'account_name' = ANY(group_by) THEN ca.account_name END),
	(CASE WHEN 'account_cloud_provider' = ANY(group_by) THEN ca.cloud_provider END),
	(CASE WHEN 'resource_id' = ANY(group_by) THEN r.resource_id END),
	(CASE WHEN 'resource_name' = ANY(group_by) THEN cr."name" END),
	(CASE WHEN 'resource_service_name' = ANY(group_by) THEN cr.service_name END),
	(CASE WHEN 'resource_region' = ANY(group_by) THEN cr.region END),
	(CASE WHEN 'recommendation_category' = ANY(group_by) THEN r.category END),
	(CASE WHEN 'recommendation_rule_name' = ANY(group_by) THEN r.rule_name END),
	(CASE WHEN 'recommendation_status' = ANY(group_by) THEN r.status END),
	(CASE WHEN 'spend_date' = ANY(group_by) THEN s."date" end)
ORDER BY avg(r.estimated_savings) desc
limit "limit" offset "offset"
$function$;
