
CREATE
OR REPLACE VIEW "public"."cloud_resourses_with_recommendations" AS
    select cloud_resourses.tenant, cloud_accounts.account_name, cloud_accounts.id, cloud_resourses."type", cloud_resourses.name, recommendation.severity 
    from cloud_resourses
    join cloud_accounts on cloud_resourses.account = cloud_accounts.id
    join recommendation on recommendation.resource_id = cloud_resourses.id;
