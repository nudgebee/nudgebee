
alter table "public"."tenant" drop constraint "tenant_name_key";
alter table "public"."tenant" add constraint "tenant_name_created_by_key" unique ("name", "created_by");
