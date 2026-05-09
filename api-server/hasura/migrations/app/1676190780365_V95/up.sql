
CREATE
OR REPLACE VIEW "public"."cloud_resource_metrics_daily_aggregate" AS
    select crm."timestamp"::date, cr.tenant, cr.account, crm.cloud_resource_id, crm.metric, min(crm.value) as min_value, max(crm.value) as max_value, avg(crm.value) as avg_value 
    from cloud_resource_metrics crm
    join cloud_resourses cr
        on cr.id = crm.cloud_resource_id
    group by crm."timestamp"::date, cr.tenant, cr.account, crm.cloud_resource_id, crm.metric;
