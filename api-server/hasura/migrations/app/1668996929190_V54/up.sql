
alter table "public"."cloud_resourses" alter column "status" set default 'Active';

UPDATE spends SET cloud_resource_id = cloud_resourses.id FROM cloud_resourses WHERE spends.cloud_account  = cloud_resourses.account and spends.resource_name = cloud_resourses.arn;

update cloud_resourses set status = 'Active';

UPDATE spends SET cloud_resource_id = cloud_resourses.id FROM cloud_resourses WHERE spends.cloud_resource_id is null and spends.cloud_account  = cloud_resourses.account and spends.resource_name = cloud_resourses.name;


