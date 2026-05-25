
alter table "public"."tenant" drop constraint "tenant_name_created_by_key";
alter table "public"."tenant" add constraint "tenant_name_key" unique ("name");
