CREATE MATERIALIZED VIEW public.cloudaccount_k8s_security_recommendation
TABLESPACE pg_default
AS select
	container->>'image' as image,
	cr.id as id,
	r.tenant_id as tenant_id,
	cr.account as account_id,
	cr."name" as pod_name,
	cr.is_active ,
	cr.meta ->> 'controller'::text as controller_name,
	cr.meta ->> 'controllerKind'::text as controller_kind,
	cr.meta ->> 'namespace' as namespace,
	r.recommendation,
	r.id as recommendation_id,
	r.severity,
	       case
		when r.severity = 'Critical' then 10
		when r.severity = 'High' then 8
		when r.severity = 'Medium' then 5
		when r.severity = 'Low' then 2
		when r.severity = 'Info' then 1
		else 0
	end as severity_weight,
	r.status
from
	cloud_resourses cr ,
	lateral jsonb_array_elements(cr.meta->'config'->'containers') as container
inner join recommendation r on
	r.account_object_id = container->>'image'
where
	cr."type" = 'Pod'
	and r.cloud_account_id = cr.account
	and r.tenant_id = cr.tenant;
	
-- View indexes:
CREATE UNIQUE INDEX ccloudaccount_k8s_security_recommendation_pk ON public.cloudaccount_k8s_security_recommendation USING btree (image,id, tenant_id,recommendation_id, account_id);
