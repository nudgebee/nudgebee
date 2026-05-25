
alter table "public"."user_roles" add column "tenant_id" uuid
 null;

update user_roles
set tenant_id = entity_id
where entity_type = 'tenant';
